package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestTasksCommandShowsRecentThreadTitlesGroupedByWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:   "inst-shop",
		WorkspaceKey: "/data/shop",
		ShortName:    "shop",
		Online:       true,
		Threads: map[string]*state.ThreadRecord{
			"thread-running": {
				ThreadID:   "thread-running",
				Name:       "分析电商趋势",
				CWD:        "/data/shop",
				Loaded:     true,
				LastUsedAt: now.Add(-time.Minute),
			},
			"thread-done": {
				ThreadID:   "thread-done",
				Name:       "整理趋势报告",
				CWD:        "/data/shop",
				Loaded:     true,
				LastUsedAt: now.Add(-2 * time.Minute),
			},
		},
	})
	surface := svc.ensureSurface(control.Action{
		SurfaceSessionID: "surface-shop",
		ChatID:           "chat-shop",
		ActorUserID:      "user-shop",
	})
	surface.AttachedInstanceID = "inst-shop"
	surface.RouteMode = state.RouteModePinned
	surface.SelectedThreadID = "thread-running"
	surface.ActiveQueueItemID = "queue-running"
	surface.QueueItems["queue-running"] = &state.QueueItemRecord{
		ID:                 "queue-running",
		FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-running"),
		Status:             state.QueueItemRunning,
	}

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTasks,
		SurfaceSessionID: "surface-shop",
		ChatID:           "chat-shop",
		ActorUserID:      "user-shop",
	})
	view := requireTaskHistoryView(t, events)
	if view.Mode != control.FeishuThreadHistoryViewTasks || len(view.TaskGroups) != 1 {
		t.Fatalf("unexpected tasks view: %#v", view)
	}
	if view.TaskGroups[0].WorkspaceLabel != "shop" {
		t.Fatalf("workspace label = %q, want shop", view.TaskGroups[0].WorkspaceLabel)
	}
	got := map[string]string{}
	for _, task := range view.TaskGroups[0].Tasks {
		got[task.Title] = task.Status
	}
	if got["分析电商趋势"] != "执行中" || got["整理趋势报告"] != "已完成" {
		t.Fatalf("recent task titles/statuses = %#v", got)
	}
	if summary := svc.SurfaceUIRuntimeSummary("surface-shop"); summary.ActiveOwnerCardFlowKind != string(ownerCardFlowKindThreadHistory) || !summary.TaskBrowserCacheLoaded {
		t.Fatalf("expected live task owner flow with cache, got %#v", summary)
	}
}

func TestTaskListBackUsesCacheUntilExplicitRefresh(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	catalog := &fakePersistedThreadCatalog{recent: []state.ThreadRecord{{
		ThreadID:     "thread-old",
		Name:         "缓存中的任务",
		WorkspaceKey: "/data/shop",
		CWD:          "/data/shop",
		LastUsedAt:   now.Add(-time.Minute),
	}}}
	svc.SetPersistedThreadCatalog(catalog)
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")

	initial := svc.ApplySurfaceAction(control.Action{Kind: control.ActionTasks, SurfaceSessionID: "surface-1", ActorUserID: "user-1"})
	initialView := requireTaskHistoryView(t, initial)
	pickerID := initialView.PickerID
	if !taskViewHasTitle(initialView, "缓存中的任务") {
		t.Fatalf("initial view missing cached title: %#v", initialView.TaskGroups)
	}

	catalog.recent = []state.ThreadRecord{{
		ThreadID:     "thread-new",
		Name:         "刷新后的任务",
		WorkspaceKey: "/data/shop",
		CWD:          "/data/shop",
		LastUsedAt:   now,
	}}
	back := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskList, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID,
	})
	backView := requireTaskHistoryView(t, back)
	if !taskViewHasTitle(backView, "缓存中的任务") || taskViewHasTitle(backView, "刷新后的任务") {
		t.Fatalf("back navigation should use cache, got %#v", backView.TaskGroups)
	}

	refreshed := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskRefresh, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID,
	})
	refreshedView := requireTaskHistoryView(t, refreshed)
	if !taskViewHasTitle(refreshedView, "刷新后的任务") || taskViewHasTitle(refreshedView, "缓存中的任务") {
		t.Fatalf("explicit refresh should rebuild cache, got %#v", refreshedView.TaskGroups)
	}
}

func TestTaskHomeReturnsToCommandMenuWithoutDiscardingCache(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	list := svc.ApplySurfaceAction(control.Action{Kind: control.ActionTasks, SurfaceSessionID: "surface-1", ActorUserID: "user-1"})
	pickerID := requireTaskHistoryView(t, list).PickerID
	home := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskHome, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID,
	})
	if len(home) != 1 || home[0].PageView == nil || home[0].PageView.CommandID != control.FeishuCommandMenu {
		t.Fatalf("expected task home to return to command menu, got %#v", home)
	}
	if svc.activeThreadHistory(svc.root.Surfaces["surface-1"]) != nil {
		t.Fatalf("expected task owner flow to close on home")
	}
	if summary := svc.SurfaceUIRuntimeSummary("surface-1"); !summary.TaskBrowserCacheLoaded {
		t.Fatalf("returning home should retain task cache, got %#v", summary)
	}
}

