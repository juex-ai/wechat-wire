package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
	"github.com/juex-ai/wechat-wire/cli/internal/config"
	"github.com/juex-ai/wechat-wire/cli/internal/contextguard"
	"github.com/juex-ai/wechat-wire/cli/internal/media"
	"github.com/juex-ai/wechat-wire/cli/internal/session"
	"github.com/juex-ai/wechat-wire/cli/internal/status"
)

const channelCapabilityName = "claude/channel"
const mediaDownloadTimeout = 2 * time.Minute

// Server is the MCP stdio bridge for WeChat iLink Bot messages.
type Server struct {
	mcpServer  *sdkmcp.Server
	mcpSession *sdkmcp.ServerSession
	transport  *channelTransport

	version      string
	forceChannel bool
	factory      bot.Factory

	mu           sync.Mutex
	runCtx       context.Context
	botClient    bot.Client
	botInit      *botInitAttempt
	channelReady bool
	runtime      *session.Session
	guard        *contextguard.Runner
}

type botInitAttempt struct {
	done   chan struct{}
	client bot.Client
	err    error
}

// NewServer creates an MCP server.
func NewServer(version string, forceChannel bool, factory bot.Factory) *Server {
	if factory == nil {
		factory = bot.NewFromEnv
	}
	return &Server{version: version, forceChannel: forceChannel, factory: factory}
}

// Run starts the MCP server and blocks until its context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()

	s.mcpServer = sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "wechat-wire",
		Version: s.version,
	}, &sdkmcp.ServerOptions{
		Instructions: `You are connected to wechat-wire, a WeChat iLink Bot MCP bridge.

Incoming WeChat messages arrive as channel notifications when the client supports the experimental claude/channel capability or the server is started with --channel.

Available tools:
- wechat_wire_status: inspect runtime status.
- wechat_wire_list_users: list locally observed WeChat users.
- wechat_wire_send_message: send a text message to a user. The active MCP process must have received context for that user from WeChat first.
- wechat_wire_send_attachment: send an image, video, or file from a readable local path. The file extension selects the WeChat media type.
- wechat_wire_send_typing: show or stop the typing indicator for a user.
- wechat_wire_forget_user: remove a user from the local user book.
- wechat_wire_get_context_guard: inspect proactive context expiry reminder settings.
- wechat_wire_configure_context_guard: update the reminder switch, timing window, timezone, or message template.
`,
		Capabilities:       s.serverCapabilities(),
		InitializedHandler: s.onInitialized,
		Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	s.registerTools()
	s.transport = newChannelTransport(&sdkmcp.StdioTransport{})
	return s.mcpServer.Run(ctx, s.transport)
}

