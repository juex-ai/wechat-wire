package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

func TestSessionRecordsIncomingMessageAndSendsWithStoredContext(t *testing.T) {
	dir := t.TempDir()
	fake := &recordingClient{}
	s := New(Config{
		UsersPath: filepath.Join(dir, "users.json"),
		Factory: func(opts bot.Options) bot.Client {
			fake.options = opts
			return fake
		},
		BotOptions: bot.Options{CredPath: filepath.Join(dir, "credentials.json")},
	})

	msg := bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "hello",
		Type:         "text",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-1",
	}
	if err := s.RememberMessage(msg); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}

	result, err := s.SendText(context.Background(), "user-1", "reply")
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if !result.Sent || result.UserID != "user-1" || result.Text != "reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if fake.loginCalls != 1 {
		t.Fatalf("login calls: got %d want 1", fake.loginCalls)
	}
	if fake.sentUserID != "user-1" || fake.sentText != "reply" || fake.sentContextToken != "ctx-1" {
		t.Fatalf("send args: user=%q text=%q context=%q", fake.sentUserID, fake.sentText, fake.sentContextToken)
	}
}

func TestSessionSendTextCanReuseExistingClient(t *testing.T) {
	dir := t.TempDir()
	existingClient := &recordingClient{}
	s := New(Config{
		UsersPath: filepath.Join(dir, "users.json"),
		Factory: func(opts bot.Options) bot.Client {
			t.Fatal("factory should not be called when a client is supplied")
			return nil
		},
	})

	msg := bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "hello",
		Type:         "text",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-1",
	}
	if err := s.RememberMessage(msg); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}

	result, err := s.SendText(context.Background(), "user-1", "reply", WithClient(existingClient))
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if !result.Sent || result.UserID != "user-1" || result.Text != "reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if existingClient.loginCalls != 0 {
		t.Fatalf("login calls: got %d want 0", existingClient.loginCalls)
	}
	if existingClient.sentUserID != "user-1" || existingClient.sentText != "reply" || existingClient.sentContextToken != "ctx-1" {
		t.Fatalf("send args: user=%q text=%q context=%q", existingClient.sentUserID, existingClient.sentText, existingClient.sentContextToken)
	}
}

func TestSessionSendTextRequiresObservedContext(t *testing.T) {
	s := New(Config{
		UsersPath: filepath.Join(t.TempDir(), "users.json"),
		Factory: func(opts bot.Options) bot.Client {
			t.Fatal("factory should not be called when user has no context")
			return nil
		},
	})

	_, err := s.SendText(context.Background(), "missing-user", "reply")
	if err == nil {
		t.Fatal("expected missing user error")
	}
	if !errors.Is(err, ErrUserNotObserved) {
		t.Fatalf("error: got %v want ErrUserNotObserved", err)
	}
}

type recordingClient struct {
	options          bot.Options
	loginCalls       int
	sentUserID       string
	sentText         string
	sentContextToken string
}

func (c *recordingClient) Login(ctx context.Context, force bool) (*bot.Credentials, error) {
	c.loginCalls++
	return &bot.Credentials{Token: "tok", AccountID: "account", UserID: "bot"}, nil
}

func (c *recordingClient) OnMessage(handler func(*bot.IncomingMessage)) {}

func (c *recordingClient) Run(ctx context.Context) error { return nil }

func (c *recordingClient) Send(ctx context.Context, userID, text string) error {
	return c.SendWithContext(ctx, userID, text, "")
}

func (c *recordingClient) SendWithContext(ctx context.Context, userID, text, contextToken string) error {
	c.sentUserID = userID
	c.sentText = text
	c.sentContextToken = contextToken
	return nil
}

func (c *recordingClient) SendTyping(ctx context.Context, userID string) error { return nil }

func (c *recordingClient) StopTyping(ctx context.Context, userID string) error { return nil }

func (c *recordingClient) Stop() {}
