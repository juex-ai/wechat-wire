// Package contextguard manages proactive context-token expiry reminders.
package contextguard

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config controls when and how context expiry reminders are sent.
type Config struct {
	Enabled            bool   `json:"enabled"`
	AssumedTTLMinutes  int    `json:"assumed_ttl_minutes"`
	LeadTimeMinutes    int    `json:"lead_time_minutes"`
	Timezone           string `json:"timezone"`
	ReminderWindowFrom string `json:"reminder_window_from"`
	ReminderWindowTo   string `json:"reminder_window_to"`
	MessageTemplate    string `json:"message_template"`
}

// Schedule describes one reminder deadline derived from an observed context.
type Schedule struct {
	EstimatedExpiresAt    time.Time
	ReminderAt            time.Time
	MovedBeforeQuietHours bool
}

// PlanSchedule calculates the reminder time and moves quiet-hour reminders to
// the most recent allowed window end.
func PlanSchedule(observedAt time.Time, config Config) (Schedule, error) {
	if config.AssumedTTLMinutes <= 0 {
		return Schedule{}, fmt.Errorf("assumed_ttl_minutes must be greater than zero")
	}
	if config.LeadTimeMinutes <= 0 || config.LeadTimeMinutes >= config.AssumedTTLMinutes {
		return Schedule{}, fmt.Errorf("lead_time_minutes must be greater than zero and less than assumed_ttl_minutes")
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return Schedule{}, fmt.Errorf("load timezone %q: %w", config.Timezone, err)
	}
	windowFromHour, windowFromMinute, err := parseClock(config.ReminderWindowFrom)
	if err != nil {
		return Schedule{}, fmt.Errorf("reminder_window_from: %w", err)
	}
	windowToHour, windowToMinute, err := parseClock(config.ReminderWindowTo)
	if err != nil {
		return Schedule{}, fmt.Errorf("reminder_window_to: %w", err)
	}
	fromMinutes := windowFromHour*60 + windowFromMinute
	toMinutes := windowToHour*60 + windowToMinute
	if fromMinutes >= toMinutes {
		return Schedule{}, fmt.Errorf("reminder window start must be before its end")
	}

	expiresAt := observedAt.Add(time.Duration(config.AssumedTTLMinutes) * time.Minute).In(location)
	reminderAt := expiresAt.Add(-time.Duration(config.LeadTimeMinutes) * time.Minute)
	windowFrom := time.Date(reminderAt.Year(), reminderAt.Month(), reminderAt.Day(), windowFromHour, windowFromMinute, 0, 0, location)
	windowTo := time.Date(reminderAt.Year(), reminderAt.Month(), reminderAt.Day(), windowToHour, windowToMinute, 0, 0, location)

	moved := false
	switch {
	case reminderAt.Before(windowFrom):
		reminderAt = windowTo.AddDate(0, 0, -1)
		moved = true
	case reminderAt.After(windowTo):
		reminderAt = windowTo
		moved = true
	}

	return Schedule{
		EstimatedExpiresAt:    expiresAt,
		ReminderAt:            reminderAt,
		MovedBeforeQuietHours: moved,
	}, nil
}

func parseClock(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	return hour, minute, nil
}
