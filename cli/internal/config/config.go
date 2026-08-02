// Package config resolves runtime configuration for the CLI.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	appName = "wechat-wire"
)

var (
	dirConfigured bool
	configDir     string
)

// SetDirOverride configures the active config directory from --homedir.
func SetDirOverride(dir string) error {
	dirConfigured = false
	configDir = ""
	if dir == "" {
		return nil
	}

	resolved, err := ResolveDir(dir)
	if err != nil {
		return err
	}
	dirConfigured = true
	configDir = resolved
	return nil
}

// ResolveDir returns an explicit --homedir or WECHAT_WIRE_DIR unchanged after
// path normalization. Without either override it returns ~/.config/wechat-wire.
func ResolveDir(dirOverride string) (string, error) {
	if dirOverride != "" {
		if hasParentTraversal(dirOverride) && !filepath.IsAbs(dirOverride) {
			return "", fmt.Errorf("--homedir relative path must not contain '..': %s", dirOverride)
		}
		return absolutePath(dirOverride)
	}

	if envDir := os.Getenv("WECHAT_WIRE_DIR"); envDir != "" {
		return absolutePath(envDir)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return absolutePath(filepath.Join(home, ".config", appName))
}

// Dir returns the active config directory.
func Dir() string {
	if dirConfigured {
		return configDir
	}
	dir, err := ResolveDir("")
	if err != nil {
		return filepath.Join(".", ".config", appName)
	}
	return dir
}

// DirSource returns which input selected the active config directory.
func DirSource() string {
	if dirConfigured {
		return "flag"
	}
	if os.Getenv("WECHAT_WIRE_DIR") != "" {
		return "env"
	}
	return "default"
}

// CredentialPath returns the SDK credential file path.
func CredentialPath() string {
	return filepath.Join(Dir(), "credentials.json")
}

// UsersPath returns the local user book path.
func UsersPath() string {
	return filepath.Join(Dir(), "users.json")
}

// MediaDir returns the private directory for downloaded inbound media.
func MediaDir() string {
	return filepath.Join(Dir(), "media")
}

// ContextGuardConfigPath returns the proactive context reminder policy path.
func ContextGuardConfigPath() string {
	return filepath.Join(Dir(), "context-guard.json")
}

// ContextGuardStatePath returns the durable at-most-once reminder state path.
func ContextGuardStatePath() string {
	return filepath.Join(Dir(), "context-guard-state.json")
}

// LogLevel returns the fixed SDK log level.
func LogLevel() string {
	return "info"
}

// BotAgent returns the SDK bot_agent identity.
func BotAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "wechat-wire/" + version
}

func absolutePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path %s: %w", path, err)
	}
	return abs, nil
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
