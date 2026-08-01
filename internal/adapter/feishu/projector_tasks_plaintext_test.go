package feishu

import (
	"strings"
	"testing"

	projectorpkg "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/projector"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
)

func TestProjectTasksKeepsWorkspaceThreadAndSessionTextOutOfMarkdown(t *testing.T) {
	const (
		workspace = "shop **动态工作区**"
		taskTitle = "修复 [登录流程](https://example.com)"
		userText  = "# 用户输入\n- 不应被当作 Markdown"
		replyText = "[Codex 回复](https://example.com/reply)"
	)
	listElements := projectorpkg.TaskListElements(control.FeishuThreadHistoryView{
		PickerID: "tasks-1",
		Mode:     control.FeishuThreadHistoryViewTasks,
		TaskGroups: []control.FeishuTaskWorkspaceGroup{{
			WorkspaceLabel: workspace,
			Tasks: []control.FeishuTaskOption{{
				ThreadID: "thread-1", Title: taskTitle, Status: "已完成", AgeText: "刚刚",
			}},
		}},
	}, "life-1")
	detailElements := projectorpkg.TaskConversationElements(control.FeishuThreadHistoryView{
		PickerID: "tasks-1", ThreadID: "thread-1", Mode: control.FeishuThreadHistoryViewConversation,
		Conversation: []control.FeishuConversationTurn{{Messages: []control.FeishuConversationMessage{
			{Role: "user", Text: userText},
			{Role: "assistant", Text: replyText},
		}}},
	}, "life-1")
	for _, elements := range [][]map[string]any{listElements, detailElements} {
		assertValuesStayOutOfMarkdown(t, elements, workspace, taskTitle, userText, replyText)
	}
	for _, value := range []string{workspace, taskTitle, userText, replyText} {
		if !containsExactString([]any{listElements, detailElements}, value) {
			t.Fatalf("expected dynamic value %q in card payload", value)
		}
	}
}

func TestTaskCardsExposeOpenBackRefreshHomeAndReplyCallbacks(t *testing.T) {
	listActions := cardActionsFromElements(projectorpkg.TaskListElements(control.FeishuThreadHistoryView{
		PickerID: "tasks-1",
		TaskGroups: []control.FeishuTaskWorkspaceGroup{{Tasks: []control.FeishuTaskOption{{
			ThreadID: "thread-1", Title: "任务一",
		}}}},
	}, "life-1"))
	detailActions := cardActionsFromElements(projectorpkg.TaskConversationElements(control.FeishuThreadHistoryView{
		PickerID: "tasks-1", ThreadID: "thread-1", CanReply: true,
	}, "life-1"))
	kinds := map[string]bool{}
	for _, action := range append(listActions, detailActions...) {
		kinds[actionPayloadKind(cardValueMap(action))] = true
	}
	for _, want := range []string{
		cardActionKindTaskOpen,
		cardActionKindTaskList,
		cardActionKindTaskRefresh,
		cardActionKindTaskHome,
		cardActionKindTaskReply,
	} {
		if !kinds[want] {
			t.Fatalf("missing task callback %q in %#v", want, kinds)
		}
	}
}

func TestProjectMaximumTaskListFitsFeishuCardBudget(t *testing.T) {
	groups := make([]control.FeishuTaskWorkspaceGroup, 0, 4)
	remaining := 30
	for groupIndex := 0; remaining > 0; groupIndex++ {
		group := control.FeishuTaskWorkspaceGroup{WorkspaceLabel: strings.Repeat("工", 40)}
		for len(group.Tasks) < 8 && remaining > 0 {
			group.Tasks = append(group.Tasks, control.FeishuTaskOption{
				ThreadID: strings.Repeat("t", 40),
				Title:    strings.Repeat("任", 80),
				Status:   "已完成",
				AgeText:  "刚刚",
			})
			remaining--
		}
		groups = append(groups, group)
	}
	assertTaskViewFitsCardBudget(t, control.FeishuThreadHistoryView{
		PickerID: "tasks-max", Mode: control.FeishuThreadHistoryViewTasks, Title: "Codex 工作区", TaskGroups: groups,
	})
}

func TestProjectMaximumTaskConversationFitsFeishuCardBudget(t *testing.T) {
	turns := make([]control.FeishuConversationTurn, 0, 2)
	for turnIndex := 0; turnIndex < 2; turnIndex++ {
		turn := control.FeishuConversationTurn{Status: "completed", UpdatedText: "刚刚"}
		for messageIndex := 0; messageIndex < 4; messageIndex++ {
			role := "assistant"
			if messageIndex%2 == 0 {
				role = "user"
			}
			turn.Messages = append(turn.Messages, control.FeishuConversationMessage{Role: role, Text: strings.Repeat("会", 800)})
		}
		turns = append(turns, turn)
	}
	assertTaskViewFitsCardBudget(t, control.FeishuThreadHistoryView{
		PickerID: "tasks-max", Mode: control.FeishuThreadHistoryViewConversation, Title: "任务 · 已完成",
		ThreadID: "thread-1", WorkspaceLabel: "shop", Conversation: turns, CanReply: true,
	})
}

func assertTaskViewFitsCardBudget(t *testing.T, view control.FeishuThreadHistoryView) {
	t.Helper()
	ops := NewProjector().ProjectEvent("chat-1", eventcontract.Event{
		Kind: eventcontract.KindThreadHistory, SurfaceSessionID: "surface-1", ThreadHistoryView: &view,
	})
	if len(ops) != 1 || ops[0].Kind != OperationSendCard {
		t.Fatalf("unexpected operations: %#v", ops)
	}
	if len(ops[0].CardElements) > 200 {
		t.Fatalf("expected no more than 200 card elements, got %d", len(ops[0].CardElements))
	}
	payload := renderOperationCard(ops[0], ops[0].effectiveCardEnvelope())
	size, err := feishuInteractiveMessageTransportSize(payload)
	if err != nil {
		t.Fatalf("measure task message transport: %v", err)
	}
	if size > feishuCardTransportLimitBytes {
		t.Fatalf("expected task message transport <= %d bytes, got %d", feishuCardTransportLimitBytes, size)
	}
}

func assertValuesStayOutOfMarkdown(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	switch typed := value.(type) {
	case []map[string]any:
		for _, item := range typed {
			assertValuesStayOutOfMarkdown(t, item, forbidden...)
		}
	case []any:
		for _, item := range typed {
			assertValuesStayOutOfMarkdown(t, item, forbidden...)
		}
	case map[string]any:
		if typed["tag"] == "markdown" {
			content, _ := typed["content"].(string)
			for _, text := range forbidden {
				if text != "" && strings.Contains(content, text) {
					t.Fatalf("dynamic task text leaked into markdown: %#v", typed)
				}
			}
		}
		for _, nested := range typed {
			assertValuesStayOutOfMarkdown(t, nested, forbidden...)
		}
	}
}

func containsExactString(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, want)
	case []map[string]any:
		for _, item := range typed {
			if containsExactString(item, want) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsExactString(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsExactString(item, want) {
				return true
			}
		}
	}
	return false
}
