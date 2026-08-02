package contextguard

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReadConfigReturnsSafeDefaultsWhenMissing(t *testing.T) {
	config, err := ReadConfig(filepath.Join(t.TempDir(), "context-guard.json"))
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !config.Enabled {
		t.Fatal("context guard should be enabled by default")
	}
	if config.AssumedTTLMinutes != 24*60 || config.LeadTimeMinutes != 60 {
		t.Fatalf("unexpected timing defaults: %+v", config)
	}
	if config.ReminderWindowFrom != "08:00" || config.ReminderWindowTo != "22:00" {
		t.Fatalf("unexpected reminder window: %+v", config)
	}
	if config.MessageTemplate != DefaultMessageTemplate {
		t.Fatalf("unexpected default message template: %q", config.MessageTemplate)
	}
}

func TestUpdateConfigPersistsPartialPatchPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context-guard.json")
	enabled := true
	template := "Reply within about {{remaining_minutes}} minutes."

	config, err := UpdateConfig(path, ConfigPatch{
		Enabled:         &enabled,
		MessageTemplate: &template,
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if !config.Enabled || config.MessageTemplate != template {
		t.Fatalf("unexpected updated config: %+v", config)
	}
	if config.AssumedTTLMinutes != 24*60 || config.ReminderWindowTo != "22:00" {
		t.Fatalf("partial patch lost defaults: %+v", config)
	}

	reloaded, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if reloaded != config {
		t.Fatalf("reloaded config: got %+v want %+v", reloaded, config)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: got %o want 600", info.Mode().Perm())
	}
}

func TestUpdateConfigRejectsInvalidPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context-guard.json")
	lead := 24 * 60

	_, err := UpdateConfig(path, ConfigPatch{LeadTimeMinutes: &lead})
	if err == nil {
		t.Fatal("expected invalid lead time error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid config should not be persisted: %v", statErr)
	}
}

func TestConcurrentConfigPatchesDoNotLoseSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context-guard.json")
	enabled := true
	timezone := "Asia/Shanghai"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup

	for _, patch := range []ConfigPatch{
		{Enabled: &enabled},
		{Timezone: &timezone},
	} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := UpdateConfig(path, patch)
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("UpdateConfig: %v", err)
		}
	}

	config, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !config.Enabled || config.Timezone != timezone {
		t.Fatalf("concurrent patches were lost: %+v", config)
	}
}
