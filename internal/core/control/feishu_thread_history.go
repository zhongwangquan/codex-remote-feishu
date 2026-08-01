package control

import "time"

type FeishuThreadHistoryViewMode string

const (
	FeishuThreadHistoryViewList         FeishuThreadHistoryViewMode = "list"
	FeishuThreadHistoryViewDetail       FeishuThreadHistoryViewMode = "detail"
	FeishuThreadHistoryViewTasks        FeishuThreadHistoryViewMode = "tasks"
	FeishuThreadHistoryViewConversation FeishuThreadHistoryViewMode = "conversation"
)

type FeishuTaskOption struct {
	ThreadID string
	Title    string
	Status   string
	AgeText  string
	Disabled bool
}

type FeishuTaskWorkspaceGroup struct {
	WorkspaceLabel string
	Tasks          []FeishuTaskOption
}

type FeishuConversationMessage struct {
	Role string
	Text string
}

type FeishuConversationTurn struct {
	TurnID      string
	Status      string
	UpdatedText string
	ErrorText   string
	Messages    []FeishuConversationMessage
}

type FeishuThreadHistoryTurnOption struct {
	TurnID   string
	Label    string
	MetaText string
	Current  bool
}

type FeishuThreadHistoryTurnDetail struct {
	TurnID      string
	Ordinal     int
	Status      string
	ErrorText   string
	Inputs      []string
	Outputs     []string
	ReturnPage  int
	PrevTurnID  string
	NextTurnID  string
	UpdatedText string
}

// FeishuThreadHistoryView is the UI-owned read model for /history and the
// task-browser list/conversation card flows.
type FeishuThreadHistoryView struct {
	PickerID         string
	MessageID        string
	Mode             FeishuThreadHistoryViewMode
	Title            string
	ThreadID         string
	ThreadLabel      string
	ThreadStatus     string
	WorkspaceLabel   string
	TurnCount        int
	Page             int
	TotalPages       int
	PageStart        int
	PageEnd          int
	CurrentTurnLabel string
	SelectedTurnID   string
	TurnOptions      []FeishuThreadHistoryTurnOption
	Detail           *FeishuThreadHistoryTurnDetail
	TaskGroups       []FeishuTaskWorkspaceGroup
	Conversation     []FeishuConversationTurn
	CanReply         bool
	PendingReply     string
	Loading          bool
	LoadingText      string
	NoticeCode       string
	NoticeText       string
	NoticeSections   []FeishuCardTextSection
	Hint             string
	CreatedAt        time.Time
	ExpiresAt        time.Time
}
