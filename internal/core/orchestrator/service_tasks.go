package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
	"github.com/kxn/codex-remote-feishu/internal/core/threadcatalogcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/threadtitle"
)

const (
	workingTaskCardMaxTasks = 50
	workingTaskCatalogLimit = 200
)

type workingTaskOrigin string

const (
	workingTaskOriginFeishu       workingTaskOrigin = "feishu"
	workingTaskOriginCodexDesktop workingTaskOrigin = "codex_desktop"
	workingTaskOriginCodexCLI     workingTaskOrigin = "codex_cli"
	workingTaskOriginMultica      workingTaskOrigin = "multica"
)

type workingTaskSummary struct {
	WorkspaceKey string
	TaskKey      string
	TaskTitle    string
	Origin       workingTaskOrigin
	Status       state.QueueItemStatus
	QueueOrder   int
	Continuable  bool
}

type workingTaskWorkspaceGroup struct {
	WorkspaceKey string
	Tasks        []workingTaskSummary
}

func (s *Service) tasksTerminalPageEvent(surface *state.SurfaceConsoleRecord, action control.Action) eventcontract.Event {
	tasks := s.workingTaskSummaries()
	visibleTasks := tasks
	hiddenTaskCount := 0
	if len(visibleTasks) > workingTaskCardMaxTasks {
		hiddenTaskCount = len(visibleTasks) - workingTaskCardMaxTasks
		visibleTasks = visibleTasks[:workingTaskCardMaxTasks]
	}
	groups := groupWorkingTaskSummaries(visibleTasks)
	sections := make([]control.FeishuCardTextSection, 0, len(groups)+2)
	if len(tasks) == 0 {
		sections = append(sections, control.FeishuCardTextSection{
			Lines: []string{"当前没有执行中、排队或可继续的任务。"},
		})
	} else {
		sections = append(sections, control.FeishuCardTextSection{
			Lines: []string{fmt.Sprintf("共 %d 个执行中、排队或可继续的任务。", len(tasks))},
		})
		for _, group := range groups {
			lines := []string{previewSnippet(workspaceSelectionLabel(group.WorkspaceKey))}
			for _, task := range group.Tasks {
				lines = append(lines,
					"• "+firstNonEmpty(strings.TrimSpace(task.TaskTitle), "未命名任务"),
					"  来源："+workingTaskOriginLabel(task.Origin)+" · 状态："+workingTaskStatusLabel(task),
				)
			}
			sections = append(sections, control.FeishuCardTextSection{
				Label: "工作区",
				Lines: lines,
			})
		}
		if hiddenTaskCount > 0 {
			sections = append(sections, control.FeishuCardTextSection{
				Label: "更多任务",
				Lines: []string{fmt.Sprintf("另有 %d 个任务未展开。", hiddenTaskCount)},
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
	} else if action.LocalPageAction {
		if flow := s.completeWorkspacePageTerminal(surface, action.MessageID); flow != nil {
			page.TrackingKey = strings.TrimSpace(flow.FlowID)
		}
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
				TaskKey:      workingTaskKey(item),
				TaskTitle:    s.workingTaskTitle(surface, item),
				Origin:       workingTaskOriginFeishu,
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
				TaskKey:      workingTaskKey(item),
				TaskTitle:    s.workingTaskTitle(surface, item),
				Origin:       workingTaskOriginFeishu,
				Status:       state.QueueItemQueued,
				QueueOrder:   index + 1,
			})
		}
	}
	for _, persisted := range s.catalog.workingTasks(workingTaskCatalogLimit) {
		thread := persisted.Thread
		workspaceKey := firstNonEmpty(
			strings.TrimSpace(thread.WorkspaceKey),
			state.ResolveWorkspaceKey(thread.CWD),
			"未关联工作区",
		)
		tasks = append(tasks, workingTaskSummary{
			WorkspaceKey: workspaceKey,
			TaskKey:      strings.TrimSpace(thread.ThreadID),
			TaskTitle:    threadtitle.DisplayBody(&thread, threadtitle.DefaultDisplayLimit),
			Origin:       workingTaskPersistedOrigin(persisted.Source),
			Status:       state.QueueItemRunning,
			Continuable:  persisted.State == threadcatalogcontract.WorkingTaskStateContinuable,
		})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].WorkspaceKey != tasks[j].WorkspaceKey {
			return tasks[i].WorkspaceKey < tasks[j].WorkspaceKey
		}
		if tasks[i].Continuable != tasks[j].Continuable {
			return !tasks[i].Continuable
		}
		if tasks[i].Status == state.QueueItemQueued && tasks[j].Status != state.QueueItemQueued {
			return false
		}
		if tasks[i].Status != state.QueueItemQueued && tasks[j].Status == state.QueueItemQueued {
			return true
		}
		if tasks[i].QueueOrder != tasks[j].QueueOrder {
			return tasks[i].QueueOrder < tasks[j].QueueOrder
		}
		return tasks[i].TaskKey < tasks[j].TaskKey
	})
	return dedupeWorkingTaskSummaries(tasks)
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

