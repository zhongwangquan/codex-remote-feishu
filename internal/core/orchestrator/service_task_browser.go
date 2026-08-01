package orchestrator

import (
	"sort"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
	"github.com/kxn/codex-remote-feishu/internal/core/threadtitle"
)

const (
	taskBrowserCacheTTL                = 10 * time.Minute
	taskBrowserRecentWindow            = 24 * time.Hour
	taskBrowserMaxTasks                = 30
	taskBrowserMaxTasksPerWorkspace    = 8
	taskConversationPageSize           = 2
	taskConversationMaxMessageRunes    = 800
	taskConversationMaxMessagesPerTurn = 4
)

func (s *Service) openTaskBrowser(surface *state.SurfaceConsoleRecord, sourceMessageID string, inline, forceRefresh bool) eventcontract.Event {
	if surface != nil {
		s.clearTargetPickerRuntime(surface)
		s.clearThreadHistoryRuntime(surface)
		s.clearWorkspacePageRuntime(surface)
	}
	cache := s.ensureTaskBrowserCache(surface, forceRefresh)
	now := s.now()
	flow := newOwnerCardFlowRecord(ownerCardFlowKindThreadHistory, s.pickers.nextThreadHistoryToken(), firstNonEmpty(surface.ActorUserID), now, taskBrowserCacheTTL, ownerCardFlowPhaseResolved)
	if sourceMessageID = strings.TrimSpace(sourceMessageID); sourceMessageID != "" {
		flow.MessageID = sourceMessageID
	}
	record := &activeThreadHistoryRecord{
		TaskBrowser: true,
		ViewMode:    control.FeishuThreadHistoryViewTasks,
	}
	s.setActiveOwnerCardFlow(surface, flow)
	s.setActiveThreadHistory(surface, record)
	return s.threadHistoryViewEvent(surface, s.buildTaskListView(flow, cache), inline, sourceMessageID)
}

func (s *Service) ensureTaskBrowserCache(surface *state.SurfaceConsoleRecord, force bool) *taskBrowserCacheRecord {
	now := s.now()
	current := s.taskBrowserCache(surface)
	if !force && current != nil && (current.ExpiresAt.IsZero() || current.ExpiresAt.After(now)) {
		return current
	}
	histories := map[string]agentproto.ThreadHistoryRecord{}
	if current != nil {
		for threadID, history := range current.Histories {
			histories[threadID] = cloneThreadHistoryRecord(history)
		}
	}
	cache := &taskBrowserCacheRecord{
		Tasks:     s.recentTaskSummaries(surface),
		Histories: histories,
		LoadedAt:  now,
		ExpiresAt: now.Add(taskBrowserCacheTTL),
	}
	s.setTaskBrowserCache(surface, cache)
	return cache
}

func (s *Service) recentTaskSummaries(surface *state.SurfaceConsoleRecord) []workingTaskSummary {
	views := s.mergedThreadViews(surface)
	queueStates := s.taskQueueStates()
	tasks := make([]workingTaskSummary, 0, min(len(views), taskBrowserMaxTasks))
	cutoff := s.now().Add(-taskBrowserRecentWindow)
	for _, view := range views {
		if view == nil || view.Thread == nil || strings.TrimSpace(view.ThreadID) == "" {
			continue
		}
		usedAt := threadLastUsedAt(view)
		queued, hasQueueState := queueStates[view.ThreadID]
		active := threadRuntimeActive(view.Thread)
		if usedAt.IsZero() && !hasQueueState && !active {
			continue
		}
		if !usedAt.IsZero() && usedAt.Before(cutoff) && !hasQueueState && !active {
			continue
		}
		workspaceKey := firstNonEmpty(mergedThreadWorkspaceClaimKey(view), state.ResolveWorkspaceKey(view.Thread.CWD), "未关联工作区")
		status, disabled := s.threadSelectionStatus(surface, view, true)
		statusText := "已完成"
		if hasQueueState {
			statusText = workingTaskStatusLabel(queued.Status, queued.QueueOrder)
		} else if active {
			statusText = "执行中"
		} else if disabled {
			statusText = firstNonEmpty(status, "暂不可接管")
		}
		tasks = append(tasks, workingTaskSummary{
			WorkspaceKey: workspaceKey,
			TaskKey:      strings.TrimSpace(view.ThreadID),
			TaskTitle:    firstNonEmpty(threadtitle.DisplayBody(view.Thread, threadtitle.DefaultDisplayLimit), "未命名任务"),
			StatusText:   statusText,
			LastUsedAt:   usedAt,
			Disabled:     disabled,
		})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].LastUsedAt.Equal(tasks[j].LastUsedAt) {
			return tasks[i].LastUsedAt.After(tasks[j].LastUsedAt)
		}
		return tasks[i].TaskKey < tasks[j].TaskKey
	})
	return tasks
}

