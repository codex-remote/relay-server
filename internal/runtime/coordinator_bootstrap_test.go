package runtime

import (
	"testing"
	"time"

	"github.com/codex-remote/relay-server/internal/protocol"
)

func TestBootstrapBatchImportsOnlyTerminalTurns(t *testing.T) {
	now := time.Now().UTC()
	payload := protocol.BootstrapBatchPayload{
		Thread: &protocol.ThreadDetail{
			ID: "thread-1", ProjectID: "project-1", Title: "History", CreatedAt: now, UpdatedAt: now,
			Turns: []protocol.ThreadHistoryTurn{
				{ID: "completed", Status: "completed"},
				{ID: "running", Status: "running"},
				{ID: "unknown", Status: "mystery"},
				{ID: "failed", Status: "failed"},
				{ID: "canceled", Status: "canceled"},
			},
		},
	}

	batch := bootstrapBatch(payload)
	if len(batch.Runs) != 3 {
		t.Fatalf("terminal runs = %d, want 3: %#v", len(batch.Runs), batch.Runs)
	}
	for index, expected := range []string{"completed", "failed", "canceled"} {
		if batch.Runs[index].Status != expected {
			t.Fatalf("run %d status = %q, want %q", index, batch.Runs[index].Status, expected)
		}
	}
}
