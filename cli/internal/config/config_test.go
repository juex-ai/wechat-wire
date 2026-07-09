package config

import (
	"path/filepath"
	"testing"
)

func TestResolveDirUsesHomeDirFlagBeforeEnv(t *testing.T) {
	envRoot := t.TempDir()
	flagRoot := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", envRoot)

	got, err := ResolveDir(flagRoot)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := filepath.Join(flagRoot, ".config", "wechat-wire")
	if got != want {
		t.Fatalf("ResolveDir: got %q want %q", got, want)
	}
}

func TestResolveDirNormalizesConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", root)

	got, err := ResolveDir("")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := filepath.Join(root, ".config", "wechat-wire")
	if got != want {
		t.Fatalf("ResolveDir: got %q want %q", got, want)
	}
}

func TestResolveDirKeepsExistingConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", filepath.Join(root, ".config", "wechat-wire"))

	got, err := ResolveDir("")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := filepath.Join(root, ".config", "wechat-wire")
	if got != want {
		t.Fatalf("ResolveDir: got %q want %q", got, want)
	}
}

func TestResolveDirRejectsRelativeHomeDirWithParentTraversal(t *testing.T) {
	if _, err := ResolveDir(filepath.Join("..", "other")); err == nil {
		t.Fatal("expected parent traversal error, got nil")
	}
	if _, err := ResolveDir("child/../other"); err == nil {
		t.Fatal("expected nested parent traversal error, got nil")
	}
}

func TestCredentialPathStaysUnderConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WECHAT_WIRE_CRED_PATH", filepath.Join(t.TempDir(), "credentials.json"))
	if err := SetHomeDir(root); err != nil {
		t.Fatalf("SetHomeDir: %v", err)
	}
	t.Cleanup(func() {
		_ = SetHomeDir("")
	})

	got := CredentialPath()
	want := filepath.Join(root, ".config", "wechat-wire", "credentials.json")
	if got != want {
		t.Fatalf("CredentialPath: got %q want %q", got, want)
	}
}

func TestSDKOptionsIgnoreEnvironmentOverrides(t *testing.T) {
	t.Setenv("WECHAT_WIRE_LOG_LEVEL", "debug")
	t.Setenv("WECHAT_WIRE_BOT_AGENT", "custom-agent")

	if got := LogLevel(); got != "info" {
		t.Fatalf("LogLevel: got %q want %q", got, "info")
	}
	if got := BotAgent("test-version"); got != "wechat-wire/test-version" {
		t.Fatalf("BotAgent: got %q want %q", got, "wechat-wire/test-version")
	}
}
