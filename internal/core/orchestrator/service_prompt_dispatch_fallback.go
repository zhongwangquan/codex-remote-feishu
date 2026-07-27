package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func (s *Service) maybeFallbackPromptDispatch(surface *state.SurfaceConsoleRecord, now time.Time) []eventcontract.Event {
	if s == nil || surface == nil || s.config.PromptDispatchWatchdogWait <= 0 {
		return nil
	}
	if surface.PendingHeadless != nil || strings.TrimSpace(surface.AttachedInstanceID) == "" || strings.TrimSpace(surface.ActiveQueueItemID) == "" {
		return nil
	}
	item := surface.QueueItems[surface.ActiveQueueItemID]
	if item == nil || item.Status != state.QueueItemDispatching || item.PromptFallback {
		return nil
	}
	inst := s.root.Instances[surface.AttachedInstanceID]
	if inst == nil || !inst.Online || !isHeadlessInstance(inst) || !inst.Managed {
		return nil
	}
	binding := s.remoteBindingForSurface(surface)
	if binding == nil ||
		strings.TrimSpace(binding.QueueItemID) != strings.TrimSpace(item.ID) ||
		strings.TrimSpace(binding.CommandID) == "" ||
		binding.AnyOutputSeen {
		return nil
	}
	startedAt := binding.DispatchStartedAt
	if startedAt.IsZero() || now.Before(startedAt.Add(s.config.PromptDispatchWatchdogWait)) {
		return nil
	}
	return s.startPromptDispatchFallbackRestart(surface, inst, item, binding)
}

