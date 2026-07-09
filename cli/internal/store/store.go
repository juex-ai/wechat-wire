// Package store manages local credential metadata and the user book.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/juex-ai/wechat-wire/cli/internal/bot"
)

// ErrNotLoggedIn is returned when credentials are missing.
var ErrNotLoggedIn = errors.New("not logged in. run: wechat-wire login")

// CredentialsInfo is the subset of the upstream SDK credential file we display.
type CredentialsInfo struct {
	Token     string `json:"token"`
	BaseURL   string `json:"base_url"`
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	SavedAt   string `json:"saved_at,omitempty"`
}

// ReadCredentials reads the SDK credentials file.
func ReadCredentials(path string) (*CredentialsInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotLoggedIn
		}
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	var disk struct {
		Token     string `json:"token"`
		BaseURL   string `json:"baseUrl"`
		AccountID string `json:"accountId"`
		UserID    string `json:"userId"`
		SavedAt   string `json:"savedAt"`
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	if disk.Token == "" || disk.AccountID == "" || disk.UserID == "" {
		return nil, fmt.Errorf("credentials missing required fields")
	}
	return &CredentialsInfo{
		Token:     disk.Token,
		BaseURL:   disk.BaseURL,
		AccountID: disk.AccountID,
		UserID:    disk.UserID,
		SavedAt:   disk.SavedAt,
	}, nil
}

// UserRecord is one known WeChat user observed through the bot stream.
type UserRecord struct {
	UserID       string `json:"user_id"`
	LastText     string `json:"last_text"`
	LastType     string `json:"last_type"`
	LastSeenAt   int64  `json:"last_seen_at"`
	MessageCount int    `json:"message_count"`
	HasContext   bool   `json:"has_context"`
}

// UserBook is the on-disk user index.
type UserBook struct {
	Users map[string]UserRecord `json:"users"`
}

// ReadUserBook reads users.json, returning an empty book when it is missing.
func ReadUserBook(path string) (*UserBook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserBook{Users: map[string]UserRecord{}}, nil
		}
		return nil, fmt.Errorf("reading users: %w", err)
	}
	var book UserBook
	if err := json.Unmarshal(data, &book); err != nil {
		return nil, fmt.Errorf("parsing users: %w", err)
	}
	if book.Users == nil {
		book.Users = map[string]UserRecord{}
	}
	return &book, nil
}

// WriteUserBook writes users.json.
func WriteUserBook(path string, book *UserBook) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating user dir: %w", err)
	}
	data, err := json.MarshalIndent(book, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling users: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing users: %w", err)
	}
	return nil
}

// RememberUser records an incoming message in the local user book.
func RememberUser(path string, msg bot.IncomingMessage) error {
	if msg.UserID == "" {
		return fmt.Errorf("message missing user_id")
	}
	book, err := ReadUserBook(path)
	if err != nil {
		return err
	}
	record := book.Users[msg.UserID]
	if record.UserID == "" {
		record.UserID = msg.UserID
	}
	ts := msg.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	msgType := msg.Type
	if msgType == "" {
		msgType = "text"
	}
	record.LastText = msg.Text
	record.LastType = msgType
	record.LastSeenAt = ts.Unix()
	record.MessageCount++
	record.HasContext = record.HasContext || msg.ContextToken != ""
	book.Users[msg.UserID] = record
	return WriteUserBook(path, book)
}

// ListUsers returns users sorted by most recent message.
func ListUsers(path string) ([]UserRecord, error) {
	book, err := ReadUserBook(path)
	if err != nil {
		return nil, err
	}
	users := make([]UserRecord, 0, len(book.Users))
	for _, user := range book.Users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].LastSeenAt == users[j].LastSeenAt {
			return users[i].UserID < users[j].UserID
		}
		return users[i].LastSeenAt > users[j].LastSeenAt
	})
	return users, nil
}

// GetUser returns one user record.
func GetUser(path, userID string) (*UserRecord, bool, error) {
	book, err := ReadUserBook(path)
	if err != nil {
		return nil, false, err
	}
	user, ok := book.Users[userID]
	return &user, ok, nil
}

// ForgetUser removes a user from the local user book.
func ForgetUser(path, userID string) (bool, error) {
	book, err := ReadUserBook(path)
	if err != nil {
		return false, err
	}
	if _, ok := book.Users[userID]; !ok {
		return false, nil
	}
	delete(book.Users, userID)
	return true, WriteUserBook(path, book)
}