func (s *Server) registerTools() {
	type statusArgs struct{}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_status",
		Description: "Show current wechat-wire version, work directory, credential path, login status, and known user count.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args statusArgs) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: status.Runtime(s.version)}}}, nil, nil
	})

	type listUsersArgs struct{}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_list_users",
		Description: "List locally observed WeChat users and their last message metadata.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args listUsersArgs) (*sdkmcp.CallToolResult, any, error) {
		users, err := s.getSessionModule().ListUsers()
		if err != nil {
			return toolError(err), nil, nil
		}
		if len(users) == 0 {
			return toolText("No users observed yet."), nil, nil
		}
		lines := make([]string, 0, len(users))
		for _, user := range users {
			lines = append(lines, fmt.Sprintf("%s last_seen=%s messages=%d type=%s has_context=%t text=%q",
				user.UserID, safeISO(user.LastSeenAt), user.MessageCount, user.LastType, user.HasContext, user.LastText))
		}
		return toolText(strings.Join(lines, "\n")), nil, nil
	})

	type sendMessageArgs struct {
		UserID string `json:"user_id" jsonschema:"WeChat user ID observed from an incoming message"`
		Text   string `json:"text" jsonschema:"Text message to send"`
	}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_send_message",
		Description: "Send a text message to a WeChat user. Requires active context from a message received in this process.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args sendMessageArgs) (*sdkmcp.CallToolResult, any, error) {
		if args.UserID == "" {
			return toolError(fmt.Errorf("user_id is required")), nil, nil
		}
		if args.Text == "" {
			return toolError(fmt.Errorf("text is required")), nil, nil
		}
		client, err := s.ensureBot(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		result, err := s.getSessionModule().SendText(ctx, args.UserID, args.Text, session.WithClient(client))
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolText(fmt.Sprintf("ok: sent to %s", result.UserID)), nil, nil
	})

	type sendAttachmentArgs struct {
		UserID   string `json:"user_id" jsonschema:"WeChat user ID observed from an incoming message"`
		Path     string `json:"path" jsonschema:"Readable local path to the image, video, or file to send"`
		FileName string `json:"file_name,omitempty" jsonschema:"Optional recipient-facing base file name; its extension selects the media type"`
		Caption  string `json:"caption,omitempty" jsonschema:"Optional text sent with the attachment"`
	}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_send_attachment",
		Description: "Send a local image, video, or file to a WeChat user. Requires current context from an incoming message; images and videos are selected by file extension.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args sendAttachmentArgs) (*sdkmcp.CallToolResult, any, error) {
		if args.UserID == "" {
			return toolError(fmt.Errorf("user_id is required")), nil, nil
		}
		if args.Path == "" {
			return toolError(fmt.Errorf("path is required")), nil, nil
		}
		client, err := s.ensureBot(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		result, err := s.getSessionModule().SendAttachment(ctx, args.UserID, session.Attachment{
			Path:     args.Path,
			FileName: args.FileName,
			Caption:  args.Caption,
		}, session.WithClient(client))
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolText(fmt.Sprintf("ok: sent %s (%d bytes) to %s", result.FileName, result.SizeBytes, result.UserID)), nil, nil
	})

	type typingArgs struct {
		UserID string `json:"user_id" jsonschema:"WeChat user ID observed from an incoming message"`
		Active bool   `json:"active" jsonschema:"true to show typing, false to stop typing"`
	}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_send_typing",
		Description: "Show or stop the WeChat typing indicator for a user.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args typingArgs) (*sdkmcp.CallToolResult, any, error) {
		if args.UserID == "" {
			return toolError(fmt.Errorf("user_id is required")), nil, nil
		}
		client, err := s.ensureBot(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		if args.Active {
			if err := client.SendTyping(ctx, args.UserID); err != nil {
				return toolError(err), nil, nil
			}
			return toolText(fmt.Sprintf("ok: typing started for %s", args.UserID)), nil, nil
		}
		if err := client.StopTyping(ctx, args.UserID); err != nil {
			return toolError(err), nil, nil
		}
		return toolText(fmt.Sprintf("ok: typing stopped for %s", args.UserID)), nil, nil
	})

	type forgetUserArgs struct {
		UserID string `json:"user_id" jsonschema:"WeChat user ID to forget locally"`
	}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_forget_user",
		Description: "Remove a WeChat user from the local user book.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args forgetUserArgs) (*sdkmcp.CallToolResult, any, error) {
		if args.UserID == "" {
			return toolError(fmt.Errorf("user_id is required")), nil, nil
		}
		removed, err := s.getSessionModule().ForgetUser(args.UserID)
		if err != nil {
			return toolError(err), nil, nil
		}
		if !removed {
			return toolText(fmt.Sprintf("not found: %s", args.UserID)), nil, nil
		}
		return toolText(fmt.Sprintf("forgot: %s", args.UserID)), nil, nil
	})

	type getContextGuardArgs struct{}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_get_context_guard",
		Description: "Show proactive context-token expiry reminder settings. Context tokens are never returned.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args getContextGuardArgs) (*sdkmcp.CallToolResult, any, error) {
		guardConfig, err := contextguard.ReadConfig(config.ContextGuardConfigPath())
		if err != nil {
			return toolError(err), nil, nil
		}
		text, err := indentedJSON(guardConfig)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolText(text), nil, nil
	})

	type configureContextGuardArgs struct {
		Enabled            *bool   `json:"enabled,omitempty" jsonschema:"Enable or disable proactive context expiry reminders"`
		AssumedTTLMinutes  *int    `json:"assumed_ttl_minutes,omitempty" jsonschema:"Assumed context token lifetime in minutes"`
		LeadTimeMinutes    *int    `json:"lead_time_minutes,omitempty" jsonschema:"Minutes before estimated expiry to send the reminder"`
		Timezone           *string `json:"timezone,omitempty" jsonschema:"IANA timezone used by the reminder window, for example Asia/Shanghai"`
		ReminderWindowFrom *string `json:"reminder_window_from,omitempty" jsonschema:"Earliest local reminder time in HH:MM"`
		ReminderWindowTo   *string `json:"reminder_window_to,omitempty" jsonschema:"Latest local reminder time in HH:MM; quiet-hour reminders move earlier to this time"`
		MessageTemplate    *string `json:"message_template,omitempty" jsonschema:"Reminder text supporting {{remaining_minutes}}, {{assumed_ttl}}, {{expires_at}}, and {{user_id}}"`
	}
	sdkmcp.AddTool(s.mcpServer, &sdkmcp.Tool{
		Name:        "wechat_wire_configure_context_guard",
		Description: "Partially update proactive context expiry reminder settings. Omitted settings remain unchanged.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args configureContextGuardArgs) (*sdkmcp.CallToolResult, any, error) {
		if args.Enabled == nil &&
			args.AssumedTTLMinutes == nil &&
			args.LeadTimeMinutes == nil &&
			args.Timezone == nil &&
			args.ReminderWindowFrom == nil &&
			args.ReminderWindowTo == nil &&
			args.MessageTemplate == nil {
			return toolError(fmt.Errorf("at least one context guard setting is required")), nil, nil
		}
		guardConfig, err := contextguard.UpdateConfig(config.ContextGuardConfigPath(), contextguard.ConfigPatch{
			Enabled:            args.Enabled,
			AssumedTTLMinutes:  args.AssumedTTLMinutes,
			LeadTimeMinutes:    args.LeadTimeMinutes,
			Timezone:           args.Timezone,
			ReminderWindowFrom: args.ReminderWindowFrom,
			ReminderWindowTo:   args.ReminderWindowTo,
			MessageTemplate:    args.MessageTemplate,
		})
		if err != nil {
			return toolError(err), nil, nil
		}
		s.wakeContextGuard()
		text, err := indentedJSON(guardConfig)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolText(text), nil, nil
	})
}

