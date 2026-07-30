package contextguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

const defaultPollInterval = 30 * time.Second

// RunnerConfig connects the context guard state machine to a bot sender.
type RunnerConfig struct {
	ConfigPath   string
	StatePath    string
	UsersPath    string
	PollInterval time.Duration
	Now          func() time.Time
	Send         func(context.Context, string, string, ContextReference) error
	OnEvent      func(Event)
}

// ContextReference identifies the exact inbound context claimed for a reminder.
// It is kept in memory only; durable state stores a hash.
type ContextReference struct {
	Token      string
	ObservedAt int64
}

// Event reports a terminal reminder outcome without exposing context tokens.
type Event struct {
	Type               string
	UserID             string
	EstimatedExpiresAt time.Time
	ReminderAt         time.Time
	Error              error
}

// Runner evaluates persisted user contexts and sends due reminders at most once.
type Runner struct {
	config RunnerConfig
	wake   chan struct{}
	mu     sync.Mutex
}

type reminderAction struct {
	UserID             string
	CycleID            string
	Text               string
	Context            ContextReference
	EstimatedExpiresAt time.Time
	ReminderAt         time.Time
}

// NewRunner creates a context guard runner.
func NewRunner(config RunnerConfig) *Runner {
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Runner{config: config, wake: make(chan struct{}, 1)}
}

// Run checks immediately and then whenever its timer or wake signal fires.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := r.Check(ctx); err != nil && ctx.Err() == nil {
			r.emit(Event{Type: "error", Error: err})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

// Wake asks a running guard to reload its configuration promptly.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Check performs one persistent scheduling and send pass.
func (r *Runner) Check(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	config, err := ReadConfig(r.config.ConfigPath)
	if err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	if r.config.Send == nil {
		return fmt.Errorf("context guard sender is required")
	}
	users, err := store.ListUsers(r.config.UsersPath)
	if err != nil {
		return err
	}

	now := r.config.Now()
	actions, err := r.claimDueReminders(users, config, now)
	if err != nil {
		return err
	}
	for _, action := range actions {
		sendErr := r.sendIfCurrent(ctx, action)
		if err := r.completeAttempt(action, sendErr, r.config.Now()); err != nil {
			return err
		}
		eventType := StatusSent
		if sendErr != nil {
			eventType = StatusFailed
		}
		r.emit(Event{
			Type:               eventType,
			UserID:             action.UserID,
			EstimatedExpiresAt: action.EstimatedExpiresAt,
			ReminderAt:         action.ReminderAt,
			Error:              sendErr,
		})
	}
	return nil
}