func (s *Service) taskQueueStates() map[string]workingTaskSummary {
	states := map[string]workingTaskSummary{}
	for _, surface := range s.root.Surfaces {
		if surface == nil {
			continue
		}
		if item := surface.QueueItems[surface.ActiveQueueItemID]; item != nil {
			threadID := workingTaskKey(item)
			if threadID != "" {
				states[threadID] = workingTaskSummary{Status: item.Status}
			}
		}
		for index, itemID := range surface.QueuedQueueItemIDs {
			item := surface.QueueItems[itemID]
			threadID := workingTaskKey(item)
			if threadID == "" {
				continue
			}
			if _, exists := states[threadID]; exists {
				continue
			}
			states[threadID] = workingTaskSummary{Status: state.QueueItemQueued, QueueOrder: index + 1}
		}
	}
	return states
}

func (s *Service) buildTaskListView(flow *activeOwnerCardFlowRecord, cache *taskBrowserCacheRecord) control.FeishuThreadHistoryView {
	view := control.FeishuThreadHistoryView{
		PickerID:  strings.TrimSpace(flow.FlowID),
		MessageID: strings.TrimSpace(flow.MessageID),
		Mode:      control.FeishuThreadHistoryViewTasks,
		Title:     "Codex 工作区",
		CreatedAt: flow.CreatedAt,
		ExpiresAt: flow.ExpiresAt,
	}
	if cache == nil || len(cache.Tasks) == 0 {
		view.Hint = "最近 24 小时没有可展示的任务。"
		return view
	}
	view.TaskGroups = buildFeishuTaskGroups(cache.Tasks)
	visible := 0
	for _, group := range view.TaskGroups {
		visible += len(group.Tasks)
	}
	if hidden := len(cache.Tasks) - visible; hidden > 0 {
		view.Hint = "还有更多较早任务未展开，可点击刷新后查看最新列表。"
	}
	return view
}

func buildFeishuTaskGroups(tasks []workingTaskSummary) []control.FeishuTaskWorkspaceGroup {
	groups := make([]control.FeishuTaskWorkspaceGroup, 0)
	indexes := map[string]int{}
	visible := 0
	for _, task := range tasks {
		if visible >= taskBrowserMaxTasks {
			break
		}
		workspaceKey := firstNonEmpty(strings.TrimSpace(task.WorkspaceKey), "未关联工作区")
		index, ok := indexes[workspaceKey]
		if !ok {
			index = len(groups)
			indexes[workspaceKey] = index
			groups = append(groups, control.FeishuTaskWorkspaceGroup{WorkspaceLabel: previewSnippet(workspaceSelectionLabel(workspaceKey))})
		}
		if len(groups[index].Tasks) >= taskBrowserMaxTasksPerWorkspace {
			continue
		}
		groups[index].Tasks = append(groups[index].Tasks, control.FeishuTaskOption{
			ThreadID: task.TaskKey,
			Title:    firstNonEmpty(strings.TrimSpace(task.TaskTitle), "未命名任务"),
			Status:   firstNonEmpty(strings.TrimSpace(task.StatusText), "已完成"),
			AgeText:  humanizeTaskAge(task.LastUsedAt),
			Disabled: task.Disabled,
		})
		visible++
	}
	return groups
}

