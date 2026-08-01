package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

type workingTaskSummary struct {
	WorkspaceKey string
	TaskKey      string
	TaskTitle    string
	Status       state.QueueItemStatus
	StatusText   string
	QueueOrder   int
	LastUsedAt   time.Time
	Disabled     bool
}

func (s *Service) tasksTerminalPageEvent(surface *state.SurfaceConsoleRecord, action control.Action) eventcontract.Event {
	inline := action.Inbound != nil && strings.TrimSpace(action.Inbound.CardDaemonLifecycleID) != ""
	return s.openTaskBrowser(surface, action.MessageID, inline, false)
}

func workingTaskKey(item *state.QueueItemRecord) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(queuedItemExecutionThreadID(item), strings.TrimSpace(item.ID))
}

func workingTaskStatusLabel(status state.QueueItemStatus, queueOrder int) string {
	switch status {
	case state.QueueItemQueued:
		if queueOrder > 0 {
			return fmt.Sprintf("排队中（第 %d 位）", queueOrder)
		}
		return "排队中"
	case state.QueueItemDispatching:
		return "正在派发"
	case state.QueueItemRunning:
		return "执行中"
	case state.QueueItemSteering:
		return "正在追加指令"
	case state.QueueItemSteered:
		return "已追加指令"
	default:
		if value := strings.TrimSpace(string(status)); value != "" {
			return value
		}
		return "处理中"
	}
}
