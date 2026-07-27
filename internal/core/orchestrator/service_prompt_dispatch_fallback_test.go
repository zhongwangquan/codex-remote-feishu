package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestTickFallsBackToManagedHeadlessWhenPromptDispatchStalls(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.config.PromptDispatchWatchdogWait = 20 * time.Second

	inst := &state.InstanceRecord{
		InstanceID:      "inst-codex-1",
		DisplayName:     "repo",
		WorkspaceRoot:   "/data/dl/repo",
		WorkspaceKey:    "/data/dl/repo",
		ShortName:       "repo",
		Backend:         agentproto.BackendCodex,
		CodexProviderID: state.DefaultCodexProviderID,
		Source:          "headless",
		Managed:         true,
		Online:          true,
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "主线程", CWD: "/data/dl/repo", Loaded: true},
		},
	}
	svc.UpsertInstance(inst)
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionStatus,
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
	})
	surface := svc.root.Surfaces["surface-1"]
	surface.Backend = agentproto.BackendCodex
	surface.CodexProviderID = state.DefaultCodexProviderID
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionAttachInstance,
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		InstanceID:       inst.InstanceID,
	})
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionUseThread,
		SurfaceSessionID: "surface-1",
		ThreadID:         "thread-1",
	})

	item := &state.QueueItemRecord{
		ID:                    "queue-1",
		SurfaceSessionID:      surface.SurfaceSessionID,
		ActorUserID:           surface.ActorUserID,
		SourceKind:            state.QueueItemSourceUser,
		SourceMessageID:       "msg-1",
		SourceMessagePreview:  "继续处理",
		ReplyToMessageID:      "msg-1",
		ReplyToMessagePreview: "继续处理",
		Inputs:                []agentproto.Input{{Type: agentproto.InputText, Text: "继续处理"}},
		FrozenDispatchPlan:    testPromptDispatchPlan(agentproto.PromptExecutionModeResumeExisting, "thread-1", "/data/dl/repo", "", agentproto.SurfaceBindingPolicyFollowExecutionThread),
		FrozenPlanMode:        state.PlanModeSettingOff,
		RouteModeAtEnqueue:    state.RouteModePinned,
		Status:                state.QueueItemDispatching,
	}
	surface.QueueItems[item.ID] = item
	surface.ActiveQueueItemID = item.ID
	svc.bindPendingRemoteTurn(inst.InstanceID, &remoteTurnBinding{
		InstanceID:            inst.InstanceID,
		SurfaceSessionID:      surface.SurfaceSessionID,
		QueueItemID:           item.ID,
		SourceMessageID:       item.SourceMessageID,
		SourceMessagePreview:  item.SourceMessagePreview,
		ReplyToMessageID:      item.ReplyToMessageID,
		ReplyToMessagePreview: item.ReplyToMessagePreview,
		DispatchPlan:          queuedItemPromptDispatchPlan(item),
		ThreadID:              "thread-1",
		CommandID:             "cmd-1",
		Status:                string(state.QueueItemDispatching),
		DispatchStartedAt:     now.Add(-21 * time.Second),
	})

	events := svc.Tick(now)

	if surface.PendingHeadless == nil || surface.PendingHeadless.Purpose != state.HeadlessLaunchPurposePromptDispatchFallback {
		t.Fatalf("expected pending headless watchdog restart, got %#v", surface.PendingHeadless)
	}
	if surface.AttachedInstanceID != "" {
		t.Fatalf("expected surface to detach and wait for watchdog headless, got %q", surface.AttachedInstanceID)
	}
	if surface.ActiveQueueItemID != "" {
		t.Fatalf("expected active queue item to be released before retry, got %q", surface.ActiveQueueItemID)
	}
	if item.Status != state.QueueItemQueued {
		t.Fatalf("expected stalled item to return to queued, got %#v", item.Status)
	}
	if !item.PromptFallback {
		t.Fatal("expected stalled item to be marked as the one-shot fallback attempt")
	}
	if len(surface.QueuedQueueItemIDs) != 1 || surface.QueuedQueueItemIDs[0] != item.ID {
		t.Fatalf("expected stalled item to be re-queued to the front, got %#v", surface.QueuedQueueItemIDs)
	}
	if binding := svc.turns.pendingRemoteBinding(inst.InstanceID); binding != nil {
		t.Fatalf("expected old pending remote binding to clear, got %#v", binding)
	}

	var fallbackNotice, killCommand, startCommand bool
	for _, event := range events {
		if event.Notice != nil && event.Notice.Code == "prompt_dispatch_watchdog_fallback" {
			fallbackNotice = true
		}
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandKillHeadless && event.DaemonCommand.InstanceID == inst.InstanceID {
			killCommand = true
		}
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandStartHeadless && event.DaemonCommand.InstanceID == surface.PendingHeadless.InstanceID {
			startCommand = true
		}
	}
	if !fallbackNotice || !killCommand || !startCommand {
		t.Fatalf("expected fallback notice + kill + start events, got %#v", events)
	}
}

