// Package session concentrates the WeChat Session interface used by CLI and MCP.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
	"github.com/juex-ai/wechat-wire/cli/internal/media"
	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

const maxOutboundAttachmentBytes = 100 << 20

var (
	// ErrUserNotObserved means the User Book has no record for the requested user.
	ErrUserNotObserved = errors.New("user not observed")
	// ErrContextMissing means the User Book has a user record without a reply context.
	ErrContextMissing = errors.New("context token missing")
)

// Config is the interface to the WeChat Session Module.
type Config struct {
	UsersPath  string
	MediaDir   string
	Factory    bot.Factory
	BotOptions bot.Options
}

// Session is the deep Module that owns User Book and bot adapter choreography.
type Session struct {
	usersPath  string
	mediaDir   string
	factory    bot.Factory
	botOptions bot.Options
}

// SendOption adjusts one text send.
type SendOption func(*sendConfig)

type sendConfig struct {
	client bot.Client
}

// SendResult is returned after a text send completes.
type SendResult struct {
	Sent   bool   `json:"sent"`
	UserID string `json:"user_id"`
	Text   string `json:"text"`
}

// Attachment describes one local file to send.
type Attachment struct {
	Path     string
	FileName string
	Caption  string
}

// AttachmentResult is returned after an attachment send completes.
type AttachmentResult struct {
	Sent      bool   `json:"sent"`
	UserID    string `json:"user_id"`
	Path      string `json:"path"`
	FileName  string `json:"file_name"`
	SizeBytes int64  `json:"size_bytes"`
	Caption   string `json:"caption,omitempty"`
}

// New creates a WeChat Session.
func New(config Config) *Session {
	factory := config.Factory
	if factory == nil {
		factory = bot.NewFromEnv
	}
	mediaDir := config.MediaDir
	if mediaDir == "" && config.UsersPath != "" {
		mediaDir = filepath.Join(filepath.Dir(config.UsersPath), "media")
	}
	return &Session{
		usersPath:  config.UsersPath,
		mediaDir:   mediaDir,
		factory:    factory,
		botOptions: config.BotOptions,
	}
}

// NewClient creates a bot client for this session.
func (s *Session) NewClient() bot.Client {
	return s.factory(s.botOptions)
}

// RememberMessage records an incoming message in the User Book.
func (s *Session) RememberMessage(msg bot.IncomingMessage) error {
	return store.RememberUser(s.usersPath, msg)
}

// DownloadMedia downloads and persists media attached to an incoming message.
func (s *Session) DownloadMedia(ctx context.Context, client bot.Client, msg *bot.IncomingMessage) (*media.Artifact, error) {
	if client == nil {
		return nil, fmt.Errorf("bot client is required")
	}
	if msg == nil {
		return nil, fmt.Errorf("message is required")
	}
	download, err := client.Download(ctx, msg)
	if err != nil {
		return nil, err
	}
	return media.Save(s.mediaDir, *msg, download)
}

// WithClient reuses an already logged-in bot client for this send.
func WithClient(client bot.Client) SendOption {
	return func(config *sendConfig) {
		config.client = client
	}
}

// SendText sends a text reply to a previously observed user.
func (s *Session) SendText(ctx context.Context, userID, text string, options ...SendOption) (*SendResult, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	user, ok, err := store.GetUser(s.usersPath, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUserNotObserved
	}
	if user.LastContextToken == "" {
		return nil, ErrContextMissing
	}

	sendConfig := sendConfig{}
	for _, option := range options {
		if option != nil {
			option(&sendConfig)
		}
	}

	client := sendConfig.client
	if client == nil {
		client = s.NewClient()
		if _, err := client.Login(ctx, false); err != nil {
			return nil, err
		}
	}
	if err := client.SendWithContext(ctx, userID, text, user.LastContextToken); err != nil {
		return nil, err
	}
	return &SendResult{Sent: true, UserID: userID, Text: text}, nil
}

// SendAttachment uploads and sends a local file to a previously observed user.
func (s *Session) SendAttachment(ctx context.Context, userID string, attachment Attachment, options ...SendOption) (*AttachmentResult, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if attachment.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	user, ok, err := store.GetUser(s.usersPath, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUserNotObserved
	}
	if user.LastContextToken == "" {
		return nil, ErrContextMissing
	}

	outbound, absolutePath, size, err := readAttachment(attachment)
	if err != nil {
		return nil, err
	}

	sendConfig := sendConfig{}
	for _, option := range options {
		if option != nil {
			option(&sendConfig)
		}
	}
	client := sendConfig.client
	if client == nil {
		client = s.NewClient()
		if _, err := client.Login(ctx, false); err != nil {
			return nil, err
		}
	}
	if err := client.SendAttachmentWithContext(ctx, userID, outbound, user.LastContextToken); err != nil {
		return nil, err
	}
	return &AttachmentResult{
		Sent:      true,
		UserID:    userID,
		Path:      absolutePath,
		FileName:  outbound.FileName,
		SizeBytes: size,
		Caption:   outbound.Caption,
	}, nil
}

func readAttachment(attachment Attachment) (bot.OutboundAttachment, string, int64, error) {
	absolutePath, err := filepath.Abs(attachment.Path)
	if err != nil {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("resolve attachment path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("stat attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("attachment is not a regular file: %s", absolutePath)
	}
	if info.Size() == 0 {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("attachment is empty: %s", absolutePath)
	}
	if info.Size() > maxOutboundAttachmentBytes {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("attachment exceeds %d MiB limit: %s", maxOutboundAttachmentBytes>>20, absolutePath)
	}

	fileName := strings.TrimSpace(attachment.FileName)
	if fileName == "" {
		fileName = filepath.Base(absolutePath)
	}
	if fileName == "." || fileName == ".." || strings.ContainsAny(fileName, `/\`) || filepath.Base(fileName) != fileName {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("file_name must be a base name")
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("open attachment: %w", err)
	}

	data, readErr := io.ReadAll(io.LimitReader(file, maxOutboundAttachmentBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("read attachment: %w", readErr)
	}
	if closeErr != nil {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("close attachment: %w", closeErr)
	}
	if len(data) == 0 {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("attachment is empty: %s", absolutePath)
	}
	if int64(len(data)) > maxOutboundAttachmentBytes {
		return bot.OutboundAttachment{}, "", 0, fmt.Errorf("attachment exceeds %d MiB limit: %s", maxOutboundAttachmentBytes>>20, absolutePath)
	}
	return bot.OutboundAttachment{
		Data:     data,
		FileName: fileName,
		Caption:  attachment.Caption,
	}, absolutePath, int64(len(data)), nil
}

// ListUsers returns observed users.
func (s *Session) ListUsers() ([]store.UserRecord, error) {
	return store.ListUsers(s.usersPath)
}

// GetUser returns one observed user.
func (s *Session) GetUser(userID string) (*store.UserRecord, bool, error) {
	return store.GetUser(s.usersPath, userID)
}

// ForgetUser removes a user from the User Book.
func (s *Session) ForgetUser(userID string) (bool, error) {
	return store.ForgetUser(s.usersPath, userID)
}
