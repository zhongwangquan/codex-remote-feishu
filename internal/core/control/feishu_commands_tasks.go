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
			Description:      "汇总正在执行和排队中的任务，展示所属工作区与当前状态。",
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
