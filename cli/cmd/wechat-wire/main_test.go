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
	t.Setenv("WECHAT_WIRE_BASE_URL", "https://ilink.example")

	stdout, _, err := runArgs("status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{
		"version:",
		"work_dir(env):",
		filepath.Join(dir, ".config", "wechat-wire"),
		"base_url:          https://ilink.example",
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