func TestTickSkipsFallbackAfterTurnStartedEvenBeforeOutput(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.config.PromptDispatchWatchdogWait = 20 * time.Second

	inst := &state.InstanceRecord{
		InstanceID:      "inst-codex-1",
		WorkspaceRoot:   "/data/dl/repo",
		WorkspaceKey:    "/data/dl/repo",
		Backend:         agentproto.BackendCodex,
		CodexProviderID: state.DefaultCodexProviderID,
		Source:          "headless",
		Managed:         true,
		Online:          true,
	}
	svc.UpsertInstance(inst)
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	surface := svc.root.Surfaces["surface-1"]
	surface.AttachedInstanceID = inst.InstanceID
	surface.Backend = agentproto.BackendCodex
	item := &state.QueueItemRecord{
		ID:                 "queue-1",
		SurfaceSessionID:   surface.SurfaceSessionID,
		RouteModeAtEnqueue: state.RouteModePinned,
		Status:             state.QueueItemRunning,
		FrozenDispatchPlan: testPromptDispatchPlan(agentproto.PromptExecutionModeResumeExisting, "thread-1", "/data/dl/repo", "", agentproto.SurfaceBindingPolicyFollowExecutionThread),
	}
	surface.QueueItems[item.ID] = item
	surface.ActiveQueueItemID = item.ID
	svc.bindActiveRemoteTurn(inst.InstanceID, &remoteTurnBinding{
		InstanceID:        inst.InstanceID,
		SurfaceSessionID:  surface.SurfaceSessionID,
		QueueItemID:       item.ID,
		DispatchPlan:      queuedItemPromptDispatchPlan(item),
		ThreadID:          "thread-1",
		TurnID:            "turn-1",
		Status:            string(state.QueueItemRunning),
		DispatchStartedAt: now.Add(-30 * time.Second),
		StartedAt:         now.Add(-29 * time.Second),
	})

	events := svc.Tick(now)
	if len(events) != 0 {
		t.Fatalf("expected no watchdog fallback after the primary turn has started, got %#v", events)
	}
	if surface.PendingHeadless != nil {
		t.Fatalf("expected no pending fallback launch, got %#v", surface.PendingHeadless)
	}
}

func TestBindPendingRemoteCommandStartsFallbackClockAtActualSend(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	inst := &state.InstanceRecord{
		InstanceID:    "inst-primary",
		WorkspaceRoot: "/data/dl/repo",
		WorkspaceKey:  "/data/dl/repo",
		Backend:       agentproto.BackendCodex,
		Source:        "headless",
		Managed:       true,
		Online:        true,
	}
	svc.UpsertInstance(inst)
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	surface := svc.root.Surfaces["surface-1"]
	surface.AttachedInstanceID = inst.InstanceID
	item := &state.QueueItemRecord{
		ID:                 "queue-1",
		SurfaceSessionID:   surface.SurfaceSessionID,
		FrozenDispatchPlan: testPromptDispatchPlan(agentproto.PromptExecutionModeResumeExisting, "thread-1", "/data/dl/repo", "", agentproto.SurfaceBindingPolicyFollowExecutionThread),
		Status:             state.QueueItemQueued,
	}
	surface.QueueItems[item.ID] = item
	binding := newRemoteTurnBindingForQueueItem(surface, inst, item)
	svc.activateSurfaceQueueItemDispatchWithBinding(surface, item, binding)
	if !binding.DispatchStartedAt.IsZero() {
		t.Fatalf("fallback clock started before the command send boundary: %s", binding.DispatchStartedAt)
	}

	svc.BindPendingRemoteCommand(surface.SurfaceSessionID, "cmd-1")

	if binding.DispatchStartedAt != now {
		t.Fatalf("fallback clock = %s, want actual send time %s", binding.DispatchStartedAt, now)
	}
}

