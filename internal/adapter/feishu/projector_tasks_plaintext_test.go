package feishu

import (
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
)

func TestProjectTasksPageKeepsWorkspaceAndThreadTitlesOutOfMarkdown(t *testing.T) {
	const (
		workspace  = "shop **动态工作区**"
		taskTitle  = "修复 [登录流程](https://example.com)"
		sourceLine = "来源：Codex Desktop · 状态：执行中"
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
					sourceLine,
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
			if strings.Contains(content, workspace) || strings.Contains(content, taskTitle) || strings.Contains(content, sourceLine) {
				t.Fatalf("dynamic task text leaked into markdown: %#v", element)
			}
		case "div":
			text, _ := element["text"].(map[string]any)
			if text["tag"] == "plain_text" {
				plainText += "\n" + strings.TrimSpace(text["content"].(string))
			}
		}
	}
	if !strings.Contains(plainText, workspace) || !strings.Contains(plainText, taskTitle) || !strings.Contains(plainText, sourceLine) {
		t.Fatalf("expected dynamic task text in plain_text elements, got %q", plainText)
	}
}

func TestProjectTasksPageMaximumCatalogFitsFeishuCardBudget(t *testing.T) {
	sections := []control.FeishuCardTextSection{{
		Lines: []string{"共 50 个正在工作或等待执行的任务。"},
	}}
	for i := 0; i < 50; i++ {
		sections = append(sections, control.FeishuCardTextSection{
			Label: "工作区",
			Lines: []string{
				strings.Repeat("工", 40),
				"• " + strings.Repeat("任", 40),
				"来源：Codex Desktop · 状态：执行中",
			},
		})
	}

	ops := NewProjector().ProjectEvent("chat-1", commandCatalogEvent(control.FeishuPageView{
		PageID:       control.FeishuCommandTasks,
		CommandID:    control.FeishuCommandTasks,
		Title:        "工作任务",
		BodySections: sections,
		Sealed:       true,
	}))
	if len(ops) != 1 || ops[0].Kind != OperationSendCard {
		t.Fatalf("unexpected operations: %#v", ops)
	}
	if len(ops[0].CardElements) > 200 {
		t.Fatalf("expected no more than 200 card elements, got %d", len(ops[0].CardElements))
	}

	payload := renderOperationCard(ops[0], ops[0].effectiveCardEnvelope())
	size, err := feishuInteractiveMessageTransportSize(payload)
	if err != nil {
		t.Fatalf("measure tasks message transport: %v", err)
	}
	if size > feishuCardTransportLimitBytes {
		t.Fatalf("expected tasks message transport <= %d bytes, got %d", feishuCardTransportLimitBytes, size)
	}
}
