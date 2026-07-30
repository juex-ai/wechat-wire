package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func runArgs(args ...string) (string, string, error) {
	cmd := rootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestVersionJSON(t *testing.T) {
	stdout, _, err := runArgs("version", "--format", "json")
	if err != nil {
		t.Fatalf("version --format json: %v", err)
	}
	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("version JSON: %v\n%s", err, stdout)
	}
	if got.Version == "" || got.Commit == "" {
		t.Fatalf("version JSON missing fields: %+v", got)
	}
}

func TestStatusShowsRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", dir)

	stdout, _, err := runArgs("status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{
		"version:",
		"work_dir(env):",
		filepath.Join(dir, ".config", "wechat-wire"),
		"credentials:",
		"users:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
}

func TestLoginFakeWritesCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", dir)
	t.Setenv("WECHAT_WIRE_FAKE", "1")

	stdout, _, err := runArgs("login", "--format", "json")
	if err != nil {
		t.Fatalf("login fake: %v", err)
	}
	var got struct {
		AccountID string `json:"account_id"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("login JSON: %v\n%s", err, stdout)
	}
	if got.AccountID == "" || got.UserID == "" {
		t.Fatalf("login returned empty credentials: %+v", got)
	}
}

func TestListenOnceFakeRecordsUser(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", dir)
	t.Setenv("WECHAT_WIRE_FAKE", "1")
	t.Setenv("WECHAT_WIRE_FAKE_MESSAGES_JSON", `[{"user_id":"user-1","text":"hello from fake","type":"text","context_token":"ctx-1"}]`)

	stdout, _, err := runArgs("listen", "--once", "--format", "json")
	if err != nil {
		t.Fatalf("listen fake once: %v", err)
	}
	if !strings.Contains(stdout, `"user_id":"user-1"`) || !strings.Contains(stdout, `"text":"hello from fake"`) {
		t.Fatalf("listen JSON missing fake message:\n%s", stdout)
	}

	users, _, err := runArgs("user", "list", "--format", "json")
	if err != nil {
		t.Fatalf("user list: %v", err)
	}
	if !strings.Contains(users, `"user_id":"user-1"`) {
		t.Fatalf("user list missing recorded user:\n%s", users)
	}
	if strings.Contains(users, "ctx-1") || strings.Contains(users, "context_token") {
		t.Fatalf("user list should not expose context token:\n%s", users)
	}
}

func TestMsgSendFakeUsesRecordedUserContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", dir)
	t.Setenv("WECHAT_WIRE_FAKE", "1")
	t.Setenv("WECHAT_WIRE_FAKE_MESSAGES_JSON", `[{"user_id":"user-1","text":"hello from fake","type":"text","context_token":"ctx-1"}]`)

	if _, _, err := runArgs("listen", "--once", "--format", "json"); err != nil {
		t.Fatalf("listen fake once: %v", err)
	}

	stdout, _, err := runArgs("msg", "send", "--user_id", "user-1", "--text", "reply from cli", "--format", "json")
	if err != nil {
		t.Fatalf("msg send fake: %v", err)
	}
	var got struct {
		Sent   bool   `json:"sent"`
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("msg send JSON: %v\n%s", err, stdout)
	}
	if !got.Sent || got.UserID != "user-1" || got.Text != "reply from cli" {
		t.Fatalf("unexpected msg send response: %+v", got)
	}
}

func TestContextGuardSetAndShowJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", dir)

	template := "Please reply within {{remaining_minutes}} minutes."
	stdout, _, err := runArgs(
		"context-guard", "set",
		"--enabled=true",
		"--lead-time-minutes=90",
		"--timezone=Asia/Shanghai",
		"--message-template="+template,
		"--format=json",
	)
	if err != nil {
		t.Fatalf("context-guard set: %v", err)
	}
	var updated struct {
		Enabled         bool   `json:"enabled"`
		LeadTimeMinutes int    `json:"lead_time_minutes"`
		Timezone        string `json:"timezone"`
		MessageTemplate string `json:"message_template"`
	}
	if err := json.Unmarshal([]byte(stdout), &updated); err != nil {
		t.Fatalf("set JSON: %v\n%s", err, stdout)
	}
	if !updated.Enabled || updated.LeadTimeMinutes != 90 || updated.Timezone != "Asia/Shanghai" || updated.MessageTemplate != template {
		t.Fatalf("unexpected updated config: %+v", updated)
	}

	stdout, _, err = runArgs("context-guard", "show", "--format=json")
	if err != nil {
		t.Fatalf("context-guard show: %v", err)
	}
	var shown map[string]any
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("show JSON: %v\n%s", err, stdout)
	}
	if shown["enabled"] != true || shown["message_template"] != template {
		t.Fatalf("unexpected shown config: %+v", shown)
	}
}
