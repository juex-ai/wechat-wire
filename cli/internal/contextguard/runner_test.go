package contextguard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

func TestRunnerSendsOnceAndPersistsAttemptAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	windowFrom := "08:00"
	windowTo := "22:00"
	template := "Reply within about {{remaining_minutes}} minutes."
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:            &enabled,
		AssumedTTLMinutes:  &ttl,
		LeadTimeMinutes:    &lead,
		Timezone:           &timezone,
		ReminderWindowFrom: &windowFrom,
		ReminderWindowTo:   &windowTo,
		MessageTemplate:    &template,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
		"user-1": {
			UserID:            "user-1",
			LastSeenAt:        time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC).Unix(),
			ContextObservedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC).Unix(),
			HasContext:        true,
			LastContextToken:  "ctx-1",
		},
	}}); err != nil {
		t.Fatalf("WriteUserBook: %v", err)
	}

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	var sends []string
	newRunner := func() *Runner {
		return NewRunner(RunnerConfig{
			ConfigPath: configPath,
			StatePath:  statePath,
			UsersPath:  usersPath,
			Now:        func() time.Time { return now },
			Send: func(ctx context.Context, userID, text string) error {
				sends = append(sends, userID+":"+text)
				return nil
			},
		})
	}

	runner := newRunner()
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check first: %v", err)
	}
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check second: %v", err)
	}
	if err := newRunner().Check(context.Background()); err != nil {
		t.Fatalf("Check after restart: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("send count: got %d want 1", len(sends))
	}
	if !strings.Contains(sends[0], "user-1:") || !strings.Contains(sends[0], "60 minutes") {
		t.Fatalf("unexpected reminder: %q", sends[0])
	}

	state, err := ReadState(statePath)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Users["user-1"].Status != StatusSent {
		t.Fatalf("unexpected state: %+v", state.Users["user-1"])
	}
}

func TestRunnerDoesNotRetryFailedReminderAfterRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:           &enabled,
		AssumedTTLMinutes: &ttl,
		LeadTimeMinutes:   &lead,
		Timezone:          &timezone,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
		"user-1": {
			UserID:            "user-1",
			LastSeenAt:        observedAt.Unix(),
			ContextObservedAt: observedAt.Unix(),
			HasContext:        true,
			LastContextToken:  "ctx-1",
		},
	}}); err != nil {
		t.Fatalf("WriteUserBook: %v", err)
	}

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	attempts := 0
	newRunner := func() *Runner {
		return NewRunner(RunnerConfig{
			ConfigPath: configPath,
			StatePath:  statePath,
			UsersPath:  usersPath,
			Now:        func() time.Time { return now },
			Send: func(ctx context.Context, userID, text string) error {
				attempts++
				return errors.New("ambiguous network failure")
			},
		})
	}

	if err := newRunner().Check(context.Background()); err != nil {
		t.Fatalf("Check first: %v", err)
	}
	if err := newRunner().Check(context.Background()); err != nil {
		t.Fatalf("Check after restart: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("send attempts: got %d want 1", attempts)
	}
	state, err := ReadState(statePath)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	userState := state.Users["user-1"]
	if userState.Status != StatusFailed || !strings.Contains(userState.LastError, "ambiguous network failure") {
		t.Fatalf("unexpected failed state: %+v", userState)
	}
}

func TestRunnerSkipsReminderAfterQuietHourDeadline(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	timezone := "Asia/Shanghai"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:  &enabled,
		Timezone: &timezone,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 3, 0, 0, 0, location)
	if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
		"user-quiet": {
			UserID:            "user-quiet",
			LastSeenAt:        observedAt.Unix(),
			ContextObservedAt: observedAt.Unix(),
			HasContext:        true,
			LastContextToken:  "ctx-quiet",
		},
	}}); err != nil {
		t.Fatalf("WriteUserBook: %v", err)
	}

	sends := 0
	runner := NewRunner(RunnerConfig{
		ConfigPath: configPath,
		StatePath:  statePath,
		UsersPath:  usersPath,
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 22, 5, 0, 0, location)
		},
		Send: func(ctx context.Context, userID, text string) error {
			sends++
			return nil
		},
	})
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if sends != 0 {
		t.Fatalf("send count: got %d want 0", sends)
	}
	state, err := ReadState(statePath)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	userState := state.Users["user-quiet"]
	if userState.Status != StatusSkipped || !userState.MovedBeforeQuietHours {
		t.Fatalf("unexpected quiet-hour state: %+v", userState)
	}
}

func TestConcurrentRunnersShareOnePersistentClaim(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:           &enabled,
		AssumedTTLMinutes: &ttl,
		LeadTimeMinutes:   &lead,
		Timezone:          &timezone,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
		"user-1": {
			UserID:            "user-1",
			LastSeenAt:        observedAt.Unix(),
			ContextObservedAt: observedAt.Unix(),
			HasContext:        true,
			LastContextToken:  "ctx-1",
		},
	}}); err != nil {
		t.Fatalf("WriteUserBook: %v", err)
	}

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	var sends atomic.Int32
	newRunner := func() *Runner {
		return NewRunner(RunnerConfig{
			ConfigPath: configPath,
			StatePath:  statePath,
			UsersPath:  usersPath,
			Now:        func() time.Time { return now },
			Send: func(ctx context.Context, userID, text string) error {
				sends.Add(1)
				return nil
			},
		})
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func(runner *Runner) {
			defer wg.Done()
			<-start
			errs <- runner.Check(context.Background())
		}(newRunner())
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
	}
	if got := sends.Load(); got != 1 {
		t.Fatalf("send count: got %d want 1", got)
	}
}

func TestRunnerRearmsAfterFreshInboundContext(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:           &enabled,
		AssumedTTLMinutes: &ttl,
		LeadTimeMinutes:   &lead,
		Timezone:          &timezone,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	writeContextUser := func(token string, observedAt time.Time) {
		t.Helper()
		if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
			"user-1": {
				UserID:            "user-1",
				LastSeenAt:        observedAt.Unix(),
				ContextObservedAt: observedAt.Unix(),
				HasContext:        true,
				LastContextToken:  token,
			},
		}}); err != nil {
			t.Fatalf("WriteUserBook: %v", err)
		}
	}
	writeContextUser("ctx-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC))

	sends := 0
	runner := NewRunner(RunnerConfig{
		ConfigPath: configPath,
		StatePath:  statePath,
		UsersPath:  usersPath,
		Now:        func() time.Time { return now },
		Send: func(ctx context.Context, userID, text string) error {
			sends++
			return nil
		},
	})
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check first cycle: %v", err)
	}

	writeContextUser("ctx-2", time.Date(2026, 7, 30, 11, 30, 0, 0, time.UTC))
	now = time.Date(2026, 7, 30, 12, 30, 0, 0, time.UTC)
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check fresh cycle: %v", err)
	}
	if sends != 2 {
		t.Fatalf("send count: got %d want 2", sends)
	}
}