func (s *Service) startPromptDispatchFallbackRestart(surface *state.SurfaceConsoleRecord, inst *state.InstanceRecord, item *state.QueueItemRecord, binding *remoteTurnBinding) []eventcontract.Event {
	if surface == nil || inst == nil || item == nil || binding == nil {
		return nil
	}
	launchContract := s.headlessLaunchContractWithOverride(surface, item.FrozenOverride)
	workspaceKey := normalizeWorkspaceClaimKey(firstNonEmpty(s.surfaceCurrentWorkspaceKey(surface), inst.WorkspaceKey, inst.WorkspaceRoot, queueItemFrozenCWD(item)))
	if workspaceKey == "" {
		return notice(surface, "prompt_dispatch_watchdog_workspace_missing", "当前无法确定兜底实例的工作区，暂时不能自动切换到 Codex CLI。")
	}
	threadID := strings.TrimSpace(firstNonEmpty(remoteBindingExecutionThreadID(binding), queuedItemExecutionThreadID(item)))
	threadCWD := strings.TrimSpace(firstNonEmpty(queueItemFrozenCWD(item), bindingThreadCWD(binding), workspaceKey))

	s.nextHeadlessID++
	instanceID := fmt.Sprintf("inst-headless-watchdog-%d-%d", s.now().UnixNano(), s.nextHeadlessID)
	pending := &state.HeadlessLaunchRecord{
		InstanceID:            instanceID,
		ThreadID:              threadID,
		ThreadTitle:           strings.TrimSpace(binding.SourceMessagePreview),
		WorkspaceKey:          workspaceKey,
		ThreadCWD:             threadCWD,
		Backend:               launchContract.Backend,
		CodexProviderID:       launchContract.CodexProviderID,
		ClaudeProfileID:       launchContract.ClaudeProfileID,
		ClaudeReasoningEffort: launchContract.ClaudeReasoningEffort,
		RequestedAt:           s.now(),
		ExpiresAt:             s.now().Add(s.config.HeadlessLaunchWait),
		Status:                state.HeadlessLaunchStarting,
		Purpose:               state.HeadlessLaunchPurposePromptDispatchFallback,
		PrepareNewThread:      item.RouteModeAtEnqueue == state.RouteModeNewThreadReady,
		SourceInstanceID:      inst.InstanceID,
	}
	if !s.claimWorkspace(surface, workspaceKey) {
		return notice(surface, "workspace_busy", "目标 workspace 当前已被其他飞书会话接管，请等待对方 /detach。")
	}

	item.Status = state.QueueItemQueued
	s.clearSurfaceActiveQueueItem(surface, item.ID)
	surface.QueuedQueueItemIDs = prependUniqueQueueItemID(surface.QueuedQueueItemIDs, item.ID)
	s.clearTurnArtifacts(binding.InstanceID, binding.ThreadID, binding.TurnID)
	s.clearPendingRemoteTurn(binding.InstanceID)
	s.clearActiveRemoteTurn(binding.InstanceID)
	surface.AttachedInstanceID = ""
	item.PromptFallback = true
	s.adoptSurfacePendingHeadlessLaunch(surface, pending)

	events := []eventcontract.Event{{
		Kind:             eventcontract.KindDaemonCommand,
		SurfaceSessionID: surface.SurfaceSessionID,
		DaemonCommand: &control.DaemonCommand{
			Kind:             control.DaemonCommandKillHeadless,
			SurfaceSessionID: surface.SurfaceSessionID,
			InstanceID:       inst.InstanceID,
			ThreadID:         threadID,
			WorkspaceKey:     workspaceKey,
			ThreadCWD:        threadCWD,
		},
	}, {
		Kind:             eventcontract.KindDaemonCommand,
		SurfaceSessionID: surface.SurfaceSessionID,
		DaemonCommand: func() *control.DaemonCommand {
			command := &control.DaemonCommand{
				Kind:             control.DaemonCommandStartHeadless,
				SurfaceSessionID: surface.SurfaceSessionID,
				InstanceID:       instanceID,
				ThreadID:         threadID,
				WorkspaceKey:     workspaceKey,
				ThreadCWD:        threadCWD,
			}
			s.applyHeadlessLaunchContract(command, launchContract)
			return command
		}(),
	}}
	events = append(events, s.pendingInputEvents(surface, control.PendingInputState{
		QueueItemID:   item.ID,
		Status:        string(item.Status),
		QueueOn:       true,
		QueuePosition: 1,
	}, queueItemSourceMessageIDs(item))...)
	events = append(events, eventcontract.Event{
		Kind:             eventcontract.KindNotice,
		SurfaceSessionID: surface.SurfaceSessionID,
		SourceMessageID:  strings.TrimSpace(firstNonEmpty(item.ReplyToMessageID, item.SourceMessageID)),
		Notice: &control.Notice{
			Code:  "prompt_dispatch_watchdog_fallback",
			Title: "已切到 Codex CLI 兜底",
			Text:  fmt.Sprintf("飞书链路在 %s 内未收到任何输出，当前消息已自动改走 Codex CLI 兜底通道并重试。", s.config.PromptDispatchWatchdogWait),
		},
		Meta: eventcontract.EventMeta{
			MessageDelivery: eventcontract.ReplyThreadAppendOnlyDelivery(),
		},
	})
	return events
}

