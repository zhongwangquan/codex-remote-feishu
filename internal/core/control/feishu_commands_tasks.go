package control

func tasksCommandSpec() feishuCommandSpec {
	return feishuCommandSpec{
		definition: FeishuCommandDefinition{
			ID:               FeishuCommandTasks,
			GroupID:          FeishuCommandGroupCurrentWork,
			Title:            "工作任务",
			CanonicalSlash:   "/tasks",
			CanonicalMenuKey: "tasks",
			ArgumentKind:     FeishuCommandArgumentNone,
			Description:      "按工作区查看最近任务，可进入同一 Codex 会话继续对话。",
			ShowInHelp:       true,
			ShowInMenu:       true,
		},
		textExact: []feishuCommandMatch{
			{alias: "/tasks", action: Action{Kind: ActionTasks}},
		},
		menuExact: []feishuCommandMatch{
			{alias: "tasks", action: Action{Kind: ActionTasks}},
		},
	}
}