func TestTaskDetailShowsConversationAndReplyResumesSameThread(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:   "inst-1",
		WorkspaceKey: "/data/shop",
		ShortName:    "shop",
		Online:       true,
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {
				ThreadID:   "thread-1",
				Name:       "更新 OpenClaw 密钥并启动服务",
				CWD:        "/data/shop",
				Loaded:     true,
				LastUsedAt: now,
			},
		},
	})
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	surface := svc.root.Surfaces["surface-1"]
	surface.AttachedInstanceID = "inst-1"
	surface.RouteMode = state.RouteModePinned
	surface.SelectedThreadID = "thread-1"

	list := svc.ApplySurfaceAction(control.Action{Kind: control.ActionTasks, SurfaceSessionID: "surface-1", ActorUserID: "user-1"})
	pickerID := requireTaskHistoryView(t, list).PickerID
	opened := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskOpen, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID, ThreadID: "thread-1",
	})
	if len(opened) != 2 || opened[0].ThreadHistoryView == nil || !opened[0].ThreadHistoryView.Loading || opened[1].DaemonCommand == nil {
		t.Fatalf("expected loading detail + history query, got %#v", opened)
	}
	if opened[1].DaemonCommand.Kind != control.DaemonCommandThreadHistoryRead || opened[1].DaemonCommand.ThreadID != "thread-1" {
		t.Fatalf("unexpected history command: %#v", opened[1].DaemonCommand)
	}

	svc.RecordSurfaceThreadHistory("surface-1", agentproto.ThreadHistoryRecord{
		Thread: agentproto.ThreadSnapshotRecord{ThreadID: "thread-1", Name: "更新 OpenClaw 密钥并启动服务"},
		Turns: []agentproto.ThreadHistoryTurnRecord{{
			TurnID: "turn-1", Status: "completed", CompletedAt: now,
			Items: []agentproto.ThreadHistoryItemRecord{
				{Kind: "user_message", Text: "把密钥替换后启动"},
				{Kind: "tool_call", Text: "内部工具细节不应展示"},
				{Kind: "agent_message", Text: "已经完成并启动"},
			},
		}},
	})
	loaded := svc.HandleSurfaceThreadHistoryLoaded("surface-1")
	detail := requireTaskHistoryView(t, loaded)
	if detail.Mode != control.FeishuThreadHistoryViewConversation || !detail.CanReply || len(detail.Conversation) != 1 {
		t.Fatalf("unexpected conversation detail: %#v", detail)
	}
	joined := ""
	for _, message := range detail.Conversation[0].Messages {
		joined += "\n" + message.Text
	}
	if !strings.Contains(joined, "把密钥替换后启动") || !strings.Contains(joined, "已经完成并启动") || strings.Contains(joined, "内部工具细节") {
		t.Fatalf("conversation content = %q", joined)
	}
	if !strings.Contains(detail.Title, "已完成") {
		t.Fatalf("detail title should include status, got %q", detail.Title)
	}

	replyEvents := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTaskReply,
		SurfaceSessionID: "surface-1",
		ActorUserID:      "user-1",
		MessageID:        "om-reply-1",
		PickerID:         pickerID,
		ThreadID:         "thread-1",
		Text:             "继续检查 Claude Code",
	})
	var command *agentproto.Command
	for index := range replyEvents {
		if replyEvents[index].Command != nil {
			command = replyEvents[index].Command
			break
		}
	}
	if command == nil {
		t.Fatalf("expected reply to dispatch prompt, got %#v", replyEvents)
	}
	if command.Target.ThreadID != "thread-1" || command.Target.ExecutionMode != agentproto.PromptExecutionModeResumeExisting {
		t.Fatalf("reply must resume the same Codex thread, got %#v", command.Target)
	}
	if view := requireTaskHistoryView(t, replyEvents); view.PendingReply != "继续检查 Claude Code" {
		t.Fatalf("expected optimistic reply in conversation, got %#v", view)
	}
}

