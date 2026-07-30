package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestSessionSendTextForContextSerializesInboundRefresh(t *testing.T) {
	dir := t.TempDir()
	client := newBlockingSendClient()
	s := New(Config{UsersPath: filepath.Join(dir, "users.json")})
	if err := s.RememberMessage(bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "old message",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-old",
	}); err != nil {
		t.Fatalf("RememberMessage old: %v", err)
	}

	sendDone := make(chan error, 1)
	go func() {
		_, err := s.SendTextForContext(
			context.Background(),
			"user-1",
			"expiry reminder",
			ContextReference{Token: "ctx-old", ObservedAt: 100},
			WithClient(client),
		)
		sendDone <- err
	}()
	select {
	case <-client.sendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("context send did not start")
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- s.RememberMessage(bot.IncomingMessage{
			UserID:       "user-1",
			Text:         "fresh reply",
			Timestamp:    time.Unix(200, 0),
			ContextToken: "ctx-new",
		})
	}()
	select {
	case err := <-refreshDone:
		t.Fatalf("context refresh completed during guarded send: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(client.releaseSend)
	if err := <-sendDone; err != nil {
		t.Fatalf("SendTextForContext: %v", err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("RememberMessage fresh: %v", err)
	}
	if client.sentContextToken != "ctx-old" {
		t.Fatalf("sent context: got %q want ctx-old", client.sentContextToken)
	}

	user, ok, err := s.GetUser("user-1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !ok || user.LastContextToken != "ctx-new" || user.ContextObservedAt != 200 {
		t.Fatalf("fresh context was not persisted: %+v", user)
	}
}

func TestSessionSendTextForContextRejectsRefreshedCycle(t *testing.T) {
	dir := t.TempDir()
	client := &recordingClient{}
	s := New(Config{UsersPath: filepath.Join(dir, "users.json")})
	if err := s.RememberMessage(bot.IncomingMessage{
		UserID:       "user-1",
		Timestamp:    time.Unix(200, 0),
		ContextToken: "ctx-new",
	}); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}

	_, err := s.SendTextForContext(
		context.Background(),
		"user-1",
		"stale reminder",
		ContextReference{Token: "ctx-old", ObservedAt: 100},
		WithClient(client),
	)
	if !errors.Is(err, ErrContextChanged) {
		t.Fatalf("error: got %v want ErrContextChanged", err)
	}
	if client.sentContextToken != "" {
		t.Fatalf("stale reminder was sent with context %q", client.sentContextToken)
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

func TestSessionSendAttachmentUsesStoredContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	data := []byte("fake pdf content")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	client := &recordingClient{}
	s := New(Config{UsersPath: filepath.Join(dir, "users.json")})
	if err := s.RememberMessage(bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "hello",
		Type:         "text",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-1",
	}); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}

	result, err := s.SendAttachment(context.Background(), "user-1", Attachment{
		Path:    path,
		Caption: "monthly report",
	}, WithClient(client))
	if err != nil {
		t.Fatalf("SendAttachment: %v", err)
	}
	if !result.Sent || result.UserID != "user-1" || result.FileName != "report.pdf" || result.SizeBytes != int64(len(data)) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if client.sentUserID != "user-1" || client.sentContextToken != "ctx-1" {
		t.Fatalf("send target: user=%q context=%q", client.sentUserID, client.sentContextToken)
	}
	if string(client.sentAttachment.Data) != string(data) || client.sentAttachment.FileName != "report.pdf" || client.sentAttachment.Caption != "monthly report" {
		t.Fatalf("attachment: %+v", client.sentAttachment)
	}
}

func TestSessionSendAttachmentRejectsInvalidLocalFile(t *testing.T) {
	dir := t.TempDir()
	s := New(Config{UsersPath: filepath.Join(dir, "users.json")})
	if err := s.RememberMessage(bot.IncomingMessage{
		UserID:       "user-1",
		Type:         "text",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-1",
	}); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}

	emptyPath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("write empty attachment: %v", err)
	}
	oversizedPath := filepath.Join(dir, "oversized.bin")
	oversized, err := os.Create(oversizedPath)
	if err != nil {
		t.Fatalf("create oversized attachment: %v", err)
	}
	if err := oversized.Truncate(maxOutboundAttachmentBytes + 1); err != nil {
		t.Fatalf("truncate oversized attachment: %v", err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatalf("close oversized attachment: %v", err)
	}
	regularPath := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regularPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("write regular attachment: %v", err)
	}

	tests := []struct {
		name       string
		attachment Attachment
		want       string
	}{
		{name: "directory", attachment: Attachment{Path: dir}, want: "not a regular file"},
		{name: "empty", attachment: Attachment{Path: emptyPath}, want: "attachment is empty"},
		{name: "oversized", attachment: Attachment{Path: oversizedPath}, want: "exceeds 100 MiB limit"},
		{name: "nested file name", attachment: Attachment{Path: regularPath, FileName: "nested/report.txt"}, want: "file_name must be a base name"},
		{name: "parent file name", attachment: Attachment{Path: regularPath, FileName: ".."}, want: "file_name must be a base name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.SendAttachment(context.Background(), "user-1", test.attachment, WithClient(&recordingClient{}))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error: got %v want containing %q", err, test.want)
			}
		})
	}
}

type recordingClient struct {
	options          bot.Options
	loginCalls       int
	sentUserID       string
	sentText         string
	sentContextToken string
	sentAttachment   bot.OutboundAttachment
}

func (c *recordingClient) Login(ctx context.Context, force bool) (*bot.Credentials, error) {
	c.loginCalls++
	return &bot.Credentials{Token: "tok", AccountID: "account", UserID: "bot"}, nil
}

func (c *recordingClient) OnMessage(handler func(*bot.IncomingMessage)) {}

func (c *recordingClient) Run(ctx context.Context) error { return nil }

func (c *recordingClient) Download(ctx context.Context, msg *bot.IncomingMessage) (*bot.DownloadedMedia, error) {
	return nil, nil
}

func (c *recordingClient) Send(ctx context.Context, userID, text string) error {
	return c.SendWithContext(ctx, userID, text, "")
}

func (c *recordingClient) SendWithContext(ctx context.Context, userID, text, contextToken string) error {
	c.sentUserID = userID
	c.sentText = text
	c.sentContextToken = contextToken
	return nil
}

func (c *recordingClient) SendAttachmentWithContext(ctx context.Context, userID string, attachment bot.OutboundAttachment, contextToken string) error {
	c.sentUserID = userID
	c.sentContextToken = contextToken
	c.sentAttachment = attachment
	return nil
}

func (c *recordingClient) SendTyping(ctx context.Context, userID string) error { return nil }

func (c *recordingClient) StopTyping(ctx context.Context, userID string) error { return nil }

func (c *recordingClient) Stop() {}

type blockingSendClient struct {
	recordingClient
	sendStarted chan struct{}
	releaseSend chan struct{}
}

func newBlockingSendClient() *blockingSendClient {
	return &blockingSendClient{
		sendStarted: make(chan struct{}),
		releaseSend: make(chan struct{}),
	}
}

func (c *blockingSendClient) SendWithContext(ctx context.Context, userID, text, contextToken string) error {
	c.sentUserID = userID
	c.sentText = text
	c.sentContextToken = contextToken
	close(c.sendStarted)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.releaseSend:
		return nil
	}
}
