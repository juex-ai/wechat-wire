package contextguard

import (
	"testing"
	"time"
)

func TestPlanScheduleMovesQuietHourReminderToPreviousWindowEnd(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 3, 0, 0, 0, location)

	schedule, err := PlanSchedule(observedAt, Config{
		AssumedTTLMinutes:  24 * 60,
		LeadTimeMinutes:    60,
		Timezone:           "Asia/Shanghai",
		ReminderWindowFrom: "08:00",
		ReminderWindowTo:   "22:00",
	})
	if err != nil {
		t.Fatalf("PlanSchedule: %v", err)
	}

	wantExpiry := time.Date(2026, 7, 31, 3, 0, 0, 0, location)
	wantReminder := time.Date(2026, 7, 30, 22, 0, 0, 0, location)
	if !schedule.EstimatedExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiry: got %s want %s", schedule.EstimatedExpiresAt, wantExpiry)
	}
	if !schedule.ReminderAt.Equal(wantReminder) {
		t.Fatalf("reminder: got %s want %s", schedule.ReminderAt, wantReminder)
	}
	if !schedule.MovedBeforeQuietHours {
		t.Fatal("expected reminder to move before quiet hours")
	}
}

func TestPlanScheduleKeepsDaytimeReminderAtLeadTime(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	observedAt := time.Date(2026, 7, 30, 20, 0, 0, 0, location)

	schedule, err := PlanSchedule(observedAt, Config{
		AssumedTTLMinutes:  24 * 60,
		LeadTimeMinutes:    60,
		Timezone:           "Asia/Shanghai",
		ReminderWindowFrom: "08:00",
		ReminderWindowTo:   "22:00",
	})
	if err != nil {
		t.Fatalf("PlanSchedule: %v", err)
	}

	want := time.Date(2026, 7, 31, 19, 0, 0, 0, location)
	if !schedule.ReminderAt.Equal(want) {
		t.Fatalf("reminder: got %s want %s", schedule.ReminderAt, want)
	}
	if schedule.MovedBeforeQuietHours {
		t.Fatal("daytime reminder should not move")
	}
}
