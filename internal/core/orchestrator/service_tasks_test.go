package orchestrator

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestTasksCommandGroupsThreadTitlesByWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:    "inst-shop",
		WorkspaceRoot: "/data/shop",
		WorkspaceKey:  "/data/shop",
		DisplayName:   "shop",
		Online:        true,
		Threads: map[string]*state.ThreadRecord{
			"thread-running": {
				ThreadID: "thread-running",
				Name:     "分析电商趋势",
			},
			"thread-next": {
				ThreadID: "thread-next",
				Name:     "整理趋势报告",
			},
		},
	})
	surface := svc.ensureSurface(control.Action{
		SurfaceSessionID: "surface-shop",
		ChatID:           "chat-shop",
		ActorUserID:      "user-shop",
	})
	surface.AttachedInstanceID = "inst-shop"
	surface.ActiveQueueItemID = "queue-running"
	surface.QueuedQueueItemIDs = []string{"queue-next"}
	surface.QueueItems["queue-running"] = &state.QueueItemRecord{
		ID:                   "queue-running",
		SourceMessagePreview: "当前状态",
		FrozenDispatchPlan:   agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-running"),
		Status:               state.QueueItemRunning,
	}
	surface.QueueItems["queue-next"] = &state.QueueItemRecord{
		ID:                   "queue-next",
		SourceMessagePreview: "继续处理",
		FrozenDispatchPlan:   agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-next"),
		Status:               state.QueueItemQueued,
	}
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:   "inst-shop-worker",
		WorkspaceKey: "/data/shop",
		Online:       true,
		Threads: map[string]*state.ThreadRecord{
			"thread-worker": {
				ThreadID: "thread-worker",
				Name:     "优化商品同步",
			},
		},
	})
	workerSurface := svc.ensureSurface(control.Action{SurfaceSessionID: "surface-shop-worker"})
	workerSurface.AttachedInstanceID = "inst-shop-worker"
	workerSurface.ActiveQueueItemID = "queue-worker"
	workerSurface.QueueItems["queue-worker"] = &state.QueueItemRecord{
		ID:                 "queue-worker",
		FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-worker"),
		Status:             state.QueueItemRunning,
	}

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTasks,
		SurfaceSessionID: "surface-shop",
		ChatID:           "chat-shop",
		ActorUserID:      "user-shop",
	})
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one tasks page", events)
	}
	page := commandCatalogFromEvent(t, events[0])
	sections := control.BuildFeishuPageBodySections(*page)
	if len(sections) != 2 {
		t.Fatalf("tasks sections = %#v, want summary + one workspace group", sections)
	}
	if sections[1].Label != "工作区" {
		t.Fatalf("workspace section label = %q, want fixed system copy", sections[1].Label)
	}
	text := commandCatalogSummaryText(page)
	for _, want := range []string{"shop", "执行中", "分析电商趋势", "优化商品同步", "排队中（第 1 位）", "整理趋势报告"} {
		if !strings.Contains(text, want) {
			t.Fatalf("tasks page %q does not contain %q", text, want)
		}
	}
	for _, unwanted := range []string{"当前状态", "继续处理"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("tasks page %q should prefer Codex thread titles over %q", text, unwanted)
		}
	}
}

func TestWorkingTaskSummariesDeduplicateQueuedTurnsForSameThread(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:   "inst-shop",
		WorkspaceKey: "/data/shop",
		Online:       true,
		Threads: map[string]*state.ThreadRecord{
			"thread-shop": {ThreadID: "thread-shop", Name: "修复结算流程"},
		},
	})
	surface := svc.ensureSurface(control.Action{SurfaceSessionID: "surface-shop"})
	surface.AttachedInstanceID = "inst-shop"
	surface.ActiveQueueItemID = "queue-running"
	surface.QueuedQueueItemIDs = []string{"queue-follow-up"}
	surface.QueueItems["queue-running"] = &state.QueueItemRecord{
		ID:                 "queue-running",
		FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-shop"),
		Status:             state.QueueItemRunning,
	}
	surface.QueueItems["queue-follow-up"] = &state.QueueItemRecord{
		ID:                 "queue-follow-up",
		FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-shop"),
		Status:             state.QueueItemQueued,
	}

	tasks := svc.workingTaskSummaries()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v, want one thread-level task", tasks)
	}
	if tasks[0].TaskTitle != "修复结算流程" || tasks[0].Status != state.QueueItemRunning {
		t.Fatalf("task = %#v, want running Codex thread title", tasks[0])
	}
}

func TestTasksCommandCapsLargeTaskLists(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	for index := 0; index < workingTaskCardMaxTasks+2; index++ {
		instanceID := fmt.Sprintf("inst-%02d", index)
		svc.UpsertInstance(&state.InstanceRecord{
			InstanceID:   instanceID,
			WorkspaceKey: fmt.Sprintf("/data/workspace-%02d", index),
			Online:       true,
		})
		surface := svc.ensureSurface(control.Action{SurfaceSessionID: fmt.Sprintf("surface-%02d", index)})
		surface.AttachedInstanceID = instanceID
		surface.ActiveQueueItemID = "queue-running"
		surface.QueueItems["queue-running"] = &state.QueueItemRecord{
			ID:                   "queue-running",
			SourceMessagePreview: fmt.Sprintf("任务 %02d", index),
			Status:               state.QueueItemRunning,
		}
	}

	page := commandCatalogFromEvent(t, svc.tasksTerminalPageEvent(nil, control.Action{}))
	sections := control.BuildFeishuPageBodySections(*page)
	if len(sections) != workingTaskCardMaxTasks+2 {
		t.Fatalf("tasks sections = %d, want summary + %d workspace groups + overflow", len(sections), workingTaskCardMaxTasks)
	}
	text := commandCatalogSummaryText(page)
	if !strings.Contains(text, "共 52 个正在工作或等待执行的任务。") || !strings.Contains(text, "另有 2 个任务未展开。") {
		t.Fatalf("capped tasks page = %q", text)
	}
}

func TestTasksCommandShowsEmptyState(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTasks,
		SurfaceSessionID: "surface-empty",
		ChatID:           "chat-empty",
		ActorUserID:      "user-empty",
	})
	page := commandCatalogFromEvent(t, events[0])
	if text := commandCatalogSummaryText(page); !strings.Contains(text, "当前没有正在执行或排队中的任务") {
		t.Fatalf("empty tasks page = %q", text)
	}
}
