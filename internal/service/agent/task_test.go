package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewTaskIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewTaskID()
		if len(id) != 16 {
			t.Fatalf("panjang ID tidak sesuai: %s", id)
		}
		if seen[id] {
			t.Fatalf("ID duplikat: %s", id)
		}
		seen[id] = true
	}
}

func TestTaskNilSafe(t *testing.T) {
	var t2 *Task
	t2.AddStep("a", "b", "c", 1.0, nil, 0) // tidak boleh panic
	if t2.Cost() != 0 {
		t.Error("task nil harus cost 0")
	}
	if t2.Steps() != nil {
		t.Error("task nil harus steps nil")
	}
	if TaskFromContext(context.Background()) != nil {
		t.Error("ctx tanpa task harus return nil")
	}
}

func TestTaskRecordsStepsAndCost(t *testing.T) {
	ctx, task := NewTask(context.Background(), discardLogger())
	if TaskFromContext(ctx) != task {
		t.Fatal("task tidak tersimpan di context")
	}

	task.AddStep("orchestrator", "llm.classify_intent", "record_transaction", 0.0001, nil, 10*time.Millisecond)
	task.AddStep("transaction", "llm.extract", "EXPENSE", 0.0002, nil, 20*time.Millisecond)
	task.AddStep("transaction", "persist", "EXPENSE", 0, errors.New("db down"), 5*time.Millisecond)

	steps := task.Steps()
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[2].Err != "db down" {
		t.Errorf("expected err 'db down', got %q", steps[2].Err)
	}
	if got := task.Cost(); got < 0.0002999 || got > 0.0003001 {
		t.Errorf("expected cost ~0.0003, got %v", got)
	}
}

func TestNewTaskWithIDUsesGivenID(t *testing.T) {
	ctx, task := NewTaskWithID(context.Background(), discardLogger(), "custom-id")
	if task.ID != "custom-id" {
		t.Errorf("expected custom-id, got %s", task.ID)
	}
	if TaskFromContext(ctx) != task {
		t.Fatal("task tidak tersimpan di context")
	}
	_, generated := NewTask(context.Background(), discardLogger())
	if len(generated.ID) != 16 {
		t.Errorf("expected generated 16-char id, got %s", generated.ID)
	}
}

func TestTaskTrace(t *testing.T) {
	_, task := NewTask(context.Background(), discardLogger())
	task.AddStep("orchestrator", "llm.classify_intent", "record_transaction", 0, nil, 0)
	task.AddStep("transaction", "llm.extract", "EXPENSE", 0, errors.New("timeout"), 0)

	want := "orchestrator/llm.classify_intent(record_transaction) -> transaction/llm.extract(EXPENSE)[ERR: timeout]"
	if got := task.Trace(); got != want {
		t.Errorf("trace tidak sesuai:\n got: %s\nwant: %s", got, want)
	}
	if !strings.Contains(task.Trace(), "llm.classify_intent") {
		t.Error("trace harus memuat langkah")
	}
}
