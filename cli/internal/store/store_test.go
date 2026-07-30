package store

import (
	"path/filepath"
	"sync"
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

	third := bot.IncomingMessage{
		UserID:    "user-1",
		Text:      "without context",
		Type:      "text",
		Timestamp: time.Unix(300, 0),
	}
	if err := RememberUser(path, third); err != nil {
		t.Fatalf("RememberUser third: %v", err)
	}

	book, err := ReadUserBook(path)
	if err != nil {
		t.Fatalf("ReadUserBook: %v", err)
	}
	user, ok := book.Users["user-1"]
	if !ok {
		t.Fatalf("user not found in book: %+v", book.Users)
	}
	if user.LastText != "without context" || user.LastType != "text" || user.MessageCount != 3 || user.LastSeenAt != 300 {
		t.Fatalf("unexpected user record: %+v", user)
	}
	if user.LastContextToken != "ctx-2" {
		t.Fatalf("context token: got %q want %q", user.LastContextToken, "ctx-2")
	}
	if user.ContextObservedAt != 200 {
		t.Fatalf("context observed at: got %d want 200", user.ContextObservedAt)
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

func TestConcurrentRememberUserPreservesEveryMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	const messages = 24
	start := make(chan struct{})
	errs := make(chan error, messages)
	var wait sync.WaitGroup

	for i := range messages {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs <- RememberUser(path, bot.IncomingMessage{
				UserID:       "user-1",
				Text:         "hello",
				Timestamp:    time.Unix(int64(i+1), 0),
				ContextToken: "ctx",
			})
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RememberUser: %v", err)
		}
	}

	book, err := ReadUserBook(path)
	if err != nil {
		t.Fatalf("ReadUserBook: %v", err)
	}
	if got := book.Users["user-1"].MessageCount; got != messages {
		t.Fatalf("message count: got %d want %d", got, messages)
	}
}