func (s *Service) workingTaskTitle(surface *state.SurfaceConsoleRecord, item *state.QueueItemRecord) string {
	if item == nil {
		return ""
	}
	if surface != nil {
		if inst := s.root.Instances[surface.AttachedInstanceID]; inst != nil {
			if thread := inst.Threads[queuedItemExecutionThreadID(item)]; thread != nil {
				if title := threadtitle.DisplayBody(thread, threadtitle.DefaultDisplayLimit); title != threadtitle.UnnamedDisplayName {
					return title
				}
			}
		}
	}
	return firstNonEmpty(
		previewSnippet(item.SourceMessagePreview),
		previewSnippet(item.ReplyToMessagePreview),
		workingTaskFallbackTitle(item.SourceKind),
	)
}

func workingTaskKey(item *state.QueueItemRecord) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(queuedItemExecutionThreadID(item), strings.TrimSpace(item.ID))
}

func dedupeWorkingTaskSummaries(tasks []workingTaskSummary) []workingTaskSummary {
	if len(tasks) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tasks))
	out := make([]workingTaskSummary, 0, len(tasks))
	for _, task := range tasks {
		key := strings.TrimSpace(task.WorkspaceKey) + "\x00" + strings.TrimSpace(task.TaskKey)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, task)
	}
	return out
}

func groupWorkingTaskSummaries(tasks []workingTaskSummary) []workingTaskWorkspaceGroup {
	if len(tasks) == 0 {
		return nil
	}
	groups := make([]workingTaskWorkspaceGroup, 0, len(tasks))
	for _, task := range tasks {
		if len(groups) == 0 || groups[len(groups)-1].WorkspaceKey != task.WorkspaceKey {
			groups = append(groups, workingTaskWorkspaceGroup{WorkspaceKey: task.WorkspaceKey})
		}
		group := &groups[len(groups)-1]
		group.Tasks = append(group.Tasks, task)
	}
	return groups
}

func workingTaskFallbackTitle(kind state.QueueItemSourceKind) string {
	switch kind {
	case state.QueueItemSourceAutoWhip:
		return "AutoWhip 自动任务"
	case state.QueueItemSourceAutoContinue:
		return "自动继续任务"
	default:
		return ""
	}
}

func workingTaskPersistedOrigin(source threadcatalogcontract.WorkingTaskSource) workingTaskOrigin {
	switch source {
	case threadcatalogcontract.WorkingTaskSourceCodexCLI:
		return workingTaskOriginCodexCLI
	case threadcatalogcontract.WorkingTaskSourceMultica:
		return workingTaskOriginMultica
	default:
		return workingTaskOriginCodexDesktop
	}
}

func workingTaskOriginLabel(origin workingTaskOrigin) string {
	switch origin {
	case workingTaskOriginCodexCLI:
		return "Codex CLI"
	case workingTaskOriginMultica:
		return "Multica"
	case workingTaskOriginCodexDesktop:
		return "Codex Desktop"
	default:
		return "飞书"
	}
}

func workingTaskStatusLabel(task workingTaskSummary) string {
	if task.Continuable {
		return "可继续"
	}
	switch task.Status {
	case state.QueueItemQueued:
		if task.QueueOrder > 0 {
			return fmt.Sprintf("排队中（第 %d 位）", task.QueueOrder)
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
		if value := strings.TrimSpace(string(task.Status)); value != "" {
			return value
		}
		return "处理中"
	}
}
