package orchestrator

import (
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestImmediateDispatchSkipsTransientQueueReaction(t *testing.T) {
	now := time.Date(2026, 7, 28, 7, 30, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复预览", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionAttachInstance,
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		InstanceID:       "inst-1",
	})

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-1",
		Text:             "请检查预览",
	})

	foundTyping := false
	foundCommand := false
	for _, event := range events {
		if event.PendingInput != nil {
			if event.PendingInput.QueueOn {
				t.Fatalf("immediate dispatch exposed a transient queue reaction: %#v", event.PendingInput)
			}
			if event.PendingInput.SourceMessageID == "msg-1" && event.PendingInput.TypingOn {
				foundTyping = true
			}
		}
		if event.Command != nil && event.Command.Kind == agentproto.CommandPromptSend {
			foundCommand = true
		}
	}
	if !foundTyping || !foundCommand {
		t.Fatalf("expected direct thinking reaction and prompt dispatch, got %#v", events)
	}
}

func TestWaitingQueueStillShowsQueueReaction(t *testing.T) {
	now := time.Date(2026, 7, 28, 7, 31, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		ActiveTurnID:            "turn-active",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复预览", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionAttachInstance,
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		InstanceID:       "inst-1",
	})

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-queued",
		Text:             "下一条任务",
	})

	if len(events) != 1 || events[0].PendingInput == nil || !events[0].PendingInput.QueueOn {
		t.Fatalf("waiting queue did not expose its queue reaction: %#v", events)
	}
	if events[0].PendingInput.TypingOn {
		t.Fatalf("waiting queue should not show thinking yet: %#v", events[0].PendingInput)
	}
}