func TestFallbackTurnCompletionRestoresStandardManagedHeadless(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	fallback := &state.InstanceRecord{
		InstanceID:      "inst-headless-watchdog-1",
		DisplayName:     "repo",
		WorkspaceRoot:   "/data/dl/repo",
		WorkspaceKey:    "/data/dl/repo",
		Backend:         agentproto.BackendCodex,
		CodexProviderID: state.DefaultCodexProviderID,
		Source:          "headless",
		Managed:         true,
		Online:          true,
		ActiveThreadID:  "thread-1",
		ActiveTurnID:    "turn-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "主线程", CWD: "/data/dl/repo", Loaded: true},
		},
	}
	svc.UpsertInstance(fallback)
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	surface := svc.root.Surfaces["surface-1"]
	surface.Backend = agentproto.BackendCodex
	surface.CodexProviderID = state.DefaultCodexProviderID
	surface.ClaimedWorkspaceKey = "/data/dl/repo"
	surface.AttachedInstanceID = fallback.InstanceID
	surface.SelectedThreadID = "thread-1"
	surface.RouteMode = state.RouteModePinned
	svc.bindWorkspaceClaim(surface, surface.ClaimedWorkspaceKey)

	item := &state.QueueItemRecord{
		ID:                    "queue-1",
		SurfaceSessionID:      surface.SurfaceSessionID,
		SourceMessageID:       "msg-1",
		SourceMessagePreview:  "继续处理",
		ReplyToMessageID:      "msg-1",
		ReplyToMessagePreview: "继续处理",
		FrozenDispatchPlan:    testPromptDispatchPlan(agentproto.PromptExecutionModeResumeExisting, "thread-1", "/data/dl/repo", "", agentproto.SurfaceBindingPolicyFollowExecutionThread),
		RouteModeAtEnqueue:    state.RouteModePinned,
		PromptFallback:        true,
		Status:                state.QueueItemRunning,
	}
	surface.QueueItems[item.ID] = item
	surface.ActiveQueueItemID = item.ID
	svc.bindActiveRemoteTurn(fallback.InstanceID, &remoteTurnBinding{
		InstanceID:       fallback.InstanceID,
		SurfaceSessionID: surface.SurfaceSessionID,
		QueueItemID:      item.ID,
		DispatchPlan:     queuedItemPromptDispatchPlan(item),
		ThreadID:         "thread-1",
		TurnID:           "turn-1",
		Status:           string(state.QueueItemRunning),
		StartedAt:        now.Add(-time.Second),
	})

	events := svc.ApplyAgentEvent(fallback.InstanceID, agentproto.Event{
		Kind:      agentproto.EventTurnCompleted,
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Status:    "completed",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: surface.SurfaceSessionID},
	})

	if surface.PendingHeadless == nil || surface.PendingHeadless.Purpose != state.HeadlessLaunchPurposePromptDispatchPrimaryRestore {
		t.Fatalf("expected primary restore launch after fallback completion, got %#v", surface.PendingHeadless)
	}
	if surface.AttachedInstanceID != "" {
		t.Fatalf("expected surface to wait detached for the standard managed headless, got %q", surface.AttachedInstanceID)
	}
	if got := surface.PendingHeadless.InstanceID; got == "" || got == fallback.InstanceID || strings.HasPrefix(got, "inst-headless-watchdog-") {
		t.Fatalf("expected a standard managed headless id, got %q", got)
	}

	var killFallback, startPrimary, restoreNotice, promptSend bool
	for _, event := range events {
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandKillHeadless && event.DaemonCommand.InstanceID == fallback.InstanceID {
			killFallback = true
		}
		if event.DaemonCommand != nil && event.DaemonCommand.Kind == control.DaemonCommandStartHeadless && event.DaemonCommand.InstanceID == surface.PendingHeadless.InstanceID {
			startPrimary = true
		}
		if event.Notice != nil && event.Notice.Code == "prompt_dispatch_primary_restoring" {
			restoreNotice = true
		}
		if event.Command != nil && event.Command.Kind == agentproto.CommandPromptSend {
			promptSend = true
		}
	}
	if !killFallback || !startPrimary || !restoreNotice {
		t.Fatalf("expected fallback kill + standard start + restore notice, got %#v", events)
	}
	if promptSend {
		t.Fatalf("expected queued work to wait for standard headless attach, got %#v", events)
	}
}
