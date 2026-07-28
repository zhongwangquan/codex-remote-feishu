package codexstate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/state"
	"github.com/kxn/codex-remote-feishu/internal/core/threadcatalogcontract"
)

const (
	workingTaskDefaultLimit       = 50
	workingTaskMinCandidateLimit  = 200
	workingTaskMaxCandidateLimit  = 1000
	rolloutLifecycleReadChunkSize = 64 * 1024
	workingTaskActivityWindow     = 10 * time.Minute
	workingTaskContinuableWindow  = 24 * time.Hour
)

type workingTaskCandidate struct {
	thread      state.ThreadRecord
	source      string
	rolloutPath string
}

type rolloutLifecycleLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
	} `json:"payload"`
}

type rolloutSessionMetaLine struct {
	Type    string `json:"type"`
	Payload struct {
		Originator string `json:"originator"`
	} `json:"payload"`
}

func (c *SQLiteThreadCatalog) WorkingTasks(limit int) ([]threadcatalogcontract.WorkingTaskRecord, error) {
	if limit <= 0 {
		limit = workingTaskDefaultLimit
	}
	candidates, err := c.workingTaskCandidates(workingTaskCandidateLimit(limit))
	if err != nil {
		c.logError("query working task candidates", err)
		return nil, err
	}
	tasks := make([]threadcatalogcontract.WorkingTaskRecord, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		originator, originatorErr := rolloutOriginator(candidate.rolloutPath)
		if originatorErr != nil {
			c.logError("read working task originator", originatorErr)
			continue
		}
		source, include := workingTaskSource(candidate.source, originator)
		if !include {
			continue
		}
		active := c.workingTaskCandidateActive(candidate)
		if source != threadcatalogcontract.WorkingTaskSourceCodexDesktop && !active {
			continue
		}
		if source == threadcatalogcontract.WorkingTaskSourceCodexDesktop &&
			!active &&
			c.now().Sub(candidate.thread.LastUsedAt) > workingTaskContinuableWindow {
			continue
		}
		taskState := threadcatalogcontract.WorkingTaskStateContinuable
		if active {
			taskState = threadcatalogcontract.WorkingTaskStateActive
		}
		tasks = append(tasks, threadcatalogcontract.WorkingTaskRecord{
			Thread: candidate.thread,
			Source: source,
			State:  taskState,
		})
		if len(tasks) >= limit {
			break
		}
	}
	return tasks, nil
}

func (c *SQLiteThreadCatalog) workingTaskCandidateActive(candidate workingTaskCandidate) bool {
	info, err := os.Stat(candidate.rolloutPath)
	if err != nil {
		c.logError("stat working task rollout", err)
		return false
	}
	if c.now().Sub(info.ModTime()) > workingTaskActivityWindow {
		return false
	}
	active, err := rolloutHasActiveTask(candidate.rolloutPath)
	if err != nil {
		c.logError("read working task lifecycle", err)
		return false
	}
	return active
}

