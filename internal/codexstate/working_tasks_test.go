package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/threadcatalogcontract"
)

func TestSQLiteThreadCatalogWorkingTasksReturnsOnlyActiveTopLevelThreads(t *testing.T) {
	dbPath := createThreadCatalogTestDB(t)
	rolloutDir := t.TempDir()
	activeCLI := writeWorkingTaskRollout(t, rolloutDir, "thread-1", []string{
		rolloutSessionMetaJSON("codex-tui"),
		rolloutLifecycleJSON("task_started"),
	})
	activeDesktop := writeWorkingTaskRollout(t, rolloutDir, "thread-3", []string{
		rolloutSessionMetaJSON("Codex Desktop"),
		rolloutLifecycleJSON("task_complete"),
		rolloutLifecycleJSON("task_started"),
	})
	remoteHeadless := writeWorkingTaskRollout(t, rolloutDir, "thread-2", []string{
		rolloutSessionMetaJSON("Codex Remote Headless"),
		rolloutLifecycleJSON("task_started"),
	})
	filteredSubagent := writeWorkingTaskRollout(t, rolloutDir, "thread-subagent", []string{
		rolloutSessionMetaJSON("Codex Desktop"),
		rolloutLifecycleJSON("task_started"),
	})

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}
	defer db.Close()
	updates := []struct {
		id      string
		path    string
		archive int
	}{
		{id: "thread-1", path: activeCLI},
		{id: "thread-3", path: activeDesktop},
		{id: "thread-2", path: remoteHeadless, archive: 0},
		{id: "thread-subagent", path: filteredSubagent},
	}
	for _, update := range updates {
		if _, err := db.Exec(`UPDATE threads SET rollout_path = ?, archived = ? WHERE id = ?`, update.path, update.archive, update.id); err != nil {
			t.Fatalf("update rollout path for %s: %v", update.id, err)
		}
	}

	catalog := NewSQLiteThreadCatalog(dbPath, SQLiteThreadCatalogOptions{Logf: func(string, ...any) {}})
	tasks, err := catalog.WorkingTasks(10)
	if err != nil {
		t.Fatalf("working tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("working tasks = %#v, want two active top-level tasks", tasks)
	}
	if tasks[0].Thread.ThreadID != "thread-3" || tasks[0].Source != threadcatalogcontract.WorkingTaskSourceCodexDesktop {
		t.Fatalf("first working task = %#v, want desktop thread-3", tasks[0])
	}
	if tasks[1].Thread.ThreadID != "thread-1" || tasks[1].Source != threadcatalogcontract.WorkingTaskSourceCodexCLI {
		t.Fatalf("second working task = %#v, want CLI thread-1", tasks[1])
	}
}

func TestSQLiteThreadCatalogWorkingTasksExcludesStaleUnclosedRollout(t *testing.T) {
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	dbPath := createThreadCatalogTestDB(t)
	path := writeWorkingTaskRollout(t, t.TempDir(), "thread-1", []string{
		rolloutSessionMetaJSON("Codex Desktop"),
		rolloutLifecycleJSON("task_started"),
	})
	staleAt := now.Add(-workingTaskActivityWindow - time.Second)
	if err := os.Chtimes(path, staleAt, staleAt); err != nil {
		t.Fatalf("mark rollout stale: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE threads SET rollout_path = ? WHERE id = 'thread-1'`, path); err != nil {
		t.Fatalf("update rollout path: %v", err)
	}

	catalog := NewSQLiteThreadCatalog(dbPath, SQLiteThreadCatalogOptions{
		Logf: func(string, ...any) {},
		Now:  func() time.Time { return now },
	})
	tasks, err := catalog.WorkingTasks(10)
	if err != nil {
		t.Fatalf("working tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("stale unclosed rollout should not stay active: %#v", tasks)
	}
}

func TestRolloutHasActiveTaskReadsBackwardAcrossLargeTurn(t *testing.T) {
	path := writeWorkingTaskRollout(t, t.TempDir(), "thread-large", []string{
		rolloutSessionMetaJSON("Codex Desktop"),
		rolloutLifecycleJSON("task_started"),
		`{"type":"event_msg","payload":{"type":"agent_message","message":"` + strings.Repeat("x", rolloutLifecycleReadChunkSize*2) + `"}}`,
	})
	active, err := rolloutHasActiveTask(path)
	if err != nil {
		t.Fatalf("read active rollout: %v", err)
	}
	if !active {
		t.Fatal("expected task_started before a large turn body to remain active")
	}
}

func TestRolloutHasActiveTaskTreatsCompletionAndAbortAsTerminal(t *testing.T) {
	for _, terminal := range []string{"task_complete", "turn_aborted"} {
		t.Run(terminal, func(t *testing.T) {
			path := writeWorkingTaskRollout(t, t.TempDir(), "thread-terminal", []string{
				rolloutSessionMetaJSON("Codex Desktop"),
				rolloutLifecycleJSON("task_started"),
				rolloutLifecycleJSON(terminal),
			})
			active, err := rolloutHasActiveTask(path)
			if err != nil {
				t.Fatalf("read terminal rollout: %v", err)
			}
			if active {
				t.Fatalf("expected %s to close the active task", terminal)
			}
		})
	}
}

func writeWorkingTaskRollout(t *testing.T, dir, threadID string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, threadID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

func rolloutLifecycleJSON(kind string) string {
	return `{"type":"event_msg","payload":{"type":"` + kind + `","turn_id":"turn-1"}}`
}

func rolloutSessionMetaJSON(originator string) string {
	return `{"type":"session_meta","payload":{"originator":"` + originator + `"}}`
}
