package contextguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

func TestRunnerDoesNotResendSentReminderAfterRestart(t *testing.T) {
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
			Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
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

func TestRunnerSendsScheduledReminderAfterRestartBeforeDueTime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	windowFrom := "00:00"
	windowTo := "23:59"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:            &enabled,
		AssumedTTLMinutes:  &ttl,
		LeadTimeMinutes:    &lead,
		Timezone:           &timezone,
		ReminderWindowFrom: &windowFrom,
		ReminderWindowTo:   &windowTo,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	if err := store.RememberUser(usersPath, bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "initial message",
		Timestamp:    observedAt,
		ContextToken: "ctx-1",
	}); err != nil {
		t.Fatalf("RememberUser: %v", err)
	}

	now := observedAt
	sends := 0
	newRunner := func() *Runner {
		return NewRunner(RunnerConfig{
			ConfigPath: configPath,
			StatePath:  statePath,
			UsersPath:  usersPath,
			Now:        func() time.Time { return now },
			Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
				sends++
				return nil
			},
		})
	}

	if err := newRunner().Check(context.Background()); err != nil {
		t.Fatalf("Check before restart: %v", err)
	}
	state, err := ReadState(statePath)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	userState := state.Users["user-1"]
	wantReminderAt := time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	if userState.Status != StatusScheduled || userState.ReminderAt != wantReminderAt.Unix() {
		t.Fatalf("unexpected persisted schedule: %+v", userState)
	}

	restarted := newRunner()
	now = wantReminderAt.Add(-time.Minute)
	if err := restarted.Check(context.Background()); err != nil {
		t.Fatalf("Check after restart before due time: %v", err)
	}
	if sends != 0 {
		t.Fatalf("send count before due time: got %d want 0", sends)
	}

	now = wantReminderAt
	if err := restarted.Check(context.Background()); err != nil {
		t.Fatalf("Check after restart at due time: %v", err)
	}
	if sends != 1 {
		t.Fatalf("send count at due time: got %d want 1", sends)
	}
	for _, path := range []string{configPath, statePath, usersPath} {
		if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
			t.Fatalf("sidecar lock should not exist for %s: %v", path, err)
		}
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
			Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
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

func TestRunnerWaitsForFreshContextObservationForLegacyUser(t *testing.T) {
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
	if err := store.WriteUserBook(usersPath, &store.UserBook{Users: map[string]store.UserRecord{
		"legacy-user": {
			UserID:           "legacy-user",
			LastSeenAt:       time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC).Unix(),
			HasContext:       true,
			LastContextToken: "legacy-context",
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
			return time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
		},
		Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
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
	if _, exists := state.Users["legacy-user"]; exists {
		t.Fatalf("legacy context should remain unscheduled: %+v", state.Users["legacy-user"])
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
		Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
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
		Send: func(ctx context.Context, userID, text string, _ ContextReference) error {
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

func TestRunnerReschedulesUnsentReminderAfterFreshInboundContext(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context-guard.json")
	statePath := filepath.Join(dir, "context-guard-state.json")
	usersPath := filepath.Join(dir, "users.json")
	enabled := true
	ttl := 120
	lead := 60
	timezone := "UTC"
	windowFrom := "00:00"
	windowTo := "23:59"
	if _, err := UpdateConfig(configPath, ConfigPatch{
		Enabled:            &enabled,
		AssumedTTLMinutes:  &ttl,
		LeadTimeMinutes:    &lead,
		Timezone:           &timezone,
		ReminderWindowFrom: &windowFrom,
		ReminderWindowTo:   &windowTo,
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	if err := store.RememberUser(usersPath, bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "initial message",
		Timestamp:    now,
		ContextToken: "ctx-1",
	}); err != nil {
		t.Fatalf("RememberUser initial: %v", err)
	}

	sends := 0
	var sentContext ContextReference
	runner := NewRunner(RunnerConfig{
		ConfigPath: configPath,
		StatePath:  statePath,
		UsersPath:  usersPath,
		Now:        func() time.Time { return now },
		Send: func(ctx context.Context, userID, text string, contextRef ContextReference) error {
			sends++
			sentContext = contextRef
			return nil
		},
	})
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check initial schedule: %v", err)
	}

	now = time.Date(2026, 7, 30, 6, 30, 0, 0, time.UTC)
	if err := store.RememberUser(usersPath, bot.IncomingMessage{
		UserID:       "user-1",
		Text:         "fresh message",
		Timestamp:    now,
		ContextToken: "ctx-2",
	}); err != nil {
		t.Fatalf("RememberUser fresh: %v", err)
	}
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check rescheduled cycle: %v", err)
	}

	state, err := ReadState(statePath)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	userState := state.Users["user-1"]
	wantReminderAt := time.Date(2026, 7, 30, 7, 30, 0, 0, time.UTC)
	if userState.ContextObservedAt != now.Unix() ||
		userState.ReminderAt != wantReminderAt.Unix() ||
		userState.Status != StatusScheduled {
		t.Fatalf("unexpected rescheduled state: %+v", userState)
	}

	now = time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check original due time: %v", err)
	}
	if sends != 0 {
		t.Fatalf("send count at original due time: got %d want 0", sends)
	}

	now = wantReminderAt
	if err := runner.Check(context.Background()); err != nil {
		t.Fatalf("Check rescheduled due time: %v", err)
	}
	if sends != 1 {
		t.Fatalf("send count at rescheduled due time: got %d want 1", sends)
	}
	if sentContext.Token != "ctx-2" || sentContext.ObservedAt != time.Date(2026, 7, 30, 6, 30, 0, 0, time.UTC).Unix() {
		t.Fatalf("unexpected reminder context: %+v", sentContext)
	}
}
