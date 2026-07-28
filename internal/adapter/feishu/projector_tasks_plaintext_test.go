package feishu

import (
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
)

func TestProjectTasksPageKeepsWorkspaceAndThreadTitlesOutOfMarkdown(t *testing.T) {
	const (
		workspace = "shop **动态工作区**"
		taskTitle = "修复 [登录流程](https://example.com)"
	)
	projector := NewProjector()
	ops := projector.ProjectEvent("chat-1", commandCatalogEvent(control.FeishuPageView{
		PageID:    control.FeishuCommandTasks,
		CommandID: control.FeishuCommandTasks,
		Title:     "工作任务",
		BodySections: []control.FeishuCardTextSection{
			{Lines: []string{"共 1 个正在工作或等待执行的任务。"}},
			{
				Label: "工作区",
				Lines: []string{
					workspace,
					"• " + taskTitle,
					"  状态：执行中",
				},
			},
		},
		Sealed: true,
	}))
	if len(ops) != 1 || ops[0].Kind != OperationSendCard {
		t.Fatalf("unexpected operations: %#v", ops)
	}

	plainText := ""
	for _, element := range ops[0].CardElements {
		switch element["tag"] {
		case "markdown":
			content, _ := element["content"].(string)
			if strings.Contains(content, workspace) || strings.Contains(content, taskTitle) {
				t.Fatalf("dynamic task text leaked into markdown: %#v", element)
			}
		case "div":
			text, _ := element["text"].(map[string]any)
			if text["tag"] == "plain_text" {
				plainText += "\n" + strings.TrimSpace(text["content"].(string))
			}
		}
	}
	if !strings.Contains(plainText, workspace) || !strings.Contains(plainText, taskTitle) {
		t.Fatalf("expected dynamic task text in plain_text elements, got %q", plainText)
	}
}
