package feishu

import (
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	larkcallback "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestParseCardActionTriggerEventBuildsHistoryActions(t *testing.T) {
	tests := []struct {
		name      string
		payload   map[string]any
		option    string
		formValue map[string]interface{}
		wantKind  control.ActionKind
		wantPage  int
		wantTurn  string
	}{
		{
			name: "page button",
			payload: map[string]any{
				"kind":      cardActionKindHistoryPage,
				"picker_id": "history-1",
				"page":      2,
			},
			wantKind: control.ActionHistoryPage,
			wantPage: 2,
		},
		{
			name: "detail from form value",
			payload: map[string]any{
				"kind":      cardActionKindHistoryDetail,
				"picker_id": "history-1",
			},
			formValue: map[string]interface{}{
				cardThreadHistoryTurnFieldName: []interface{}{"turn-2"},
			},
			wantKind: control.ActionHistoryDetail,
			wantTurn: "turn-2",
		},
		{
			name: "detail from option fallback",
			payload: map[string]any{
				"kind":       cardActionKindHistoryDetail,
				"picker_id":  "history-1",
				"field_name": cardThreadHistoryTurnFieldName,
			},
			option:   "turn-3",
			wantKind: control.ActionHistoryDetail,
			wantTurn: "turn-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := NewLiveGateway(LiveGatewayConfig{GatewayID: "app-1"})
			gateway.recordSurfaceMessage("om-card-history", "feishu:app-1:user:user-1")
			userID := "user-1"
			event := &larkcallback.CardActionTriggerEvent{
				Event: &larkcallback.CardActionTriggerRequest{
					Operator: &larkcallback.Operator{UserID: &userID},
					Action: &larkcallback.CallBackAction{
						Value:     tt.payload,
						Option:    tt.option,
						FormValue: tt.formValue,
					},
					Context: &larkcallback.Context{
						OpenChatID:    "oc_1",
						OpenMessageID: "om-card-history",
					},
				},
			}

			action, ok := gateway.parseCardActionTriggerEvent(event)
			if !ok {
				t.Fatal("expected history action to parse")
			}
			if action.Kind != tt.wantKind || action.PickerID != "history-1" {
				t.Fatalf("unexpected history action: %#v", action)
			}
			if action.Page != tt.wantPage {
				t.Fatalf("page = %d, want %d", action.Page, tt.wantPage)
			}
			if action.TurnID != tt.wantTurn {
				t.Fatalf("turn id = %q, want %q", action.TurnID, tt.wantTurn)
			}
		})
	}
}

func TestParseCardActionTriggerEventHistoryDetailPrefersFormValueOverOptionInGroupChat(t *testing.T) {
	gateway := NewLiveGateway(LiveGatewayConfig{GatewayID: "app-1"})
	gateway.recordSurfaceMessage("om-card-history-conflict", "feishu:app-1:chat:oc_group")
	userID := "user-1"
	event := &larkcallback.CardActionTriggerEvent{
		Event: &larkcallback.CardActionTriggerRequest{
			Operator: &larkcallback.Operator{UserID: &userID},
			Action: &larkcallback.CallBackAction{
				Value: map[string]any{
					"kind":       cardActionKindHistoryDetail,
					"picker_id":  "history-1",
					"field_name": cardThreadHistoryTurnFieldName,
				},
				Option: "turn-from-option",
				FormValue: map[string]interface{}{
					cardThreadHistoryTurnFieldName: []interface{}{"turn-from-form"},
				},
			},
			Context: &larkcallback.Context{
				OpenChatID:    "oc_group",
				OpenMessageID: "om-card-history-conflict",
			},
		},
	}

	action, ok := gateway.parseCardActionTriggerEvent(event)
	if !ok {
		t.Fatal("expected conflicting history detail action to parse")
	}
	if action.Kind != control.ActionHistoryDetail || action.PickerID != "history-1" {
		t.Fatalf("unexpected history action: %#v", action)
	}
	if action.SurfaceSessionID != "feishu:app-1:chat:oc_group" {
		t.Fatalf("expected group-card callback to keep chat surface, got %#v", action)
	}
	if action.TurnID != "turn-from-form" {
		t.Fatalf("turn id = %q, want %q", action.TurnID, "turn-from-form")
	}
}

func TestParseCardActionTriggerEventBuildsTaskNavigationActions(t *testing.T) {
	tests := []struct {
		kind      string
		wantKind  control.ActionKind
		threadID  string
		page      int
		formValue map[string]interface{}
		wantText  string
	}{
		{kind: cardActionKindTaskOpen, wantKind: control.ActionTaskOpen, threadID: "thread-1"},
		{kind: cardActionKindTaskList, wantKind: control.ActionTaskList},
		{kind: cardActionKindTaskRefresh, wantKind: control.ActionTaskRefresh, threadID: "thread-1"},
		{kind: cardActionKindTaskPage, wantKind: control.ActionTaskPage, threadID: "thread-1", page: 2},
		{kind: cardActionKindTaskHome, wantKind: control.ActionTaskHome},
		{
			kind: cardActionKindTaskReply, wantKind: control.ActionTaskReply, threadID: "thread-1",
			formValue: map[string]interface{}{cardTaskReplyFieldName: "继续处理"}, wantText: "继续处理",
		},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			gateway := NewLiveGateway(LiveGatewayConfig{GatewayID: "app-1"})
			gateway.recordSurfaceMessage("om-card-task", "feishu:app-1:user:user-1")
			userID := "user-1"
			payload := map[string]any{"kind": tt.kind, "picker_id": "tasks-1"}
			if tt.threadID != "" {
				payload["thread_id"] = tt.threadID
			}
			if tt.page > 0 {
				payload["page"] = tt.page
			}
			if tt.kind == cardActionKindTaskReply {
				payload["field_name"] = cardTaskReplyFieldName
			}
			event := &larkcallback.CardActionTriggerEvent{Event: &larkcallback.CardActionTriggerRequest{
				Operator: &larkcallback.Operator{UserID: &userID},
				Action:   &larkcallback.CallBackAction{Value: payload, FormValue: tt.formValue},
				Context:  &larkcallback.Context{OpenChatID: "oc_1", OpenMessageID: "om-card-task"},
			}}
			action, ok := gateway.parseCardActionTriggerEvent(event)
			if !ok {
				t.Fatal("expected task callback to parse")
			}
			if action.Kind != tt.wantKind || action.PickerID != "tasks-1" || action.ThreadID != tt.threadID || action.Page != tt.page || action.Text != tt.wantText {
				t.Fatalf("unexpected task action: %#v", action)
			}
		})
	}
}
