package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

func TestRememberUserCreatesAndUpdatesUserBook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")

	first := bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "hello",
		Type:         "text",
		Timestamp:    time.Unix(100, 0),
		ContextToken: "ctx-1",
	}
	if err := RememberUser(path, first); err != nil {
		t.Fatalf("RememberUser first: %v", err)
	}

	second := bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "second",
		Type:         "image",
		Timestamp:    time.Unix(200, 0),
		ContextToken: "ctx-2",
	}
	if err := RememberUser(path, second); err != nil {
		t.Fatalf("RememberUser second: %v", err)
	}

	book, err := ReadUserBook(path)
	if err != nil {
		t.Fatalf("ReadUserBook: %v", err)
	}
	user, ok := book.Users["user-1"]
	if !ok {
		t.Fatalf("user not found in book: %+v", book.Users)
	}
	if user.LastText != "second" || user.LastType != "image" || user.MessageCount != 2 || user.LastSeenAt != 200 {
		t.Fatalf("unexpected user record: %+v", user)
	}
	if user.LastContextToken != "ctx-2" {
		t.Fatalf("context token: got %q want %q", user.LastContextToken, "ctx-2")
	}
	if !user.HasContext {
		t.Fatalf("expected HasContext=true")
	}
}

func TestForgetUserRemovesUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if err := RememberUser(path, bot.IncomingMessage{UserID: "user-1", Text: "hello", Timestamp: time.Unix(100, 0)}); err != nil {
		t.Fatalf("RememberUser: %v", err)
	}

	removed, err := ForgetUser(path, "user-1")
	if err != nil {
		t.Fatalf("ForgetUser: %v", err)
	}
	if !removed {
		t.Fatal("expected removal")
	}

	book, err := ReadUserBook(path)
	if err != nil {
		t.Fatalf("ReadUserBook: %v", err)
	}
	if _, ok := book.Users["user-1"]; ok {
		t.Fatalf("user still present: %+v", book.Users)
	}
}
