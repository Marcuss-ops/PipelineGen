package scripts

import (
	"testing"
)

func TestNewScriptRepository(t *testing.T) {
	t.Run("nil db", func(t *testing.T) {
		repo := NewScriptRepository(nil)
		if repo == nil {
			t.Error("Expected non-nil repository")
		}
	})
}

func TestScriptRecord(t *testing.T) {
	script := &ScriptRecord{
		Topic:    "Test Topic",
		Duration: 60,
		Language: "en",
	}
	
	if script.Topic != "Test Topic" {
		t.Errorf("Topic = %s, want %s", script.Topic, "Test Topic")
	}
	if script.Duration != 60 {
		t.Errorf("Duration = %d, want %d", script.Duration, 60)
	}
}