func humanizeTaskAge(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("01-02 15:04")
}

func (s *Service) requireActiveTaskBrowser(surface *state.SurfaceConsoleRecord, pickerID, actorUserID string) (*activeOwnerCardFlowRecord, *activeThreadHistoryRecord, []eventcontract.Event) {
	flow, blocked := s.requireActiveOwnerCardFlow(surface, ownerCardFlowKindThreadHistory, pickerID, actorUserID, "这张任务卡片已失效，请重新发送 /tasks。", "这张任务卡片只允许发起者本人操作。")
	if blocked != nil {
		return nil, nil, blocked
	}
	record := s.activeThreadHistory(surface)
	if record == nil || !record.TaskBrowser {
		s.clearThreadHistoryRuntime(surface)
		return nil, nil, notice(surface, "tasks_expired", "这张任务卡片已失效，请重新发送 /tasks。")
	}
	return flow, record, nil
}

func (s *Service) handleTaskList(surface *state.SurfaceConsoleRecord, pickerID, actorUserID, sourceMessageID string, inline bool) []eventcontract.Event {
	flow, record, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	record.ViewMode = control.FeishuThreadHistoryViewTasks
	record.ThreadID = ""
	record.Page = 0
	if surface.PendingHeadless == nil || strings.TrimSpace(surface.PendingHeadless.ThreadID) != strings.TrimSpace(record.PendingThreadID) {
		record.PendingThreadID = ""
	}
	record.PendingReply = ""
	record.PendingQueueItem = ""
	refreshOwnerCardFlow(flow, ownerCardFlowPhaseResolved, s.now(), taskBrowserCacheTTL)
	if inline && strings.TrimSpace(sourceMessageID) != "" {
		flow.MessageID = strings.TrimSpace(sourceMessageID)
	}
	return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskListView(flow, s.ensureTaskBrowserCache(surface, false)), inline, sourceMessageID)}
}

func (s *Service) handleTaskHome(surface *state.SurfaceConsoleRecord, pickerID, actorUserID, sourceMessageID string) []eventcontract.Event {
	_, _, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	s.clearThreadHistoryRuntime(surface)
	return []eventcontract.Event{s.menuPageEvent(surface, "", sourceMessageID)}
}

func (s *Service) handleTaskRefresh(surface *state.SurfaceConsoleRecord, pickerID, threadID, actorUserID, sourceMessageID string, inline bool) []eventcontract.Event {
	flow, record, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || record.ViewMode == control.FeishuThreadHistoryViewTasks {
		cache := s.ensureTaskBrowserCache(surface, true)
		record.ViewMode = control.FeishuThreadHistoryViewTasks
		refreshOwnerCardFlow(flow, ownerCardFlowPhaseResolved, s.now(), taskBrowserCacheTTL)
		return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskListView(flow, cache), inline, sourceMessageID)}
	}
	if threadID != strings.TrimSpace(record.ThreadID) {
		return notice(surface, "tasks_thread_changed", "当前任务已切换，请在最新任务详情里刷新。")
	}
	if cache := s.ensureTaskBrowserCache(surface, false); cache != nil {
		delete(cache.Histories, threadID)
	}
	return s.startTaskConversationQuery(surface, flow, record, sourceMessageID, inline)
}

