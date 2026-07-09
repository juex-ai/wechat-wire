// Package bot defines the small interface used by the CLI and MCP server.
package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	wechatbot "github.com/corespeed-io/wechatbot/golang"
)

// Credentials is the stable credential shape surfaced by wechat-wire.
type Credentials struct {
	Token     string `json:"token"`
	BaseURL   string `json:"base_url"`
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	SavedAt   string `json:"saved_at,omitempty"`
}

// IncomingMessage is the stable message shape used by stores and notifications.
type IncomingMessage struct {
	UserID       string    `json:"user_id"`
	Text         string    `json:"text"`
	Type         string    `json:"type"`
	Timestamp    time.Time `json:"timestamp"`
	ContextToken string    `json:"-"`
}

// Options configures either the real SDK adapter or the fake localtest bot.
type Options struct {
	BaseURL      string
	CredPath     string
	LogLevel     string
	BotAgent     string
	OnQRURL      func(url string)
	OnScanned    func()
	OnExpired    func()
	OnError      func(err error)
	OnVerifyCode func(isRetry bool) (string, error)
}

// Client is the subset of the upstream SDK we need.
type Client interface {
	Login(ctx context.Context, force bool) (*Credentials, error)
	OnMessage(handler func(*IncomingMessage))
	Run(ctx context.Context) error
	Send(ctx context.Context, userID, text string) error
	SendTyping(ctx context.Context, userID string) error
	StopTyping(ctx context.Context, userID string) error
	Stop()
}

// Factory creates a Client for the supplied options.
type Factory func(Options) Client

// NewFromEnv returns the fake localtest bot when WECHAT_WIRE_FAKE=1,
// otherwise it wraps github.com/corespeed-io/wechatbot/golang.
func NewFromEnv(opts Options) Client {
	if os.Getenv("WECHAT_WIRE_FAKE") == "1" {
		return NewFake(opts)
	}
	return NewSDK(opts)
}

// NewSDK creates a real upstream SDK adapter.
func NewSDK(opts Options) Client {
	sdkOpts := wechatbot.Options{
		BaseURL:      opts.BaseURL,
		CredPath:     opts.CredPath,
		LogLevel:     opts.LogLevel,
		BotAgent:     opts.BotAgent,
		OnQRURL:      opts.OnQRURL,
		OnScanned:    opts.OnScanned,
		OnExpired:    opts.OnExpired,
		OnError:      opts.OnError,
		OnVerifyCode: opts.OnVerifyCode,
	}
	return &sdkClient{bot: wechatbot.New(sdkOpts)}
}

type sdkClient struct {
	bot *wechatbot.Bot
}

func (c *sdkClient) Login(ctx context.Context, force bool) (*Credentials, error) {
	creds, err := c.bot.Login(ctx, force)
	if err != nil {
		return nil, err
	}
	return &Credentials{
		Token:     creds.Token,
		BaseURL:   creds.BaseURL,
		AccountID: creds.AccountID,
		UserID:    creds.UserID,
		SavedAt:   creds.SavedAt,
	}, nil
}

func (c *sdkClient) OnMessage(handler func(*IncomingMessage)) {
	c.bot.OnMessage(func(msg *wechatbot.IncomingMessage) {
		handler(&IncomingMessage{
			UserID:       msg.UserID,
			Text:         msg.Text,
			Type:         string(msg.Type),
			Timestamp:    msg.Timestamp,
			ContextToken: msg.ContextToken,
		})
	})
}

func (c *sdkClient) Run(ctx context.Context) error {
	return c.bot.Run(ctx)
}

func (c *sdkClient) Send(ctx context.Context, userID, text string) error {
	return c.bot.Send(ctx, userID, text)
}

func (c *sdkClient) SendTyping(ctx context.Context, userID string) error {
	return c.bot.SendTyping(ctx, userID)
}

func (c *sdkClient) StopTyping(ctx context.Context, userID string) error {
	return c.bot.StopTyping(ctx, userID)
}

func (c *sdkClient) Stop() {
	c.bot.Stop()
}

// NewFake creates the deterministic localtest bot.
func NewFake(opts Options) Client {
	return &fakeClient{opts: opts}
}

type fakeClient struct {
	opts     Options
	mu       sync.Mutex
	handlers []func(*IncomingMessage)
}

