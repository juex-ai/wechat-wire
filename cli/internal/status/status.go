// Package status formats runtime diagnostics for the CLI and MCP server.
package status

import (
	"fmt"
	"strings"

	"github.com/juex-ai/wechat-wire/cli/internal/config"
	"github.com/juex-ai/wechat-wire/cli/internal/store"
)

const labelWidth = 18

// Info is the machine-readable runtime status shape.
type Info struct {
	Version        string `json:"version"`
	WorkDirSource  string `json:"work_dir_source"`
	WorkDir        string `json:"work_dir"`
	BaseURL        string `json:"base_url"`
	CredentialPath string `json:"credential_path"`
	LoggedIn       bool   `json:"logged_in"`
	AccountID      string `json:"account_id"`
	UserID         string `json:"user_id"`
	UserCount      int    `json:"user_count"`
}

// Version formats build metadata only.
func Version(version, commit string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s %s\n", labelWidth, "version:", version)
	fmt.Fprintf(&b, "%-*s %s\n", labelWidth, "commit:", commit)
	return b.String()
}

// Runtime formats runtime configuration diagnostics.
func Runtime(version string) string {
	info := RuntimeInfo(version)
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s %s\n", labelWidth, "version:", info.Version)
	fmt.Fprintf(&b, "%-*s %s\n", labelWidth, fmt.Sprintf("work_dir(%s):", info.WorkDirSource), info.WorkDir)
	fmt.Fprintf(&b, "%-*s %s\n", labelWidth, "base_url:", info.BaseURL)
	fmt.Fprintf(&b, "%-*s %s", labelWidth, "credentials:", info.CredentialPath)
	if info.LoggedIn {
		fmt.Fprintf(&b, " logged_in account_id=%s user_id=%s", info.AccountID, info.UserID)
	} else {
		fmt.Fprint(&b, " not_logged_in")
	}
	fmt.Fprint(&b, "\n")
	fmt.Fprintf(&b, "%-*s %d\n", labelWidth, "users:", info.UserCount)
	return b.String()
}

// RuntimeInfo returns runtime configuration diagnostics as structured data.
func RuntimeInfo(version string) Info {
	info := Info{
		Version:        version,
		WorkDirSource:  config.DirSource(),
		WorkDir:        config.Dir(),
		BaseURL:        config.DisplayBaseURL(),
		CredentialPath: config.CredentialPath(),
	}
	if creds, err := store.ReadCredentials(config.CredentialPath()); err == nil {
		info.LoggedIn = true
		info.AccountID = creds.AccountID
		info.UserID = creds.UserID
	}
	if users, err := store.ListUsers(config.UsersPath()); err == nil {
		info.UserCount = len(users)
	}
	return info
}
