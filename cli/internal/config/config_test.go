package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDirUsesFlagDirectoryBeforeEnv(t *testing.T) {
	envRoot := t.TempDir()
	flagRoot := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", envRoot)

	got, err := ResolveDir(flagRoot)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := flagRoot
	if got != want {
		t.Fatalf("ResolveDir: got %q want %q", got, want)
	}
}

func TestResolveDirUsesEnvironmentDirectoryDirectly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WECHAT_WIRE_DIR", root)

	got, err := ResolveDir("")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := root
	if got != want {
		t.Fatalf("ResolveDir: got %q want %q", got, want)
	}
}

func TestResolveDirDefaultsToUserConfigDirectory(t *testing.T) {
	t.Setenv("WECHAT_WIRE_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	got, err := ResolveDir("")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := filepath.Join(home, ".config", "wechat-wire")
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
	if err := SetDirOverride(root); err != nil {
		t.Fatalf("SetDirOverride: %v", err)
	}
	t.Cleanup(func() {
		_ = SetDirOverride("")
	})

	got := CredentialPath()
	want := filepath.Join(root, "credentials.json")
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