func (s *Service) handleTaskPage(surface *state.SurfaceConsoleRecord, pickerID, threadID string, page int, actorUserID, sourceMessageID string, inline bool) []eventcontract.Event {
	flow, record, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	threadID = strings.TrimSpace(threadID)
	if record.ViewMode != control.FeishuThreadHistoryViewConversation || threadID == "" || threadID != strings.TrimSpace(record.ThreadID) {
		return notice(surface, "tasks_thread_changed", "当前任务已切换，请在最新任务详情里翻页。")
	}
	if _, ok := s.cachedTaskHistory(surface, threadID); !ok {
		return s.startTaskConversationQuery(surface, flow, record, sourceMessageID, inline)
	}
	record.Page = page
	refreshOwnerCardFlow(flow, ownerCardFlowPhaseResolved, s.now(), taskBrowserCacheTTL)
	return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationView(surface, flow, record), inline, sourceMessageID)}
}

func (s *Service) handleTaskOpen(surface *state.SurfaceConsoleRecord, pickerID, threadID, actorUserID, sourceMessageID string, inline bool) []eventcontract.Event {
	flow, record, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return notice(surface, "tasks_thread_missing", "目标任务不存在，请返回任务列表后重试。")
	}
	record.ViewMode = control.FeishuThreadHistoryViewConversation
	record.ThreadID = threadID
	record.Page = 0
	record.PendingThreadID = threadID
	record.PendingReply = ""
	record.PendingQueueItem = ""
	refreshOwnerCardFlow(flow, ownerCardFlowPhaseRunning, s.now(), taskBrowserCacheTTL)
	events := s.useThreadWithOverlayCleanup(surface, threadID, true, surfaceOverlayRouteCleanupOptions{PreserveThreadHistory: true})
	filtered := targetPickerFilteredFollowupEvents(events)
	if taskBrowserThreadReady(surface, threadID) {
		record.PendingThreadID = ""
		if _, ok := s.cachedTaskHistory(surface, threadID); ok {
			flow.Phase = ownerCardFlowPhaseResolved
			bumpOwnerCardFlowRevision(flow)
			return append([]eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationView(surface, flow, record), inline, sourceMessageID)}, filtered...)
		}
		return append(s.startTaskConversationQuery(surface, flow, record, sourceMessageID, inline), filtered...)
	}
	if taskBrowserPendingStillRunning(surface, record) {
		loading := s.buildTaskConversationLoadingView(surface, flow, record, "正在恢复这个任务并读取对话...")
		return append([]eventcontract.Event{s.threadHistoryViewEvent(surface, loading, inline, sourceMessageID)}, filtered...)
	}
	text := firstNonEmpty(targetPickerFirstNoticeText(events), "这个任务暂时无法打开，请返回任务列表后重试。")
	record.PendingThreadID = ""
	flow.Phase = ownerCardFlowPhaseError
	bumpOwnerCardFlowRevision(flow)
	return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationErrorView(surface, flow, record, text), inline, sourceMessageID)}
}

func (s *Service) startTaskConversationQuery(surface *state.SurfaceConsoleRecord, flow *activeOwnerCardFlowRecord, record *activeThreadHistoryRecord, sourceMessageID string, inline bool) []eventcontract.Event {
	inst, threadID, _, text := s.currentThreadHistoryTarget(surface)
	if inst == nil || threadID == "" || threadID != strings.TrimSpace(record.ThreadID) {
		return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationErrorView(surface, flow, record, firstNonEmpty(text, "这个任务暂时无法读取。")), inline, sourceMessageID)}
	}
	record.PendingThreadID = ""
	flow.Phase = ownerCardFlowPhaseLoading
	bumpOwnerCardFlowRevision(flow)
	loading := s.buildTaskConversationLoadingView(surface, flow, record, "正在读取完整对话...")
	return []eventcontract.Event{
		s.threadHistoryViewEvent(surface, loading, inline, sourceMessageID),
		taskConversationHistoryReadEvent(surface, inst, record.ThreadID, sourceMessageID),
	}
}