func TestTaskOpenRestoresPersistedDesktopThreadThenReadsConversation(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	persisted := state.ThreadRecord{
		ThreadID: "desktop-thread-1", Name: "桌面端旧任务", WorkspaceKey: "/data/shop", CWD: "/data/shop", Loaded: true, LastUsedAt: now,
	}
	svc.SetPersistedThreadCatalog(&fakePersistedThreadCatalog{
		recent: []state.ThreadRecord{persisted},
		byID:   map[string]state.ThreadRecord{"desktop-thread-1": persisted},
	})
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")

	list := svc.ApplySurfaceAction(control.Action{Kind: control.ActionTasks, SurfaceSessionID: "surface-1", ActorUserID: "user-1"})
	pickerID := requireTaskHistoryView(t, list).PickerID
	opened := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskOpen, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID, ThreadID: "desktop-thread-1",
	})
	if view := requireTaskHistoryView(t, opened); !view.Loading {
		t.Fatalf("expected restoring task detail, got %#v", view)
	}
	pending := svc.SurfaceSnapshot("surface-1").PendingHeadless
	if pending.InstanceID == "" || pending.ThreadID != "desktop-thread-1" {
		t.Fatalf("expected persisted Desktop task to start headless restore, got %#v", pending)
	}
	var sawStart bool
	for _, event := range opened {
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandStartHeadless {
			sawStart = true
		}
	}
	if !sawStart {
		t.Fatalf("expected headless start command, got %#v", opened)
	}

	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID: pending.InstanceID, WorkspaceKey: "/data/shop", WorkspaceRoot: "/data/shop", Source: "headless", Managed: true, Online: true,
		Threads: map[string]*state.ThreadRecord{},
	})
	connected := svc.ApplyInstanceConnected(pending.InstanceID)
	var historyRead *control.DaemonCommand
	for index := range connected {
		if connected[index].DaemonCommand != nil && connected[index].DaemonCommand.Kind == control.DaemonCommandThreadHistoryRead {
			historyRead = connected[index].DaemonCommand
			break
		}
	}
	if historyRead == nil || historyRead.ThreadID != "desktop-thread-1" {
		t.Fatalf("expected restored task to read original conversation, got %#v", connected)
	}
	record := svc.activeThreadHistory(svc.root.Surfaces["surface-1"])
	if record == nil || !record.TaskBrowser || record.ThreadID != "desktop-thread-1" {
		t.Fatalf("task owner flow should survive route restoration, got %#v", record)
	}
}

func TestTaskBackDuringRestoreKeepsCachedListAfterHeadlessConnects(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	persisted := state.ThreadRecord{
		ThreadID: "desktop-thread-1", Name: "缓存任务", WorkspaceKey: "/data/shop", CWD: "/data/shop", Loaded: true, LastUsedAt: now,
	}
	svc.SetPersistedThreadCatalog(&fakePersistedThreadCatalog{
		recent: []state.ThreadRecord{persisted},
		byID:   map[string]state.ThreadRecord{"desktop-thread-1": persisted},
	})
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	list := svc.ApplySurfaceAction(control.Action{Kind: control.ActionTasks, SurfaceSessionID: "surface-1", ActorUserID: "user-1"})
	pickerID := requireTaskHistoryView(t, list).PickerID
	svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskOpen, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID, ThreadID: "desktop-thread-1",
	})
	pending := svc.SurfaceSnapshot("surface-1").PendingHeadless
	back := svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTaskList, SurfaceSessionID: "surface-1", ActorUserID: "user-1", PickerID: pickerID,
	})
	if view := requireTaskHistoryView(t, back); view.Mode != control.FeishuThreadHistoryViewTasks || !taskViewHasTitle(view, "缓存任务") {
		t.Fatalf("expected immediate cached task list, got %#v", view)
	}

	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID: pending.InstanceID, WorkspaceKey: "/data/shop", WorkspaceRoot: "/data/shop", Source: "headless", Managed: true, Online: true,
		Threads: map[string]*state.ThreadRecord{},
	})
	connected := svc.ApplyInstanceConnected(pending.InstanceID)
	view := requireTaskHistoryView(t, connected)
	if view.Mode != control.FeishuThreadHistoryViewTasks || !taskViewHasTitle(view, "缓存任务") {
		t.Fatalf("restored route should preserve cached list after back, got %#v", view)
	}
	for _, event := range connected {
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandThreadHistoryRead {
			t.Fatalf("back navigation should not reload conversation, got %#v", connected)
		}
	}
}

func TestTaskConversationPaginationKeepsLatestTurnsOnFirstPage(t *testing.T) {
	history := agentproto.ThreadHistoryRecord{Turns: []agentproto.ThreadHistoryTurnRecord{
		{TurnID: "turn-1", Items: []agentproto.ThreadHistoryItemRecord{{Kind: "user_message", Text: "第一轮"}}},
		{TurnID: "turn-2", Items: []agentproto.ThreadHistoryItemRecord{{Kind: "user_message", Text: "第二轮"}}},
		{TurnID: "turn-3", Items: []agentproto.ThreadHistoryItemRecord{{Kind: "user_message", Text: "第三轮"}}},
	}}
	turns := taskConversationTurns(history)
	page, pages, start, end := paginateTaskConversation(0, len(turns))
	if page != 0 || pages != 2 || start != 1 || end != 3 {
		t.Fatalf("latest page = (%d,%d,%d,%d), want (0,2,1,3)", page, pages, start, end)
	}
	if turns[start].TurnID != "turn-2" || turns[end-1].TurnID != "turn-3" {
		t.Fatalf("latest page turns = %#v", turns[start:end])
	}
}

func requireTaskHistoryView(t *testing.T, events []eventcontract.Event) *control.FeishuThreadHistoryView {
	t.Helper()
	for index := range events {
		if events[index].ThreadHistoryView != nil {
			return events[index].ThreadHistoryView
		}
	}
	t.Fatalf("expected task history view, got %#v", events)
	return nil
}

func taskViewHasTitle(view *control.FeishuThreadHistoryView, title string) bool {
	if view == nil {
		return false
	}
	for _, group := range view.TaskGroups {
		for _, task := range group.Tasks {
			if task.Title == title {
				return true
			}
		}
	}
	return false
}