func (s *Service) restorePrimaryAfterPromptDispatchFallback(outcome *remoteTurnOutcome) ([]eventcontract.Event, bool) {
	if s == nil ||
		outcome == nil ||
		outcome.Surface == nil ||
		outcome.Item == nil ||
		!outcome.Item.PromptFallback {
		return nil, false
	}
	surface := outcome.Surface
	inst := s.root.Instances[outcome.InstanceID]
	if inst == nil ||
		!inst.Online ||
		strings.TrimSpace(surface.AttachedInstanceID) != strings.TrimSpace(outcome.InstanceID) {
		return nil, false
	}

	workspaceKey := normalizeWorkspaceClaimKey(firstNonEmpty(
		s.surfaceCurrentWorkspaceKey(surface),
		inst.WorkspaceKey,
		inst.WorkspaceRoot,
		queueItemFrozenCWD(outcome.Item),
	))
	if workspaceKey == "" {
		return notice(surface, "prompt_dispatch_primary_restore_workspace_missing", "Codex CLI 兜底已结束，但当前无法确定主链工作区；暂时继续使用兜底实例。请发送 /workspace 重新接管。"), false
	}
	threadID := strings.TrimSpace(firstNonEmpty(
		outcome.ThreadID,
		surface.SelectedThreadID,
		remoteBindingExecutionThreadID(outcome.Binding),
		queuedItemExecutionThreadID(outcome.Item),
	))
	threadCWD := strings.TrimSpace(firstNonEmpty(
		queueItemFrozenCWD(outcome.Item),
		bindingThreadCWD(outcome.Binding),
		workspaceKey,
	))
	threadTitle := ""
	if thread := inst.Threads[threadID]; thread != nil {
		threadTitle = strings.TrimSpace(thread.Name)
		if cwd := strings.TrimSpace(thread.CWD); cwd != "" {
			threadCWD = cwd
		}
	}
	if !s.claimWorkspace(surface, workspaceKey) {
		return notice(surface, "workspace_busy", "主链恢复时发现目标 workspace 已被其他飞书会话接管；暂时继续使用兜底实例，请等待对方 /detach。"), false
	}

	launchContract := s.headlessLaunchContractWithOverride(surface, outcome.Item.FrozenOverride)
	s.nextHeadlessID++
	instanceID := fmt.Sprintf("inst-headless-%d-%d", s.now().UnixNano(), s.nextHeadlessID)
	pending := &state.HeadlessLaunchRecord{
		InstanceID:            instanceID,
		ThreadID:              threadID,
		ThreadTitle:           threadTitle,
		WorkspaceKey:          workspaceKey,
		ThreadCWD:             threadCWD,
		Backend:               launchContract.Backend,
		CodexProviderID:       launchContract.CodexProviderID,
		ClaudeProfileID:       launchContract.ClaudeProfileID,
		ClaudeReasoningEffort: launchContract.ClaudeReasoningEffort,
		RequestedAt:           s.now(),
		ExpiresAt:             s.now().Add(s.config.HeadlessLaunchWait),
		Status:                state.HeadlessLaunchStarting,
		Purpose:               state.HeadlessLaunchPurposePromptDispatchPrimaryRestore,
		SourceInstanceID:      inst.InstanceID,
	}
	surface.AttachedInstanceID = ""
	s.adoptSurfacePendingHeadlessLaunch(surface, pending)

	return []eventcontract.Event{{
		Kind:             eventcontract.KindDaemonCommand,
		SurfaceSessionID: surface.SurfaceSessionID,
		DaemonCommand: &control.DaemonCommand{
			Kind:             control.DaemonCommandKillHeadless,
			SurfaceSessionID: surface.SurfaceSessionID,
			InstanceID:       inst.InstanceID,
			ThreadID:         threadID,
			ThreadTitle:      threadTitle,
			WorkspaceKey:     workspaceKey,
			ThreadCWD:        threadCWD,
		},
	}, {
		Kind:             eventcontract.KindDaemonCommand,
		SurfaceSessionID: surface.SurfaceSessionID,
		DaemonCommand: func() *control.DaemonCommand {
			command := &control.DaemonCommand{
				Kind:             control.DaemonCommandStartHeadless,
				SurfaceSessionID: surface.SurfaceSessionID,
				InstanceID:       instanceID,
				ThreadID:         threadID,
				ThreadTitle:      threadTitle,
				WorkspaceKey:     workspaceKey,
				ThreadCWD:        threadCWD,
			}
			s.applyHeadlessLaunchContract(command, launchContract)
			return command
		}(),
	}, {
		Kind:             eventcontract.KindNotice,
		SurfaceSessionID: surface.SurfaceSessionID,
		Notice: &control.Notice{
			Code:  "prompt_dispatch_primary_restoring",
			Title: "正在恢复飞书主链",
			Text:  "Codex CLI 兜底已结束，正在重新接入标准 managed headless；完成后后续消息会继续走飞书主链。",
		},
	}}, true
}

func prependUniqueQueueItemID(ids []string, queueItemID string) []string {
	queueItemID = strings.TrimSpace(queueItemID)
	if queueItemID == "" {
		return ids
	}
	filtered := make([]string, 0, len(ids)+1)
	filtered = append(filtered, queueItemID)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) == queueItemID {
			continue
		}
		filtered = append(filtered, id)
	}
	return filtered
}