func taskConversationHistoryReadEvent(surface *state.SurfaceConsoleRecord, inst *state.InstanceRecord, threadID, sourceMessageID string) eventcontract.Event {
	return eventcontract.Event{
		Kind:             eventcontract.KindDaemonCommand,
		GatewayID:        surface.GatewayID,
		SurfaceSessionID: surface.SurfaceSessionID,
		SourceMessageID:  sourceMessageID,
		DaemonCommand: &control.DaemonCommand{
			Kind:             control.DaemonCommandThreadHistoryRead,
			GatewayID:        surface.GatewayID,
			SurfaceSessionID: surface.SurfaceSessionID,
			SourceMessageID:  sourceMessageID,
			InstanceID:       inst.InstanceID,
			ThreadID:         strings.TrimSpace(threadID),
		},
	}
}

func (s *Service) buildTaskConversationLoadingView(surface *state.SurfaceConsoleRecord, flow *activeOwnerCardFlowRecord, record *activeThreadHistoryRecord, text string) control.FeishuThreadHistoryView {
	view := s.taskConversationBaseView(surface, flow, record)
	view.Loading = true
	view.LoadingText = strings.TrimSpace(text)
	view.NoticeSections = []control.FeishuCardTextSection{{Lines: []string{view.LoadingText}}}
	return view
}

func (s *Service) buildTaskConversationErrorView(surface *state.SurfaceConsoleRecord, flow *activeOwnerCardFlowRecord, record *activeThreadHistoryRecord, text string) control.FeishuThreadHistoryView {
	view := s.taskConversationBaseView(surface, flow, record)
	view.NoticeCode = "tasks_unavailable"
	view.NoticeText = strings.TrimSpace(text)
	view.NoticeSections = []control.FeishuCardTextSection{{Label: "暂时无法打开", Lines: []string{view.NoticeText}}}
	return view
}

func (s *Service) taskConversationBaseView(surface *state.SurfaceConsoleRecord, flow *activeOwnerCardFlowRecord, record *activeThreadHistoryRecord) control.FeishuThreadHistoryView {
	task := s.cachedTaskSummary(surface, record.ThreadID)
	title := firstNonEmpty(task.TaskTitle, record.ThreadID, "任务详情")
	status := s.currentTaskStatus(surface, record.ThreadID, task.StatusText)
	return control.FeishuThreadHistoryView{
		PickerID:       strings.TrimSpace(flow.FlowID),
		MessageID:      strings.TrimSpace(flow.MessageID),
		Mode:           control.FeishuThreadHistoryViewConversation,
		Title:          title + " · " + status,
		ThreadID:       strings.TrimSpace(record.ThreadID),
		ThreadLabel:    title,
		ThreadStatus:   status,
		WorkspaceLabel: previewSnippet(workspaceSelectionLabel(task.WorkspaceKey)),
		Page:           record.Page,
		PendingReply:   record.PendingReply,
		CreatedAt:      flow.CreatedAt,
		ExpiresAt:      flow.ExpiresAt,
	}
}

func (s *Service) buildTaskConversationView(surface *state.SurfaceConsoleRecord, flow *activeOwnerCardFlowRecord, record *activeThreadHistoryRecord) control.FeishuThreadHistoryView {
	view := s.taskConversationBaseView(surface, flow, record)
	history, ok := s.cachedTaskHistory(surface, record.ThreadID)
	if !ok {
		view.NoticeText = "还没有读取到这个任务的会话内容。"
		return view
	}
	turns := taskConversationTurns(history)
	page, pages, start, end := paginateTaskConversation(record.Page, len(turns))
	record.Page = page
	view.Page = page
	view.TotalPages = pages
	view.PageStart = start
	view.PageEnd = end
	view.TurnCount = len(turns)
	view.Conversation = append([]control.FeishuConversationTurn(nil), turns[start:end]...)
	view.CanReply = taskBrowserThreadReady(surface, record.ThreadID) && surface.PendingHeadless == nil
	if len(turns) == 0 && strings.TrimSpace(record.PendingReply) == "" {
		view.Hint = "这个任务暂时还没有可展示的对话。"
	}
	return view
}

