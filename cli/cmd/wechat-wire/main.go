// Command wechat-wire is a CLI and MCP bridge for WeChat iLink Bot.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
	"github.com/juex-ai/wechat-wire/cli/internal/config"
	"github.com/juex-ai/wechat-wire/cli/internal/mcp"
	"github.com/juex-ai/wechat-wire/cli/internal/session"
	"github.com/juex-ai/wechat-wire/cli/internal/status"
	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

// Build-time metadata injected via:
//
//	go build -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT"
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var homeDir string

	root := &cobra.Command{
		Use:          "wechat-wire",
		Short:        "wechat-wire — WeChat iLink Bot CLI and MCP bridge",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return config.SetHomeDir(homeDir)
		},
	}
	root.PersistentFlags().StringVar(&homeDir, "homedir", "", "Base directory for wechat-wire config; final path is .config/wechat-wire")

	root.AddCommand(versionCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(loginCmd())
	root.AddCommand(listenCmd())
	root.AddCommand(msgCmd())
	root.AddCommand(userCmd())
	root.AddCommand(mcpCmd())

	return root
}

func versionCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), map[string]string{"version": version, "commit": commit})
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), status.Version(version, commit))
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	return cmd
}

func statusCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print runtime configuration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), status.RuntimeInfo(version))
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), status.Runtime(version))
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	return cmd
}

func loginCmd() *cobra.Command {
	var force bool
	var format string
	var verifyCode string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to WeChat iLink Bot using the upstream SDK QR flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			runtime := newSession(cmd.ErrOrStderr(), verifyCode)
			client := runtime.NewClient()
			creds, err := client.Login(cmd.Context(), force)
			if err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), creds)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "logged in: account_id=%s user_id=%s\n", creds.AccountID, creds.UserID)
			return err
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force QR login instead of reusing saved credentials")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	cmd.Flags().StringVar(&verifyCode, "verify-code", "", "Pairing code for non-interactive login when WeChat requests one")
	return cmd
}

func listenCmd() *cobra.Command {
	var once bool
	var format string
	var verifyCode string
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Login and stream incoming WeChat messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if once {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				stop = cancel
			}

			runtime := newSession(cmd.ErrOrStderr(), verifyCode)
			client := runtime.NewClient()
			if _, err := client.Login(ctx, false); err != nil {
				return err
			}
			client.OnMessage(func(msg *bot.IncomingMessage) {
				if err := runtime.RememberMessage(*msg); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "remember user: %v\n", err)
				}
				printMessage(cmd.OutOrStdout(), format, msg)
				if once {
					stop()
				}
			})
			if err := client.Run(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "Exit after the first incoming message")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	cmd.Flags().StringVar(&verifyCode, "verify-code", "", "Pairing code for non-interactive login when WeChat requests one")
	return cmd
}

func msgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "msg",
		Short: "Send messages to observed WeChat users",
	}
	cmd.AddCommand(msgSendCmd())
	return cmd
}

func msgSendCmd() *cobra.Command {
	var userID string
	var text string
	var content string
	var filePath string
	var fileName string
	var caption string
	var format string
	var verifyCode string

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send text or a local attachment to an observed WeChat user",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			if userID == "" {
				return fmt.Errorf("--user_id is required")
			}
			messageText := text
			if messageText == "" {
				messageText = content
			}
			if messageText == "" && filePath == "" {
				return fmt.Errorf("one of --text or --file is required")
			}
			if messageText != "" && filePath != "" {
				return fmt.Errorf("--text and --file cannot be used together")
			}
			if filePath == "" && (fileName != "" || caption != "") {
				return fmt.Errorf("--file_name and --caption require --file")
			}

			runtime := newSession(cmd.ErrOrStderr(), verifyCode)
			var result any
			var err error
			if filePath != "" {
				result, err = runtime.SendAttachment(cmd.Context(), userID, session.Attachment{
					Path:     filePath,
					FileName: fileName,
					Caption:  caption,
				})
			} else {
				result, err = runtime.SendText(cmd.Context(), userID, messageText)
			}
			if errors.Is(err, session.ErrUserNotObserved) {
				return fmt.Errorf("user not found: %s; run wechat-wire listen first", userID)
			}
			if errors.Is(err, session.ErrContextMissing) {
				return fmt.Errorf("no context_token for user %s; run wechat-wire listen to receive a fresh message first", userID)
			}
			if err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			if attachmentResult, ok := result.(*session.AttachmentResult); ok {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: sent %s (%d bytes) to %s\n",
					attachmentResult.FileName, attachmentResult.SizeBytes, attachmentResult.UserID)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: sent to %s\n", userID)
			return err
		},
	}
	cmd.Flags().StringVar(&userID, "user_id", "", "WeChat user ID observed from incoming messages (required)")
	cmd.Flags().StringVar(&text, "text", "", "Text message to send instead of --file")
	cmd.Flags().StringVar(&content, "content", "", "Alias for --text")
	cmd.Flags().StringVar(&filePath, "file", "", "Local image, video, or file path to send")
	cmd.Flags().StringVar(&fileName, "file_name", "", "Optional recipient-facing base file name")
	cmd.Flags().StringVar(&caption, "caption", "", "Optional attachment caption")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	cmd.Flags().StringVar(&verifyCode, "verify-code", "", "Pairing code for non-interactive login when WeChat requests one")
	return cmd
}

func userCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "View and manage locally observed WeChat users"}
	cmd.AddCommand(userListCmd())
	cmd.AddCommand(userShowCmd())
	cmd.AddCommand(userForgetCmd())
	return cmd
}

func userListCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List locally observed WeChat users",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			users, err := newSession(io.Discard, "").ListUsers()
			if err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"users": publicUserRecords(users)})
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-32s  %-19s  %-8s  %s\n", "USER_ID", "LAST_SEEN", "MESSAGES", "LAST_TEXT"); err != nil {
				return err
			}
			for _, user := range users {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-32s  %-19s  %-8d  %s\n", user.UserID, formatUnix(user.LastSeenAt), user.MessageCount, user.LastText); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table|json")
	return cmd
}

func userShowCmd() *cobra.Command {
	var userID string
	var format string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show one locally observed WeChat user",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			if userID == "" {
				return fmt.Errorf("--user_id is required")
			}
			user, ok, err := newSession(io.Discard, "").GetUser(userID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("user not found: %s", userID)
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), publicUserRecord(*user))
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "user_id:       %s\n", user.UserID); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "last_seen:     %s\n", formatUnix(user.LastSeenAt)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "message_count: %d\n", user.MessageCount); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "last_type:     %s\n", user.LastType); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "has_context:   %t\n", user.HasContext); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "last_text:     %s\n", user.LastText); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&userID, "user_id", "", "User ID to show (required)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	return cmd
}

func userForgetCmd() *cobra.Command {
	var userID string
	cmd := &cobra.Command{
		Use:   "forget",
		Short: "Remove one user from the local user book",
		RunE: func(cmd *cobra.Command, args []string) error {
			if userID == "" {
				return fmt.Errorf("--user_id is required")
			}
			removed, err := newSession(io.Discard, "").ForgetUser(userID)
			if err != nil {
				return err
			}
			if removed {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "forgot: %s\n", userID)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "not found: %s\n", userID)
			return err
		},
	}
	cmd.Flags().StringVar(&userID, "user_id", "", "User ID to forget (required)")
	return cmd
}

func mcpCmd() *cobra.Command {
	var forceChannel bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the wechat-wire MCP server (stdio)",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := mcp.NewServer(version, forceChannel, bot.NewFromEnv)
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&forceChannel, "channel", false, "Force claude/channel notifications even if the MCP client does not advertise the capability")
	return cmd
}

func newSession(events io.Writer, verifyCode string) *session.Session {
	return session.New(session.Config{
		UsersPath:  config.UsersPath(),
		MediaDir:   config.MediaDir(),
		Factory:    bot.NewFromEnv,
		BotOptions: botOptions(events, verifyCode),
	})
}

func botOptions(events io.Writer, verifyCode string) bot.Options {
	return bot.Options{
		CredPath: config.CredentialPath(),
		LogLevel: config.LogLevel(),
		BotAgent: config.BotAgent(version),
		OnQRURL: func(url string) {
			_, _ = fmt.Fprintf(events, "scan QR URL: %s\n", url)
		},
		OnScanned: func() {
			_, _ = fmt.Fprintln(events, "QR scanned")
		},
		OnExpired: func() {
			_, _ = fmt.Fprintln(events, "QR expired")
		},
		OnError: func(err error) {
			_, _ = fmt.Fprintf(events, "sdk error: %v\n", err)
		},
		OnVerifyCode: func(isRetry bool) (string, error) {
			if verifyCode != "" {
				return verifyCode, nil
			}
			prompt := "verify code"
			if isRetry {
				prompt = "verify code (retry)"
			}
			_, _ = fmt.Fprintf(events, "%s: ", prompt)
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return "", err
				}
				return "", fmt.Errorf("verification code required")
			}
			return strings.TrimSpace(scanner.Text()), nil
		},
	}
}

func printMessage(out io.Writer, format string, msg *bot.IncomingMessage) {
	if format == "json" {
		_ = writeJSON(out, msg)
		return
	}
	_, _ = fmt.Fprintf(out, "[%s] %s %s: %s\n", msg.Timestamp.Local().Format("2006-01-02 15:04:05"), msg.UserID, msg.Type, msg.Text)
}

type userRecordOutput struct {
	UserID       string `json:"user_id"`
	LastText     string `json:"last_text"`
	LastType     string `json:"last_type"`
	LastSeenAt   int64  `json:"last_seen_at"`
	MessageCount int    `json:"message_count"`
	HasContext   bool   `json:"has_context"`
}

func publicUserRecords(users []store.UserRecord) []userRecordOutput {
	out := make([]userRecordOutput, 0, len(users))
	for _, user := range users {
		out = append(out, publicUserRecord(user))
	}
	return out
}

func publicUserRecord(user store.UserRecord) userRecordOutput {
	return userRecordOutput{
		UserID:       user.UserID,
		LastText:     user.LastText,
		LastType:     user.LastType,
		LastSeenAt:   user.LastSeenAt,
		MessageCount: user.MessageCount,
		HasContext:   user.HasContext,
	}
}

func formatUnix(sec int64) string {
	if sec == 0 {
		return "(never)"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

func validateFormat(format string, allowed ...string) error {
	for _, v := range allowed {
		if format == v {
			return nil
		}
	}
	return fmt.Errorf("--format must be one of: %s", strings.Join(allowed, "|"))
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}
