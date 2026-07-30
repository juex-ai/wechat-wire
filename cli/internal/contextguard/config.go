package contextguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultMessageTemplate = "记得回复我一下，不然按当前估算，再过约 {{remaining_minutes}} 分钟，我就暂时没法主动给你发提醒啦。"

// ConfigPatch updates only explicitly supplied context guard settings.
type ConfigPatch struct {
	Enabled            *bool
	AssumedTTLMinutes  *int
	LeadTimeMinutes    *int
	Timezone           *string
	ReminderWindowFrom *string
	ReminderWindowTo   *string
	MessageTemplate    *string
}

// DefaultConfig returns the conservative opt-in context guard policy.
func DefaultConfig() Config {
	return Config{
		Enabled:            false,
		AssumedTTLMinutes:  24 * 60,
		LeadTimeMinutes:    60,
		Timezone:           "Local",
		ReminderWindowFrom: "08:00",
		ReminderWindowTo:   "22:00",
		MessageTemplate:    DefaultMessageTemplate,
	}
}

// ReadConfig reads a context guard configuration, returning defaults when the
// file does not exist.
func ReadConfig(path string) (Config, error) {
	config := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return Config{}, fmt.Errorf("read context guard config: %w", err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse context guard config: %w", err)
	}
	if err := ValidateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

// UpdateConfig applies and persists a partial configuration update.
func UpdateConfig(path string, patch ConfigPatch) (Config, error) {
	unlock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return Config{}, err
	}
	defer unlock()

	config, err := ReadConfig(path)
	if err != nil {
		return Config{}, err
	}
	if patch.Enabled != nil {
		config.Enabled = *patch.Enabled
	}
	if patch.AssumedTTLMinutes != nil {
		config.AssumedTTLMinutes = *patch.AssumedTTLMinutes
	}
	if patch.LeadTimeMinutes != nil {
		config.LeadTimeMinutes = *patch.LeadTimeMinutes
	}
	if patch.Timezone != nil {
		config.Timezone = strings.TrimSpace(*patch.Timezone)
	}
	if patch.ReminderWindowFrom != nil {
		config.ReminderWindowFrom = strings.TrimSpace(*patch.ReminderWindowFrom)
	}
	if patch.ReminderWindowTo != nil {
		config.ReminderWindowTo = strings.TrimSpace(*patch.ReminderWindowTo)
	}
	if patch.MessageTemplate != nil {
		config.MessageTemplate = strings.TrimSpace(*patch.MessageTemplate)
	}
	if err := ValidateConfig(config); err != nil {
		return Config{}, err
	}
	if err := writeJSONFile(path, config); err != nil {
		return Config{}, err
	}
	return config, nil
}

// ValidateConfig checks scheduling and message constraints.
func ValidateConfig(config Config) error {
	if strings.TrimSpace(config.MessageTemplate) == "" {
		return fmt.Errorf("message_template must not be empty")
	}
	if len(config.MessageTemplate) > 4000 {
		return fmt.Errorf("message_template must not exceed 4000 bytes")
	}
	_, err := PlanSchedule(time.Now(), config)
	return err
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create context guard directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary context guard file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temporary context guard file: %w", err)
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temp.Close()
		return fmt.Errorf("encode context guard file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close context guard file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace context guard file: %w", err)
	}
	return nil
}