func taskConversationTurns(history agentproto.ThreadHistoryRecord) []control.FeishuConversationTurn {
	turns := make([]control.FeishuConversationTurn, 0, len(history.Turns))
	for _, turn := range history.Turns {
		view := control.FeishuConversationTurn{
			TurnID:    strings.TrimSpace(turn.TurnID),
			Status:    humanizeTaskTurnStatus(turn.Status),
			ErrorText: truncateTaskConversationText(turn.ErrorMessage),
		}
		if !turn.CompletedAt.IsZero() {
			view.UpdatedText = turn.CompletedAt.Local().Format("01-02 15:04")
		} else if !turn.StartedAt.IsZero() {
			view.UpdatedText = turn.StartedAt.Local().Format("01-02 15:04")
		}
		for _, item := range turn.Items {
			text := truncateTaskConversationText(item.Text)
			if text == "" {
				continue
			}
			role := ""
			switch strings.TrimSpace(item.Kind) {
			case "user_message":
				role = "user"
			case "agent_message":
				role = "assistant"
			default:
				continue
			}
			if len(view.Messages) >= taskConversationMaxMessagesPerTurn {
				break
			}
			view.Messages = append(view.Messages, control.FeishuConversationMessage{Role: role, Text: text})
		}
		if len(view.Messages) == 0 && view.ErrorText == "" {
			continue
		}
		turns = append(turns, view)
	}
	return turns
}

func humanizeTaskTurnStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "succeeded", "success":
		return "已完成"
	case "running", "in_progress", "started":
		return "执行中"
	case "failed", "error":
		return "失败"
	case "cancelled", "canceled", "interrupted", "stopped":
		return "已停止"
	default:
		return strings.TrimSpace(status)
	}
}

func truncateTaskConversationText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= taskConversationMaxMessageRunes {
		return text
	}
	return string(runes[:taskConversationMaxMessageRunes-3]) + "..."
}

func paginateTaskConversation(page, total int) (clamped, pages, start, end int) {
	if total <= 0 {
		return 0, 1, 0, 0
	}
	pages = (total + taskConversationPageSize - 1) / taskConversationPageSize
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	end = total - page*taskConversationPageSize
	start = end - taskConversationPageSize
	if start < 0 {
		start = 0
	}
	return page, pages, start, end
}

func (s *Service) cachedTaskSummary(surface *state.SurfaceConsoleRecord, threadID string) workingTaskSummary {
	cache := s.ensureTaskBrowserCache(surface, false)
	for _, task := range cache.Tasks {
		if strings.TrimSpace(task.TaskKey) == strings.TrimSpace(threadID) {
			return task
		}
	}
	if view := s.mergedThreadView(surface, threadID); view != nil && view.Thread != nil {
		return workingTaskSummary{
			WorkspaceKey: mergedThreadWorkspaceClaimKey(view),
			TaskKey:      threadID,
			TaskTitle:    threadtitle.DisplayBody(view.Thread, threadtitle.DefaultDisplayLimit),
			StatusText:   "已完成",
		}
	}
	return workingTaskSummary{TaskKey: threadID, TaskTitle: threadID, StatusText: "处理中"}
}

func (s *Service) cachedTaskHistory(surface *state.SurfaceConsoleRecord, threadID string) (agentproto.ThreadHistoryRecord, bool) {
	cache := s.taskBrowserCache(surface)
	if cache == nil || cache.Histories == nil {
		return agentproto.ThreadHistoryRecord{}, false
	}
	history, ok := cache.Histories[strings.TrimSpace(threadID)]
	if !ok {
		return agentproto.ThreadHistoryRecord{}, false
	}
	return cloneThreadHistoryRecord(history), true
}

