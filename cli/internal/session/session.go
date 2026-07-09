// Package session concentrates the WeChat Session interface used by CLI and MCP.
package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

var (
	// ErrUserNotObserved means the User Book has no record for the requested user.
	ErrUserNotObserved = errors.New("user not observed")
	// ErrContextMissing means the User Book has a user record without a reply context.
	ErrContextMissing = errors.New("context token missing")
)

// Config is the interface to the WeChat Session Module.
type Config struct {
	UsersPath  string
	Factory    bot.Factory
	BotOptions bot.Options
}

// Session is the deep Module that owns User Book and bot adapter choreography.
type Session struct {
	usersPath  string
	factory    bot.Factory
	botOptions bot.Options
}

// SendResult is returned after a text send completes.
type SendResult struct {
	Sent   bool   `json:"sent"`
	UserID string `json:"user_id"`
	Text   string `json:"text"`
}

// New creates a WeChat Session.
func New(config Config) *Session {
	factory := config.Factory
	if factory == nil {
		factory = bot.NewFromEnv
	}
	return &Session{
		usersPath:  config.UsersPath,
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

// SendText sends a text reply to a previously observed user.
func (s *Session) SendText(ctx context.Context, userID, text string) (*SendResult, error) {
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

	client := s.NewClient()
	if _, err := client.Login(ctx, false); err != nil {
		return nil, err
	}
	if err := client.SendWithContext(ctx, userID, text, user.LastContextToken); err != nil {
		return nil, err
	}
	return &SendResult{Sent: true, UserID: userID, Text: text}, nil
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
