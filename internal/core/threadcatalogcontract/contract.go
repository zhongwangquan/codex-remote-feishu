package threadcatalogcontract

import (
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

type PersistedThreadCatalog interface {
	RecentThreads(limit int) ([]state.ThreadRecord, error)
	RecentWorkspaces(limit int) (map[string]time.Time, error)
	ThreadByID(threadID string) (*state.ThreadRecord, error)
}

type BackendAwarePersistedThreadCatalog interface {
	RecentThreadsForBackend(agentproto.Backend, int) ([]state.ThreadRecord, error)
	RecentWorkspacesForBackend(agentproto.Backend, int) (map[string]time.Time, error)
	ThreadByIDForBackend(agentproto.Backend, string) (*state.ThreadRecord, error)
}

type WorkingTaskSource string

const (
	WorkingTaskSourceCodexDesktop WorkingTaskSource = "codex_desktop"
	WorkingTaskSourceCodexCLI     WorkingTaskSource = "codex_cli"
	WorkingTaskSourceMultica      WorkingTaskSource = "multica"
)

type WorkingTaskState string

const (
	WorkingTaskStateActive      WorkingTaskState = "active"
	WorkingTaskStateContinuable WorkingTaskState = "continuable"
)

type WorkingTaskRecord struct {
	Thread state.ThreadRecord
	Source WorkingTaskSource
	State  WorkingTaskState
}

// WorkingTaskCatalog is an optional extension implemented by persisted thread
// catalogs that can discover active local turns and unarchived Desktop tasks
// that remain available for continuation.
type WorkingTaskCatalog interface {
	WorkingTasks(limit int) ([]WorkingTaskRecord, error)
}
