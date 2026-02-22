package watcher_test

import (
	"testing"

	"torrentstream/notifier/internal/watcher"
)

func TestIsCompletionEvent_DetectsCompleted(t *testing.T) {
	event := watcher.ChangeEvent{
		OperationType: "update",
		UpdatedFields: map[string]interface{}{
			"status": "completed",
		},
	}
	if !watcher.IsCompletionEvent(event) {
		t.Error("should detect completion event")
	}
}

func TestIsCompletionEvent_IgnoresOtherStatuses(t *testing.T) {
	for _, status := range []string{"active", "stopped", "pending", "error"} {
		event := watcher.ChangeEvent{
			OperationType: "update",
			UpdatedFields: map[string]interface{}{"status": status},
		}
		if watcher.IsCompletionEvent(event) {
			t.Errorf("should not detect %q as completion", status)
		}
	}
}

func TestIsCompletionEvent_IgnoresInsert(t *testing.T) {
	event := watcher.ChangeEvent{
		OperationType: "insert",
		UpdatedFields: map[string]interface{}{"status": "completed"},
	}
	if watcher.IsCompletionEvent(event) {
		t.Error("insert events should not trigger notification")
	}
}
