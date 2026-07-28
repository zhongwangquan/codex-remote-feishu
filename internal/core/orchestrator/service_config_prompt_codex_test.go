package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestCodexHeadlessObservedCWDDefaultsDoNotPersistWorkspaceDefaults(t *testing.T) {
	now := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Source:                  "headless",
		Managed:                 true,
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复登录流程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})

	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:            agentproto.EventConfigObserved,
		ThreadID:        "thread-1",
		CWD:             "/data/dl/droid",
		ConfigScope:     "cwd_default",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "medium",
		AccessMode:      agentproto.AccessModeConfirm,
	})

	if defaults := svc.root.WorkspaceDefaults[state.WorkspaceDefaultsStorageKey("/data/dl/droid", state.InstanceBackendContract{
		Backend:         agentproto.BackendCodex,
		CodexProviderID: state.DefaultCodexProviderID,
	})]; defaults != (state.ModelConfigRecord{}) {
		t.Fatalf("expected codex observed cwd defaults not to persist root workspace defaults, got %#v", defaults)
	}
	if defaults := svc.root.Instances["inst-1"].CWDDefaults["/data/dl/droid"]; defaults != (state.ModelConfigRecord{}) {
		t.Fatalf("expected codex observed cwd defaults not to become instance cwd defaults, got %#v", defaults)
	}

	snapshot := svc.SurfaceSnapshot("surface-1")
	if snapshot == nil {
		t.Fatal("expected surface snapshot")
	}
	if snapshot.NextPrompt.EffectiveModel != "" || snapshot.NextPrompt.EffectiveModelSource != "codex_config" {
		t.Fatalf("expected headless model to follow codex config, got %#v", snapshot.NextPrompt)
	}
	if snapshot.NextPrompt.EffectiveReasoningEffort != "" || snapshot.NextPrompt.EffectiveReasoningEffortSource != "codex_config" {
		t.Fatalf("expected headless reasoning to follow codex config, got %#v", snapshot.NextPrompt)
	}
	if snapshot.NextPrompt.EffectiveAccessMode != agentproto.AccessModeFullAccess || snapshot.NextPrompt.EffectiveAccessModeSource != "surface_default" {
		t.Fatalf("expected codex observed cwd access not to affect snapshot, got %#v", snapshot.NextPrompt)
	}

	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-1",
		Text:             "你好",
	})

	surface := svc.root.Surfaces["surface-1"]
	var item *state.QueueItemRecord
	for _, current := range surface.QueueItems {
		item = current
	}
	if item == nil {
		t.Fatal("expected queue item")
	}
	if item.FrozenOverride.Model != "" || item.FrozenOverride.ReasoningEffort != "" {
		t.Fatalf("expected queue item not to freeze codex-owned model/reasoning, got %#v", item.FrozenOverride)
	}
	if item.FrozenOverride.AccessMode != agentproto.AccessModeFullAccess {
		t.Fatalf("expected queue item to freeze fallback access, got %#v", item.FrozenOverride)
	}
}

func TestCodexHeadlessThreadObservedModelReasoningStayBackendOwned(t *testing.T) {
	now := time.Date(2026, 4, 9, 13, 45, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Source:                  "headless",
		Managed:                 true,
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复登录流程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})

	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:            agentproto.EventConfigObserved,
		ThreadID:        "thread-1",
		CWD:             "/data/dl/droid",
		ConfigScope:     "thread",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "medium",
		AccessMode:      agentproto.AccessModeConfirm,
		PlanMode:        "on",
	})

	if defaults := svc.root.WorkspaceDefaults[state.WorkspaceDefaultsStorageKey("/data/dl/droid", state.InstanceBackendContract{
		Backend:         agentproto.BackendCodex,
		CodexProviderID: state.DefaultCodexProviderID,
	})]; defaults != (state.ModelConfigRecord{}) {
		t.Fatalf("expected codex thread observation not to persist workspace defaults, got %#v", defaults)
	}

	snapshot := svc.SurfaceSnapshot("surface-1")
	if snapshot == nil {
		t.Fatal("expected surface snapshot")
	}
	if snapshot.NextPrompt.BaseModel != "gpt-5.3-codex" || snapshot.NextPrompt.BaseModelSource != "thread" {
		t.Fatalf("expected thread observed model in snapshot, got %#v", snapshot.NextPrompt)
	}
	if snapshot.NextPrompt.BaseReasoningEffort != "medium" || snapshot.NextPrompt.BaseReasoningEffortSource != "thread" {
		t.Fatalf("expected thread observed effort in snapshot, got %#v", snapshot.NextPrompt)
	}
	if snapshot.NextPrompt.ObservedThreadPlanMode != "on" || snapshot.NextPrompt.EffectivePlanMode != "off" {
		t.Fatalf("expected observed thread plan without surface plan persistence, got %#v", snapshot.NextPrompt)
	}
	if snapshot.NextPrompt.EffectiveAccessMode != agentproto.AccessModeFullAccess || snapshot.NextPrompt.EffectiveAccessModeSource != "surface_default" {
		t.Fatalf("expected observed thread access not to become default, got %#v", snapshot.NextPrompt)
	}

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-1",
		Text:             "继续",
	})
	command := promptSendCommandFromEvents(t, events)
	if command.Overrides.Model != "" || command.Overrides.ReasoningEffort != "" {
		t.Fatalf("expected prompt dispatch not to copy observed thread config into overrides, got %#v", command.Overrides)
	}

	surface := svc.root.Surfaces["surface-1"]
	var item *state.QueueItemRecord
	for _, current := range surface.QueueItems {
		item = current
	}
	if item == nil {
		t.Fatal("expected queue item")
	}
	if item.FrozenOverride.Model != "" || item.FrozenOverride.ReasoningEffort != "" {
		t.Fatalf("expected queue item not to freeze observed thread model/reasoning, got %#v", item.FrozenOverride)
	}
	if item.FrozenOverride.AccessMode != agentproto.AccessModeFullAccess {
		t.Fatalf("expected queue item to keep default access, got %#v", item.FrozenOverride)
	}
}

func TestCodexHeadlessModelClearRestoresCodexOwnedConfig(t *testing.T) {
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Source:                  "headless",
		Managed:                 true,
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {
				ThreadID:      "thread-1",
				Name:          "修复登录流程",
				CWD:           "/data/dl/droid",
				ExplicitModel: "gpt-5.4",
			},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionModelCommand, SurfaceSessionID: "surface-1", Text: "/model gpt-5.6-sol xhigh"})

	cleared := svc.ApplySurfaceAction(control.Action{Kind: control.ActionModelCommand, SurfaceSessionID: "surface-1", Text: "/model clear"})
	if len(cleared) != 1 || cleared[0].Notice == nil || !strings.Contains(cleared[0].Notice.Text, "跟随 Codex 当前配置") {
		t.Fatalf("expected model clear to confirm codex-following state, got %#v", cleared)
	}
	if override := svc.root.Surfaces["surface-1"].PromptOverride; override.Model != "" || override.ReasoningEffort != "" {
		t.Fatalf("expected model clear to remove explicit overrides, got %#v", override)
	}

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-1",
		Text:             "继续",
	})
	command := promptSendCommandFromEvents(t, events)
	if command.Overrides.Model != "" || command.Overrides.ReasoningEffort != "" {
		t.Fatalf("expected cleared prompt to leave model/reasoning to codex, got %#v", command.Overrides)
	}
}
