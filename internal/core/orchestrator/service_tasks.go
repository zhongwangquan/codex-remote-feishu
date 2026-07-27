package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

type workingTaskSummary struct {
	WorkspaceKey string
	TaskTitle    string
	Status       state.QueueItemStatus
	QueueOrder   int
}

func (s *Service) tasksTerminalPageEvent(surface *state.SurfaceConsoleRecord) eventcontract.Event {
	tasks := s.workingTaskSummaries()
	sections := make([]control.FeishuCardTextSection, 0, len(tasks)+1)
	if len(tasks) == 0 {
		sections = append(sections, control.FeishuCardTextSection{
			Lines: []string{"当前没有正在执行或排队中的任务。"},
		})
	} else {
		sections = append(sections, control.FeishuCardTextSection{
			Lines: []string{fmt.Sprintf("共 %d 个正在工作或等待执行的任务。", len(tasks))},
		})
		for _, task := range tasks {
			sections = append(sections, control.FeishuCardTextSection{
				Label: workspaceSelectionLabel(task.WorkspaceKey),
				Lines: []string{
					"状态：" + workingTaskStatusLabel(task.Status, task.QueueOrder),
					"任务：" + firstNonEmpty(strings.TrimSpace(task.TaskTitle), "未命名任务"),
				},
			})
		}
	}
	page := control.NormalizeFeishuPageView(control.FeishuPageView{
		PageID:         control.FeishuCommandTasks,
		CommandID:      control.FeishuCommandTasks,
		Title:          "工作任务",
		ThemeKey:       "info",
		BodySections:   sections,
		Phase:          frontstagecontract.PhaseSucceeded,
		ActionPolicy:   frontstagecontract.ActionPolicyReadOnly,
		Sealed:         true,
		Interactive:    false,
		RelatedButtons: nil,
	})
	if flow := s.markCommandLauncherTerminal(surface); flow != nil {
		page.TrackingKey = strings.TrimSpace(flow.FlowID)
	}
	return s.pageEvent(surface, control.NormalizeFeishuPageView(page))
}

func (s *Service) workingTaskSummaries() []workingTaskSummary {
	tasks := []workingTaskSummary{}
	for _, surface := range s.root.Surfaces {
		if surface == nil {
			continue
		}
		workspaceKey := s.workingTaskWorkspaceKey(surface)
		if item := surface.QueueItems[surface.ActiveQueueItemID]; item != nil {
			tasks = append(tasks, workingTaskSummary{
				WorkspaceKey: workspaceKey,
				TaskTitle:    workingTaskTitle(item),
				Status:       item.Status,
			})
		}
		for index, queueItemID := range surface.QueuedQueueItemIDs {
			item := surface.QueueItems[queueItemID]
			if item == nil {
				continue
			}
			tasks = append(tasks, workingTaskSummary{
				WorkspaceKey: workspaceKey,
				TaskTitle:    workingTaskTitle(item),
				Status:       state.QueueItemQueued,
				QueueOrder:   index + 1,
			})
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Status == state.QueueItemQueued && tasks[j].Status != state.QueueItemQueued {
			return false
		}
		if tasks[i].Status != state.QueueItemQueued && tasks[j].Status == state.QueueItemQueued {
			return true
		}
		if tasks[i].WorkspaceKey == tasks[j].WorkspaceKey {
			return tasks[i].QueueOrder < tasks[j].QueueOrder
		}
		return tasks[i].WorkspaceKey < tasks[j].WorkspaceKey
	})
	return tasks
}

func (s *Service) workingTaskWorkspaceKey(surface *state.SurfaceConsoleRecord) string {
	if surface == nil {
		return ""
	}
	if inst := s.root.Instances[surface.AttachedInstanceID]; inst != nil {
		return firstNonEmpty(strings.TrimSpace(inst.WorkspaceKey), strings.TrimSpace(inst.WorkspaceRoot))
	}
	return firstNonEmpty(strings.TrimSpace(surface.ClaimedWorkspaceKey), "未关联工作区")
}

func workingTaskTitle(item *state.QueueItemRecord) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(item.SourceMessagePreview),
		strings.TrimSpace(item.ReplyToMessagePreview),
		workingTaskSourceLabel(item.SourceKind),
	)
}

func workingTaskSourceLabel(kind state.QueueItemSourceKind) string {
	switch kind {
	case state.QueueItemSourceAutoWhip:
		return "AutoWhip 自动任务"
	case state.QueueItemSourceAutoContinue:
		return "自动继续任务"
	default:
		return ""
	}
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