func (s *Service) cacheTaskHistory(surface *state.SurfaceConsoleRecord, history agentproto.ThreadHistoryRecord) {
	threadID := strings.TrimSpace(history.Thread.ThreadID)
	if threadID == "" {
		return
	}
	cache := s.ensureTaskBrowserCache(surface, false)
	if cache.Histories == nil {
		cache.Histories = map[string]agentproto.ThreadHistoryRecord{}
	}
	cache.Histories[threadID] = cloneThreadHistoryRecord(history)
	cache.ExpiresAt = s.now().Add(taskBrowserCacheTTL)
}

func (s *Service) currentTaskStatus(surface *state.SurfaceConsoleRecord, threadID, fallback string) string {
	if surface != nil {
		if item := surface.QueueItems[surface.ActiveQueueItemID]; item != nil && workingTaskKey(item) == threadID {
			return workingTaskStatusLabel(item.Status, 0)
		}
		for index, itemID := range surface.QueuedQueueItemIDs {
			if item := surface.QueueItems[itemID]; item != nil && workingTaskKey(item) == threadID {
				return workingTaskStatusLabel(state.QueueItemQueued, index+1)
			}
		}
		if inst := s.root.Instances[surface.AttachedInstanceID]; inst != nil {
			if thread := inst.Threads[threadID]; threadRuntimeActive(thread) {
				return "执行中"
			}
		}
	}
	return firstNonEmpty(strings.TrimSpace(fallback), "已完成")
}

func taskBrowserThreadReady(surface *state.SurfaceConsoleRecord, threadID string) bool {
	return surface != nil && strings.TrimSpace(surface.SelectedThreadID) == strings.TrimSpace(threadID) && strings.TrimSpace(surface.AttachedInstanceID) != ""
}

func taskBrowserPendingStillRunning(surface *state.SurfaceConsoleRecord, record *activeThreadHistoryRecord) bool {
	return surface != nil && record != nil && record.TaskBrowser && strings.TrimSpace(record.PendingThreadID) != "" && surface.PendingHeadless != nil && strings.TrimSpace(surface.PendingHeadless.ThreadID) == strings.TrimSpace(record.PendingThreadID)
}

func (s *Service) maybeFinalizePendingTaskBrowser(surface *state.SurfaceConsoleRecord, events []eventcontract.Event, fallbackFailureText string) []eventcontract.Event {
	flow := s.activeOwnerCardFlow(surface)
	record := s.activeThreadHistory(surface)
	if flow == nil || flow.Kind != ownerCardFlowKindThreadHistory || record == nil || !record.TaskBrowser || strings.TrimSpace(record.PendingThreadID) == "" {
		return events
	}
	filtered := targetPickerFilteredFollowupEvents(events)
	if taskBrowserThreadReady(surface, record.PendingThreadID) {
		readyThreadID := record.PendingThreadID
		record.PendingThreadID = ""
		if record.ViewMode == control.FeishuThreadHistoryViewTasks {
			record.ThreadID = ""
			flow.Phase = ownerCardFlowPhaseResolved
			bumpOwnerCardFlowRevision(flow)
			return append([]eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskListView(flow, s.ensureTaskBrowserCache(surface, false)), false, "")}, filtered...)
		}
		record.ThreadID = readyThreadID
		if _, ok := s.cachedTaskHistory(surface, record.ThreadID); ok {
			flow.Phase = ownerCardFlowPhaseResolved
			bumpOwnerCardFlowRevision(flow)
			return append([]eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationView(surface, flow, record), false, "")}, filtered...)
		}
		return append(s.startTaskConversationQuery(surface, flow, record, "", false), filtered...)
	}
	if taskBrowserPendingStillRunning(surface, record) {
		return filtered
	}
	record.PendingThreadID = ""
	if record.ViewMode == control.FeishuThreadHistoryViewTasks {
		flow.Phase = ownerCardFlowPhaseResolved
		bumpOwnerCardFlowRevision(flow)
		return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskListView(flow, s.ensureTaskBrowserCache(surface, false)), false, "")}
	}
	flow.Phase = ownerCardFlowPhaseError
	bumpOwnerCardFlowRevision(flow)
	text := firstNonEmpty(strings.TrimSpace(fallbackFailureText), targetPickerFirstNoticeText(events), "这个任务暂时无法打开，请返回任务列表后重试。")
	return []eventcontract.Event{s.threadHistoryViewEvent(surface, s.buildTaskConversationErrorView(surface, flow, record, text), false, "")}
}