func (s *Server) onInitialized(ctx context.Context, req *sdkmcp.InitializedRequest) {
	s.setSession(req.Session)
	allowed := s.channelAllowed(req.Session)
	s.setChannelReady(allowed)
	if !allowed {
		s.log("client did not declare %s capability; notifications disabled", channelCapabilityName)
	}
	s.log("starting WeChat listener")
	s.startBot(ctx)
}

func (s *Server) startBot(ctx context.Context) {
	go func() {
		if _, err := s.ensureBot(ctx); err != nil && ctx.Err() == nil {
			s.sendChannelNotification(fmt.Sprintf("wechat-wire listener failed: %v", err), "error", "", "")
		}
	}()
}

func (s *Server) ensureBot(ctx context.Context) (bot.Client, error) {
	client, attempt, baseCtx, owner := s.claimBotInit(ctx)
	if client != nil {
		return client, nil
	}
	if !owner {
		return attempt.await(ctx)
	}

	s.initializeBot(baseCtx, attempt)
	return attempt.await(ctx)
}

func (s *Server) claimBotInit(ctx context.Context) (bot.Client, *botInitAttempt, context.Context, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.botClient != nil {
		return s.botClient, nil, nil, false
	}
	if s.botInit != nil {
		return nil, s.botInit, nil, false
	}
	baseCtx := s.runCtx
	if baseCtx == nil {
		baseCtx = ctx
	}
	attempt := &botInitAttempt{done: make(chan struct{})}
	s.botInit = attempt
	return nil, attempt, baseCtx, true
}

func (s *Server) initializeBot(baseCtx context.Context, attempt *botInitAttempt) {
	client := s.getSessionModule().NewClient()
	client.OnMessage(func(msg *bot.IncomingMessage) {
		s.handleMessage(baseCtx, client, msg)
	})
	if _, err := client.Login(baseCtx, false); err != nil {
		s.finishBotInit(attempt, nil, err)
		return
	}
	s.startContextGuard(baseCtx, client)
	go func() {
		if err := client.Run(baseCtx); err != nil && baseCtx.Err() == nil {
			s.log("listener exited: %v", err)
			s.sendChannelNotification(fmt.Sprintf("wechat-wire listener exited: %v", err), "error", "", "")
		}
	}()
	s.finishBotInit(attempt, client, nil)
}