func (c *SQLiteThreadCatalog) workingTaskCandidates(limit int) ([]workingTaskCandidate, error) {
	var candidates []workingTaskCandidate
	err := c.readWithRetry("query working task candidates", func(db *sql.DB) error {
		threadSourceFilter := ""
		hasThreadSource, err := sqliteTableHasColumn(db, "threads", "thread_source")
		if err != nil {
			return err
		}
		if hasThreadSource {
			threadSourceFilter = "\n  AND COALESCE(NULLIF(TRIM(thread_source), ''), 'user') = 'user'"
		}
		rows, err := db.Query(`
SELECT id, title, cwd, updated_at, source, rollout_path, first_user_message
FROM threads
WHERE archived = 0
  AND source IN ('cli', 'vscode')
  AND COALESCE(agent_role, '') = ''
  AND cwd NOT LIKE '%/_tmp-codex-thread-latency-%'
  AND cwd NOT LIKE '%/_tmp-codex-appserver-%'
  AND title NOT LIKE 'Automation:%'
  AND first_user_message NOT LIKE 'Automation:%'
  AND cwd NOT LIKE ?`+threadSourceFilter+`
ORDER BY updated_at DESC, id DESC
LIMIT ?
`, cronRepoRunPathPattern, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		local := make([]workingTaskCandidate, 0, limit)
		for rows.Next() {
			var (
				threadID         string
				title            string
				cwd              string
				updatedAt        int64
				source           string
				rolloutPath      string
				firstUserMessage sql.NullString
			)
			if err := rows.Scan(&threadID, &title, &cwd, &updatedAt, &source, &rolloutPath, &firstUserMessage); err != nil {
				return err
			}
			threadID = strings.TrimSpace(threadID)
			cwd = strings.TrimSpace(cwd)
			rolloutPath = strings.TrimSpace(rolloutPath)
			if threadID == "" || rolloutPath == "" || internalProbeWorkspace(cwd) {
				continue
			}
			preview := strings.TrimSpace(firstUserMessage.String)
			title = strings.TrimSpace(title)
			if preview == "" {
				preview = title
			}
			local = append(local, workingTaskCandidate{
				thread: state.ThreadRecord{
					ThreadID:         threadID,
					Name:             title,
					Preview:          preview,
					FirstUserMessage: preview,
					WorkspaceKey:     state.ResolveWorkspaceKey(cwd),
					CWD:              cwd,
					Loaded:           true,
					LastUsedAt:       unixTimestamp(updatedAt),
				},
				source:      source,
				rolloutPath: rolloutPath,
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		candidates = local
		return nil
	})
	return candidates, err
}

func sqliteTableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func workingTaskCandidateLimit(taskLimit int) int {
	candidateLimit := taskLimit * 4
	if candidateLimit < workingTaskMinCandidateLimit {
		candidateLimit = workingTaskMinCandidateLimit
	}
	if candidateLimit > workingTaskMaxCandidateLimit {
		candidateLimit = workingTaskMaxCandidateLimit
	}
	return candidateLimit
}

func workingTaskSource(source, originator string) (threadcatalogcontract.WorkingTaskSource, bool) {
	switch strings.ToLower(strings.TrimSpace(originator)) {
	case "codex remote headless":
		return "", false
	case "codex desktop":
		return threadcatalogcontract.WorkingTaskSourceCodexDesktop, true
	case "codex-tui", "codex cli":
		return threadcatalogcontract.WorkingTaskSourceCodexCLI, true
	case "multica-agent-sdk":
		return threadcatalogcontract.WorkingTaskSourceMultica, true
	default:
		if strings.EqualFold(strings.TrimSpace(source), "cli") {
			return threadcatalogcontract.WorkingTaskSourceCodexCLI, true
		}
		return threadcatalogcontract.WorkingTaskSourceCodexDesktop, true
	}
}

func rolloutOriginator(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("missing rollout path")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var decoded rolloutSessionMetaLine
	if err := decoder.Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.Type != "session_meta" {
		return "", nil
	}
	return strings.TrimSpace(decoded.Payload.Originator), nil
}

func rolloutHasActiveTask(path string) (bool, error) {
	kind, err := latestRolloutTaskLifecycle(path)
	if err != nil {
		return false, err
	}
	return kind == "task_started", nil
}

func latestRolloutTaskLifecycle(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("missing rollout path")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	var (
		offset    = info.Size()
		remainder []byte
	)
	for offset > 0 {
		readSize := int64(rolloutLifecycleReadChunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		n, readErr := file.ReadAt(chunk, offset)
		if readErr != nil && readErr != io.EOF {
			return "", readErr
		}
		chunk = chunk[:n]
		data := make([]byte, 0, len(chunk)+len(remainder))
		data = append(data, chunk...)
		data = append(data, remainder...)
		lines := bytes.Split(data, []byte{'\n'})
		if offset > 0 {
			remainder = bytes.Clone(lines[0])
			lines = lines[1:]
		} else {
			remainder = nil
		}
		for index := len(lines) - 1; index >= 0; index-- {
			if lifecycle := rolloutTaskLifecycle(lines[index]); lifecycle != "" {
				return lifecycle, nil
			}
		}
	}
	return "", nil
}

func rolloutTaskLifecycle(line []byte) string {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.Contains(line, []byte(`"event_msg"`)) {
		return ""
	}
	var decoded rolloutLifecycleLine
	if err := json.Unmarshal(line, &decoded); err != nil || decoded.Type != "event_msg" {
		return ""
	}
	switch strings.TrimSpace(decoded.Payload.Type) {
	case "task_started", "task_complete", "turn_aborted":
		return strings.TrimSpace(decoded.Payload.Type)
	default:
		return ""
	}
}
