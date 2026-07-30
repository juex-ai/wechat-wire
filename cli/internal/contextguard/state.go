package contextguard

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	StatusScheduled  = "scheduled"
	StatusAttempting = "attempting"
	StatusSent       = "sent"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

// UserState is the durable at-most-once reminder state for one user context.
type UserState struct {
	CycleID               string `json:"cycle_id"`
	ContextObservedAt     int64  `json:"context_observed_at"`
	EstimatedExpiresAt    int64  `json:"estimated_expires_at"`
	ReminderAt            int64  `json:"reminder_at"`
	MovedBeforeQuietHours bool   `json:"moved_before_quiet_hours"`
	Status                string `json:"status"`
	AttemptedAt           int64  `json:"attempted_at,omitempty"`
	CompletedAt           int64  `json:"completed_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

// State contains reminder attempts keyed by WeChat user ID.
type State struct {
	Users map[string]UserState `json:"users"`
}

// ReadState reads durable reminder state, returning an empty state when absent.
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{Users: map[string]UserState{}}, nil
		}
		return State{}, fmt.Errorf("read context guard state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse context guard state: %w", err)
	}
	if state.Users == nil {
		state.Users = map[string]UserState{}
	}
	return state, nil
}

func writeState(path string, state State) error {
	if state.Users == nil {
		state.Users = map[string]UserState{}
	}
	return writeJSONFile(path, state)
}