func (s *Server) finishBotInit(attempt *botInitAttempt, client bot.Client, err error) {
	s.mu.Lock()
	if err == nil {
		s.botClient = client
	}
	if s.botInit == attempt {
		s.botInit = nil
	}
	s.mu.Unlock()

	attempt.client = client
	attempt.err = err
	close(attempt.done)
}

func (a *botInitAttempt) await(ctx context.Context) (bot.Client, error) {
	select {
	case <-a.done:
		return a.client, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) botOptions() bot.Options {
	return bot.Options{
		CredPath: config.CredentialPath(),
		LogLevel: config.LogLevel(),
		BotAgent: config.BotAgent(s.version),
		OnQRURL: func(url string) {
			s.log("scan QR URL: %s", url)
			s.sendLoginNotification(
				fmt.Sprintf("wechat-wire login required. Scan this QR URL with WeChat to continue:\n%s", url),
				"login_required",
			)
		},
		OnScanned: func() {
			s.log("QR scanned")
			s.sendLoginNotification("wechat-wire login QR code scanned. Confirm the login in WeChat to continue.", "login_scanned")
		},
		OnExpired: func() {
			s.log("QR expired")
			s.sendLoginNotification("wechat-wire login QR code expired. Restart the MCP server to request a new QR URL.", "login_expired")
		},
		OnError: func(err error) {
			s.log("sdk error: %v", err)
		},
		OnVerifyCode: func(isRetry bool) (string, error) {
			return "", fmt.Errorf("verification code required; run wechat-wire login --force first")
		},
	}
}

func (s *Server) sendLoginNotification(content, eventType string) {
	s.sendChannelNotification(content, eventType, "", "login")
}

func (s *Server) handleMessage(ctx context.Context, client bot.Client, msg *bot.IncomingMessage) {
	if msg == nil {
		return
	}
	if err := s.getSessionModule().RememberMessage(*msg); err != nil {
		s.log("remember user: %v", err)
	}
	s.wakeContextGuard()
	if isMediaMessage(msg.Type) {
		go s.downloadAndNotifyMedia(ctx, client, msg)
		return
	}
	s.sendMessageNotification(msg, nil, nil)
}

func (s *Server) startContextGuard(ctx context.Context, client bot.Client) {
	s.mu.Lock()
	if s.guard != nil {
		s.mu.Unlock()
		return
	}
	guard := contextguard.NewRunner(contextguard.RunnerConfig{
		ConfigPath: config.ContextGuardConfigPath(),
		StatePath:  config.ContextGuardStatePath(),
		UsersPath:  config.UsersPath(),
		Send: func(sendCtx context.Context, userID, text string, expected contextguard.ContextReference) error {
			_, err := s.getSessionModule().SendTextForContext(
				sendCtx,
				userID,
				text,
				session.ContextReference{Token: expected.Token, ObservedAt: expected.ObservedAt},
				session.WithClient(client),
			)
			return err
		},
		OnEvent: s.handleContextGuardEvent,
	})
	s.guard = guard
	s.mu.Unlock()

	go func() {
		if err := guard.Run(ctx); err != nil && ctx.Err() == nil {
			s.log("context guard exited: %v", err)
		}
	}()
}

func (s *Server) handleContextGuardEvent(event contextguard.Event) {
	switch event.Type {
	case contextguard.StatusSent:
		content := fmt.Sprintf(
			"wechat-wire sent a context expiry reminder to user_id=%s; estimated expiry=%s",
			event.UserID,
			event.EstimatedExpiresAt.Format(time.RFC3339),
		)
		s.log("%s", content)
	case contextguard.StatusFailed:
		content := fmt.Sprintf("wechat-wire context expiry reminder failed for user_id=%s: %v", event.UserID, event.Error)
		s.log("%s", content)
	case "error":
		s.log("context guard check failed: %v", event.Error)
	}
}

func (s *Server) downloadAndNotifyMedia(ctx context.Context, client bot.Client, msg *bot.IncomingMessage) {
	downloadCtx, cancel := context.WithTimeout(ctx, mediaDownloadTimeout)
	defer cancel()

	artifact, err := s.getSessionModule().DownloadMedia(downloadCtx, client, msg)
	if err == nil && artifact == nil {
		err = fmt.Errorf("message contains no downloadable media")
	}
	if err != nil {
		s.log("download %s from %s: %v", msg.Type, msg.UserID, err)
	}
	s.sendMessageNotification(msg, artifact, err)
}

func (s *Server) sendMessageNotification(msg *bot.IncomingMessage, artifact *media.Artifact, downloadErr error) {
	lines := []string{fmt.Sprintf("wechat-wire message from user_id=%s type=%s at %s",
		msg.UserID, msg.Type, msg.Timestamp.Local().Format("2006-01-02 15:04:05"))}
	if msg.Text != "" {
		lines = append(lines, msg.Text)
	}
	meta := channelNotificationMeta{
		EventType:   "message",
		UserID:      msg.UserID,
		MessageType: msg.Type,
	}
	var attachments []channelAttachment
	if artifact != nil {
		lines = append(lines, "local_path: "+artifact.LocalPath)
		if artifact.FileName != "" {
			lines = append(lines, "file_name: "+artifact.FileName)
		}
		attachments = []channelAttachment{{Path: artifact.LocalPath}}
		meta.LocalPath = artifact.LocalPath
		meta.FileName = artifact.FileName
		meta.MediaType = artifact.MediaType
		meta.MediaSizeBytes = artifact.Size
	}
	if downloadErr != nil {
		lines = append(lines, "media_download_error: "+downloadErr.Error())
		meta.MediaType = msg.Type
		meta.MediaDownloadError = downloadErr.Error()
	}
	s.sendNotification(channelNotification{
		Content:     strings.Join(lines, "\n"),
		Meta:        meta,
		Attachments: attachments,
	})
}

func isMediaMessage(messageType string) bool {
	switch messageType {
	case "image", "voice", "file", "video":
		return true
	default:
		return false
	}
}

func (s *Server) setSession(session *sdkmcp.ServerSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpSession = session
}

func (s *Server) getSessionModule() *session.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil {
		return s.runtime
	}
	s.runtime = session.New(session.Config{
		UsersPath:  config.UsersPath(),
		MediaDir:   config.MediaDir(),
		Factory:    s.factory,
		BotOptions: s.botOptions(),
	})
	return s.runtime
}

