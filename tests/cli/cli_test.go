package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIListenAndSendWithFakeBackend(t *testing.T) {
	bin := binary(t)
	dataDir := t.TempDir()
	env := append(os.Environ(),
		"WECHAT_WIRE_DIR="+dataDir,
		"WECHAT_WIRE_FAKE=1",
		`WECHAT_WIRE_FAKE_MESSAGES_JSON=[{"user_id":"user-1","text":"hello cli","type":"text","context_token":"ctx-1"}]`,
	)

	listenOut := runCLI(t, env, bin, "listen", "--once", "--format", "json")
	if !strings.Contains(listenOut, `"user_id":"user-1"`) || !strings.Contains(listenOut, `"text":"hello cli"`) {
		t.Fatalf("listen output missing fake message:\n%s", listenOut)
	}

	sendOut := runCLI(t, env, bin, "msg", "send", "--user_id", "user-1", "--text", "reply from cli", "--format", "json")
	var got struct {
		Sent   bool   `json:"sent"`
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(sendOut), &got); err != nil {
		t.Fatalf("parse send JSON: %v\n%s", err, sendOut)
	}
	if !got.Sent || got.UserID != "user-1" || got.Text != "reply from cli" {
		t.Fatalf("unexpected send response: %+v", got)
	}

	attachmentPath := filepath.Join(dataDir, "report.txt")
	if err := os.WriteFile(attachmentPath, []byte("attachment from cli"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	attachmentOut := runCLI(t, env, bin, "msg", "send", "--user_id", "user-1", "--file", attachmentPath, "--caption", "report", "--format", "json")
	var attachment struct {
		Sent      bool   `json:"sent"`
		UserID    string `json:"user_id"`
		FileName  string `json:"file_name"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal([]byte(attachmentOut), &attachment); err != nil {
		t.Fatalf("parse attachment JSON: %v\n%s", err, attachmentOut)
	}
	if !attachment.Sent || attachment.UserID != "user-1" || attachment.FileName != "report.txt" || attachment.SizeBytes != 19 {
		t.Fatalf("unexpected attachment response: %+v", attachment)
	}
}

func binary(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("WECHAT_WIRE_BIN"); v != "" {
		return v
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return filepath.Join(root, "bin", "wechat-wire")
}

func runCLI(t *testing.T, env []string, bin string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("wechat-wire %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}
