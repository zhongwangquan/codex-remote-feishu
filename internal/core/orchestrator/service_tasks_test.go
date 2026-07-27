package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestTasksCommandShowsWorkspaceAndWorkingStatus(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:    "inst-shop",
		WorkspaceRoot: "/data/shop",
		WorkspaceKey:  "/data/shop",
		DisplayName:   "shop",
		Online:        true,
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
		SourceMessagePreview: "分析最新电商趋势",
		Status:               state.QueueItemRunning,
	}
	surface.QueueItems["queue-next"] = &state.QueueItemRecord{
		ID:                   "queue-next",
		SourceMessagePreview: "整理趋势报告",
		Status:               state.QueueItemQueued,
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
	text := commandCatalogSummaryText(page)
	for _, want := range []string{"shop", "执行中", "分析最新电商趋势", "排队中（第 1 位）", "整理趋势报告"} {
		if !strings.Contains(text, want) {
			t.Fatalf("tasks page %q does not contain %q", text, want)
		}
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
