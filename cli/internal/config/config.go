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
	homeDirConfigured bool
	configDir         string
)

// SetHomeDir configures the active config directory from --homedir.
func SetHomeDir(homeDir string) error {
	homeDirConfigured = false
	configDir = ""
	if homeDir == "" {
		return nil
	}

	dir, err := ResolveDir(homeDir)
	if err != nil {
		return err
	}
	homeDirConfigured = true
	configDir = dir
	return nil
}

// ResolveDir returns the wechat-wire config directory from --homedir,
// WECHAT_WIRE_DIR, or the current user's home directory.
func ResolveDir(homeDir string) (string, error) {
	base := homeDir
	rejectParentTraversal := homeDir != ""
	if base == "" {
		base = os.Getenv("WECHAT_WIRE_DIR")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "."
		} else {
			base = home
		}
	}

	if rejectParentTraversal && hasParentTraversal(base) && !filepath.IsAbs(base) {
		return "", fmt.Errorf("--homedir relative path must not contain '..': %s", base)
	}

	absBase, err := absolutePath(base)
	if err != nil {
		return "", err
	}
	return appendConfigApp(absBase), nil
}

// Dir returns the active config directory.
func Dir() string {
	if homeDirConfigured {
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
	if homeDirConfigured {
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

func appendConfigApp(path string) string {
	clean := filepath.Clean(path)
	if filepath.Base(clean) == appName && filepath.Base(filepath.Dir(clean)) == ".config" {
		return clean
	}
	if filepath.Base(clean) == ".config" {
		return filepath.Join(clean, appName)
	}
	return filepath.Join(clean, ".config", appName)
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