func (c *fakeClient) Login(ctx context.Context, force bool) (*Credentials, error) {
	_ = ctx
	if !force {
		if creds, err := readFakeCredentials(c.opts.CredPath); err == nil {
			return creds, nil
		}
	}
	creds := &Credentials{
		Token:     "fake-token",
		BaseURL:   "fake://wechat-wire",
		AccountID: "fake-account",
		UserID:    "fake-bot",
		SavedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if c.opts.CredPath != "" {
		if err := writeFakeCredentials(c.opts.CredPath, creds); err != nil {
			return nil, err
		}
	}
	return creds, nil
}

func (c *fakeClient) OnMessage(handler func(*IncomingMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

func (c *fakeClient) Run(ctx context.Context) error {
	messages, err := fakeMessagesFromEnv()
	if err != nil {
		return err
	}
	for _, msg := range messages {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		c.dispatch(msg)
	}
	if os.Getenv("WECHAT_WIRE_FAKE_EXIT_AFTER_MESSAGES") == "1" {
		return nil
	}
	<-ctx.Done()
	return nil
}

func (c *fakeClient) Send(ctx context.Context, userID, text string) error {
	_ = ctx
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}
	if text == "" {
		return fmt.Errorf("text is required")
	}
	return nil
}

func (c *fakeClient) SendTyping(ctx context.Context, userID string) error {
	_ = ctx
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}
	return nil
}

func (c *fakeClient) StopTyping(ctx context.Context, userID string) error {
	_ = ctx
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}
	return nil
}

func (c *fakeClient) Stop() {}

func (c *fakeClient) dispatch(msg *IncomingMessage) {
	c.mu.Lock()
	handlers := append([]func(*IncomingMessage){}, c.handlers...)
	c.mu.Unlock()
	for _, handler := range handlers {
		handler(msg)
	}
}

type fakeMessageJSON struct {
	UserID       string `json:"user_id"`
	Text         string `json:"text"`
	Type         string `json:"type"`
	Timestamp    int64  `json:"timestamp"`
	ContextToken string `json:"context_token"`
}

func fakeMessagesFromEnv() ([]*IncomingMessage, error) {
	raw := os.Getenv("WECHAT_WIRE_FAKE_MESSAGES_JSON")
	if raw == "" {
		raw = `[{"user_id":"fake-user","text":"hello from fake wechat","type":"text","context_token":"fake-context"}]`
	}
	var in []fakeMessageJSON
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, fmt.Errorf("parsing WECHAT_WIRE_FAKE_MESSAGES_JSON: %w", err)
	}
	out := make([]*IncomingMessage, 0, len(in))
	for i, msg := range in {
		if msg.UserID == "" {
			return nil, fmt.Errorf("fake message %d missing user_id", i)
		}
		msgType := msg.Type
		if msgType == "" {
			msgType = "text"
		}
		ts := time.Now()
		if msg.Timestamp != 0 {
			ts = time.Unix(msg.Timestamp, 0)
		}
		out = append(out, &IncomingMessage{
			UserID:       msg.UserID,
			Text:         msg.Text,
			Type:         msgType,
			Timestamp:    ts,
			ContextToken: msg.ContextToken,
		})
	}
	return out, nil
}

func readFakeCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var disk struct {
		Token     string `json:"token"`
		BaseURL   string `json:"baseUrl"`
		AccountID string `json:"accountId"`
		UserID    string `json:"userId"`
		SavedAt   string `json:"savedAt"`
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, err
	}
	if disk.Token == "" || disk.AccountID == "" || disk.UserID == "" {
		return nil, fmt.Errorf("fake credentials missing required fields")
	}
	return &Credentials{
		Token:     disk.Token,
		BaseURL:   disk.BaseURL,
		AccountID: disk.AccountID,
		UserID:    disk.UserID,
		SavedAt:   disk.SavedAt,
	}, nil
}

func writeFakeCredentials(path string, creds *Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating credential dir: %w", err)
	}
	data, err := json.MarshalIndent(struct {
		Token     string `json:"token"`
		BaseURL   string `json:"baseUrl"`
		AccountID string `json:"accountId"`
		UserID    string `json:"userId"`
		SavedAt   string `json:"savedAt,omitempty"`
	}{
		Token:     creds.Token,
		BaseURL:   creds.BaseURL,
		AccountID: creds.AccountID,
		UserID:    creds.UserID,
		SavedAt:   creds.SavedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