func (r *Runner) claimDueReminders(users []store.UserRecord, config Config, now time.Time) ([]reminderAction, error) {
	unlock, err := acquireFileLock(r.config.StatePath + ".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := ReadState(r.config.StatePath)
	if err != nil {
		return nil, err
	}
	knownUsers := make(map[string]bool, len(users))
	sort.Slice(users, func(i, j int) bool { return users[i].UserID < users[j].UserID })
	actions := make([]reminderAction, 0)
	changed := false

	for _, user := range users {
		knownUsers[user.UserID] = true
		if user.LastContextToken == "" {
			continue
		}
		observedUnix := user.ContextObservedAt
		if observedUnix == 0 {
			continue
		}
		observedAt := time.Unix(observedUnix, 0)
		schedule, err := PlanSchedule(observedAt, config)
		if err != nil {
			return nil, err
		}
		cycleID := contextCycleID(user.UserID, user.LastContextToken, observedUnix)
		userState, exists := state.Users[user.UserID]
		if !exists || userState.CycleID != cycleID {
			userState = UserState{
				CycleID:               cycleID,
				ContextObservedAt:     observedUnix,
				EstimatedExpiresAt:    schedule.EstimatedExpiresAt.Unix(),
				ReminderAt:            schedule.ReminderAt.Unix(),
				MovedBeforeQuietHours: schedule.MovedBeforeQuietHours,
				Status:                StatusScheduled,
			}
			state.Users[user.UserID] = userState
			changed = true
		}
		if userState.AttemptedAt != 0 || userState.Status == StatusSkipped {
			continue
		}
		if userState.EstimatedExpiresAt != schedule.EstimatedExpiresAt.Unix() ||
			userState.ReminderAt != schedule.ReminderAt.Unix() ||
			userState.MovedBeforeQuietHours != schedule.MovedBeforeQuietHours {
			userState.EstimatedExpiresAt = schedule.EstimatedExpiresAt.Unix()
			userState.ReminderAt = schedule.ReminderAt.Unix()
			userState.MovedBeforeQuietHours = schedule.MovedBeforeQuietHours
			state.Users[user.UserID] = userState
			changed = true
		}
		if now.Before(schedule.ReminderAt) {
			continue
		}
		if !now.Before(schedule.EstimatedExpiresAt) || now.After(reminderDeadline(schedule.ReminderAt, config)) {
			userState.Status = StatusSkipped
			userState.CompletedAt = now.Unix()
			state.Users[user.UserID] = userState
			changed = true
			continue
		}

		userState.Status = StatusAttempting
		userState.AttemptedAt = now.Unix()
		state.Users[user.UserID] = userState
		changed = true
		actions = append(actions, reminderAction{
			UserID:             user.UserID,
			CycleID:            cycleID,
			Text:               renderMessage(config.MessageTemplate, user.UserID, schedule.EstimatedExpiresAt, now),
			Context:            ContextReference{Token: user.LastContextToken, ObservedAt: observedUnix},
			EstimatedExpiresAt: schedule.EstimatedExpiresAt,
			ReminderAt:         schedule.ReminderAt,
		})
	}
	for userID := range state.Users {
		if !knownUsers[userID] {
			delete(state.Users, userID)
			changed = true
		}
	}
	if changed {
		if err := writeState(r.config.StatePath, state); err != nil {
			return nil, err
		}
	}
	return actions, nil
}

func (r *Runner) sendIfCurrent(ctx context.Context, action reminderAction) error {
	return r.config.Send(ctx, action.UserID, action.Text, action.Context)
}

func (r *Runner) completeAttempt(action reminderAction, sendErr error, completedAt time.Time) error {
	unlock, err := acquireFileLock(r.config.StatePath + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	state, err := ReadState(r.config.StatePath)
	if err != nil {
		return err
	}
	userState, ok := state.Users[action.UserID]
	if !ok || userState.CycleID != action.CycleID {
		return nil
	}
	userState.CompletedAt = completedAt.Unix()
	if sendErr == nil {
		userState.Status = StatusSent
		userState.LastError = ""
	} else {
		userState.Status = StatusFailed
		userState.LastError = truncateError(sendErr.Error())
	}
	state.Users[action.UserID] = userState
	return writeState(r.config.StatePath, state)
}

func reminderDeadline(reminderAt time.Time, config Config) time.Time {
	location, _ := time.LoadLocation(config.Timezone)
	hour, minute, _ := parseClock(config.ReminderWindowTo)
	local := reminderAt.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 59, int(time.Second-time.Nanosecond), location)
}

func renderMessage(template, userID string, expiresAt, now time.Time) string {
	remainingMinutes := int(math.Ceil(expiresAt.Sub(now).Minutes()))
	if remainingMinutes < 0 {
		remainingMinutes = 0
	}
	return strings.NewReplacer(
		"{{remaining_minutes}}", strconv.Itoa(remainingMinutes),
		"{{expires_at}}", expiresAt.Format("2006-01-02 15:04 MST"),
		"{{user_id}}", userID,
	).Replace(template)
}

func contextCycleID(userID, contextToken string, observedAt int64) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + contextToken + "\x00" + strconv.FormatInt(observedAt, 10)))
	return hex.EncodeToString(sum[:12])
}

func truncateError(value string) string {
	const limit = 500
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (r *Runner) emit(event Event) {
	if r.config.OnEvent != nil {
		r.config.OnEvent(event)
	}
}