func (s *Server) getSession() *sdkmcp.ServerSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpSession
}

func (s *Server) setChannelReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelReady = ready
}

func (s *Server) canNotifyChannel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channelReady
}

func (s *Server) sendChannelNotification(content, eventType, userID, messageType string) {
	s.sendNotification(channelNotification{
		Content: content,
		Meta: channelNotificationMeta{
			EventType:   eventType,
			UserID:      userID,
			MessageType: messageType,
		},
	})
}

func (s *Server) sendNotification(params channelNotification) {
	if !s.canNotifyChannel() || s.getSession() == nil || s.transport == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.transport.Notify(ctx, "notifications/claude/channel", params); err != nil {
		s.log("failed to send notification: %v", err)
	}
}

func (s *Server) serverCapabilities() *sdkmcp.ServerCapabilities {
	return &sdkmcp.ServerCapabilities{
		Tools: &sdkmcp.ToolCapabilities{ListChanged: true},
		Experimental: map[string]any{
			channelCapabilityName: map[string]any{},
		},
	}
}

func supportsChannel(session *sdkmcp.ServerSession) bool {
	if session == nil {
		return false
	}
	params := session.InitializeParams()
	if params == nil || params.Capabilities == nil {
		return false
	}
	_, ok := params.Capabilities.Experimental[channelCapabilityName]
	return ok
}

func (s *Server) channelAllowed(session *sdkmcp.ServerSession) bool {
	return s.forceChannel || supportsChannel(session)
}

func (s *Server) log(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[wechat-wire] "+format+"\n", args...)
}

func (s *Server) wakeContextGuard() {
	s.mu.Lock()
	guard := s.guard
	s.mu.Unlock()
	if guard != nil {
		guard.Wake()
	}
}

func indentedJSON(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode JSON: %w", err)
	}
	return string(data), nil
}

func toolText(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}
}

func toolError(err error) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: fmt.Sprintf("error: %v", err)}},
		IsError: true,
	}
}

func safeISO(sec int64) string {
	if sec == 0 {
		return "(never)"
	}
	return time.Unix(sec, 0).Local().Format(time.RFC3339)
}