func (s *Service) handleTaskReply(surface *state.SurfaceConsoleRecord, pickerID, threadID, text, actorUserID, sourceMessageID string, inline bool) []eventcontract.Event {
	flow, record, blocked := s.requireActiveTaskBrowser(surface, pickerID, actorUserID)
	if blocked != nil {
		return blocked
	}
	threadID = strings.TrimSpace(threadID)
	text = strings.TrimSpace(text)
	if text == "" {
		return notice(surface, "tasks_reply_empty", "请输入回复内容后再发送。")
	}
	if record.ViewMode != control.FeishuThreadHistoryViewConversation || threadID == "" || threadID != strings.TrimSpace(record.ThreadID) || !taskBrowserThreadReady(surface, threadID) {
		return notice(surface, "tasks_thread_changed", "当前任务目标已经变化，请返回任务列表重新打开后再回复。")
	}
	events := s.handleText(surface, control.Action{
		Kind:             control.ActionTextMessage,
		GatewayID:        surface.GatewayID,
		SurfaceSessionID: surface.SurfaceSessionID,
		ChatID:           surface.ChatID,
		ActorUserID:      actorUserID,
		MessageID:        sourceMessageID,
		Text:             text,
	})
	queueItemID := taskReplyQueueItemID(surface, sourceMessageID, text)
	if queueItemID == "" {
		return events
	}
	record.PendingReply = text
	record.PendingQueueItem = queueItemID
	flow.Phase = ownerCardFlowPhaseRunning
	bumpOwnerCardFlowRevision(flow)
	view := s.buildTaskConversationView(surface, flow, record)
	return append([]eventcontract.Event{s.threadHistoryViewEvent(surface, view, inline, sourceMessageID)}, targetPickerFilteredFollowupEvents(events)...)
}

func taskReplyQueueItemID(surface *state.SurfaceConsoleRecord, sourceMessageID, text string) string {
	if surface == nil {
		return ""
	}
	for itemID, item := range surface.QueueItems {
		if item == nil || strings.TrimSpace(item.SourceMessageID) != strings.TrimSpace(sourceMessageID) || strings.TrimSpace(item.SourceMessagePreview) != strings.TrimSpace(text) {
			continue
		}
		switch item.Status {
		case state.QueueItemQueued, state.QueueItemDispatching, state.QueueItemRunning, state.QueueItemSteering, state.QueueItemSteered:
			return itemID
		}
	}
	return ""
}

func (s *Service) taskBrowserTurnCompletedEvents(instanceID, threadID string) []eventcontract.Event {
	inst := s.root.Instances[strings.TrimSpace(instanceID)]
	if inst == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	var events []eventcontract.Event
	for _, surface := range s.findAttachedSurfaces(instanceID) {
		flow := s.activeOwnerCardFlow(surface)
		record := s.activeThreadHistory(surface)
		if flow == nil || flow.Kind != ownerCardFlowKindThreadHistory || record == nil || !record.TaskBrowser || record.ViewMode != control.FeishuThreadHistoryViewConversation || strings.TrimSpace(record.ThreadID) != strings.TrimSpace(threadID) {
			continue
		}
		record.PendingReply = ""
		record.PendingQueueItem = ""
		events = append(events, taskConversationHistoryReadEvent(surface, inst, threadID, ""))
	}
	return events
}
