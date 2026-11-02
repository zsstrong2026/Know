package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// meetingEngine 讨论引擎：广播式多智能体讨论（M1 先支持顺序单/多智能体，并发与筛选在 M2）
type meetingEngine struct {
	roomRepo        interfaces.MeetingRoomRepository
	participantRepo interfaces.MeetingParticipantRepository
	messageRepo     interfaces.MeetingMessageRepository
	agendaRepo      interfaces.MeetingAgendaRepository
	agentRepo       interfaces.CustomAgentRepository
	artifactRepo    interfaces.MeetingArtifactRepository
	sessionService  interfaces.SessionService
	modelService    interfaces.ModelService
	redis           *redis.Client
	broadcaster     *MeetingBroadcaster
	cfg             *config.Config
	contextMax      int // LLM 上下文消息上限（MEETING_CONTEXT_MAX_MESSAGES，默认 50）
	turnLockLease   time.Duration

	messageService    interfaces.MessageService                // 后台会话 assistant 消息落库（产物链路）
	sandboxConfigRepo repository.TenantSandboxConfigRepository // 租户沙箱配置解析（产物执行沙箱）
	collector         *ArtifactCollector                       // 沙箱产物采集（可能为 typed nil，使用前判空）
}

func NewMeetingEngine(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agendaRepo interfaces.MeetingAgendaRepository,
	agentRepo interfaces.CustomAgentRepository,
	artifactRepo interfaces.MeetingArtifactRepository,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	messageService interfaces.MessageService,
	sandboxConfigRepo repository.TenantSandboxConfigRepository,
	collector *ArtifactCollector,
	redisClient *redis.Client,
	cfg *config.Config,
) interfaces.MeetingEngine {
	// 上下文消息上限：优先取配置，缺失或非法时回退 50
	contextMax := 50
	if cfg != nil && cfg.Meeting != nil && cfg.Meeting.ContextMaxMessages > 0 {
		contextMax = cfg.Meeting.ContextMaxMessages
	}
	return &meetingEngine{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agendaRepo: agendaRepo, agentRepo: agentRepo,
		artifactRepo:   artifactRepo,
		sessionService: sessionService, modelService: modelService,
		redis:         redisClient,
		broadcaster:   NewMeetingBroadcaster(),
		cfg:           cfg,
		contextMax:    contextMax,
		turnLockLease: 5 * time.Minute,

		messageService:    messageService,
		sandboxConfigRepo: sandboxConfigRepo,
		collector:         collector,
	}
}

// Broadcaster 暴露给 handler 层订阅 SSE（同进程）
func (e *meetingEngine) Broadcaster() *MeetingBroadcaster { return e.broadcaster }

func turnLockKey(roomID string) string  { return fmt.Sprintf("meeting:%s:turn_lock", roomID) }
func interruptKey(roomID string) string { return fmt.Sprintf("meeting:%s:interrupt", roomID) }

// RunTurn 执行讨论轮次：取锁后循环消化排队输入，直至没有新的用户消息
func (e *meetingEngine) RunTurn(ctx context.Context, roomID string, triggerMessageID string) error {
	token, err := redislock.NewToken()
	if err != nil {
		return err
	}
	ok, err := redislock.TryAcquire(ctx, e.redis, turnLockKey(roomID), token, e.turnLockLease)
	if err != nil {
		return err
	}
	if !ok {
		// 已有讨论轮进行中：本条消息排队（已落库，锁持有者会在循环末尾拾取），这里直接返回
		logger.Infof(ctx, "meeting turn already running, queued. room=%s", roomID)
		return nil
	}
	defer func() {
		if _, err := redislock.Release(ctx, e.redis, turnLockKey(roomID), token); err != nil {
			// 释放失败有 5min lease 兜底，仅记录便于观测
			logger.Warnf(ctx, "meeting turn lock release failed, room=%s err=%v", roomID, err)
		}
	}()
	// 进入新一轮讨论即清除遗留的打断标记
	e.clearInterrupt(ctx, roomID)

	current := triggerMessageID
	converged := false // 裁判判定本轮讨论是否已收敛（由 runDiscussion 返回）
	for {
		if e.isInterrupted(ctx, roomID) {
			// 用户已打断：清除标记并停止继续消化排队输入
			e.clearInterrupt(ctx, roomID)
			break
		}
		in, err := e.prepareTurn(ctx, roomID, current)
		if err != nil {
			return err
		}
		room := in.room
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]interface{}{
			"room_id": roomID, "trigger_message_id": current,
		}})
		converged = e.runDiscussion(ctx, room, in.trigger, in.speakers, in.agents, in.participants)
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
			"room_id": roomID, "trigger_message_id": current,
		}})

		// 裁判判定已收敛：本轮讨论结束。启用议程时自动开题下一议题并继续，
		// 否则等待用户指示。
		if converged {
			// 启用议程时自动开题下一议题：主持人落"开题"消息并接着讨论，
			// 避免每个子议题都要用户手动触发一次。
			if room.AgendaEnabled {
				if next := e.openNextAgenda(ctx, room); next != nil {
					current = next.ID
					continue
				}
			}
			break
		}

		// 用户打断（"回到正题"）：落系统提示 + 主持人生成收敛消息 + 广播 guard.triggered，随后结束本轮
		if e.isInterrupted(ctx, roomID) {
			e.handleInterrupt(ctx, room, roomID)
			e.clearInterrupt(ctx, roomID)
			break
		}

		// 护栏：检测互呛（同一对智能体连续多来回且无用户/主持人插入）→ 主持人收敛 → 广播 guard.triggered。
		// 放在 TurnEnded 之后，保证 TurnStarted/TurnEnded 始终配对；触发后结束本轮并等待用户指示（不再消化排队输入）。
		if recentForGuard, gerr := e.messageRepo.ListByRoom(ctx, roomID, "", e.guardWindowSize(room)); gerr == nil {
			if hit, pair := detectBickering(recentForGuard, e.guardMaxExchange(room)); hit {
				nameA := e.agentDisplayName(ctx, roomID, pair[0])
				nameB := e.agentDisplayName(ctx, roomID, pair[1])
				content := e.generateGuardMessage(ctx, room, nameA, nameB, recentForGuard)
				if hostMsg, herr := e.appendHostMessage(ctx, roomID, content); herr == nil {
					e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventGuardTriggered, Data: map[string]interface{}{
						"agents": []string{pair[0], pair[1]}, "message": hostMsg,
					}})
				} else {
					logger.Warnf(ctx, "meeting guard host message failed, room=%s err=%v", roomID, herr)
				}
				break // 护栏触发即结束当前讨论轮，等待用户指示
			}
		} else if gerr != nil {
			logger.Warnf(ctx, "meeting guard window query failed, room=%s err=%v", roomID, gerr)
		}

		// 拾取排队输入：本轮触发消息之后的最后一条用户消息
		recent, err := e.messageRepo.ListByRoom(ctx, roomID, "", 50)
		if err != nil {
			return err
		}
		next := extractQueuedTrigger(recent, current)
		if next == nil {
			break // 没有新输入，结束
		}
		current = next.ID
	}
	return nil
}

// turnInput 一轮讨论的输入快照
type turnInput struct {
	room         *types.MeetingRoom
	trigger      *types.MeetingMessage
	speakers     []*types.CustomAgent // 本轮发言者（selectSpeakers 结果）
	agents       []*types.CustomAgent // 全部参会智能体（供子轮按 ID 取回）
	participants []*types.MeetingParticipant
}

// prepareTurn 加载本轮所需的会议/触发消息/参会智能体，并选出本轮发言者
func (e *meetingEngine) prepareTurn(ctx context.Context, roomID string, triggerMessageID string) (*turnInput, error) {
	room, err := e.roomRepo.GetByIDInternal(ctx, roomID)
	if err != nil {
		return nil, err
	}
	trigger, err := e.messageRepo.GetByID(ctx, triggerMessageID)
	if err != nil {
		return nil, err
	}
	participants, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	agents := filterAgentParticipants(participants)
	agentObjs := make([]*types.CustomAgent, 0, len(agents))
	for _, p := range agents {
		agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID)
		if err != nil {
			logger.Warnf(ctx, "meeting load agent failed, room=%s agent=%s err=%v", roomID, p.AgentID, err)
			continue
		}
		agentObjs = append(agentObjs, agent)
	}
	// 结构性治理：用户跳过开场直接发言时，确保议程已生成（懒生成一次）
	e.ensureAgendaGenerated(ctx, room, agentObjs)
	return &turnInput{
		room: room, trigger: trigger,
		speakers:     e.selectSpeakers(ctx, room, trigger, agentObjs),
		agents:       agentObjs,
		participants: participants,
	}, nil
}

// extractQueuedTrigger 返回 triggerID 之后最后一条用户消息；无则 nil。
// 注意 ListByRoom 按时间升序（旧->新）返回。
func extractQueuedTrigger(msgs []*types.MeetingMessage, triggerID string) *types.MeetingMessage {
	triggerIdx := -1
	for i, m := range msgs {
		if m.ID == triggerID {
			triggerIdx = i
		}
	}
	if triggerIdx < 0 {
		return nil
	}
	for i := len(msgs) - 1; i > triggerIdx; i-- {
		if msgs[i].SenderKind == types.MeetingSenderUser {
			return msgs[i]
		}
	}
	return nil
}

// shouldRunParallel 判断本轮是否走并发模式：parallel 且发言者多于 1 个
func shouldRunParallel(mode string, speakerCount int) bool {
	return mode == types.MeetingBroadcastParallel && speakerCount > 1
}

// errAgentPass 智能体弃权（回复 PASS）：静默跳过，不落库、不提示，不占用讨论产出
var errAgentPass = errors.New("meeting: agent passed")

// mentionTokenRe 匹配 @某人（@ 后接连续的字母/数字/下划线/连字符/汉字）
var mentionTokenRe = regexp.MustCompile(`@([\p{L}\p{N}_\-]+)`)

// passReplyRe 判定弃权回复：整条回复除空白/标点/符号（含全角与 Markdown 强调符）外只剩 PASS
var passReplyRe = regexp.MustCompile(`(?i)^[\s\p{Z}\p{P}\p{S}]*pass[\s\p{Z}\p{P}\p{S}]*$`)

// isPassReply 判断智能体回复是否为弃权（PASS）。
// 采用整条严格匹配，避免把正文里偶然出现 "PASS" 字样的正常发言误判为弃权。
func isPassReply(content string) bool {
	return passReplyRe.MatchString(content)
}

// parseMentionedAgentIDs 从回复文本解析 @展示名 提及并映射为 agentID（按参会顺序去重，排除自己）。
// participants 为空或无有效匹配时返回 nil。
func parseMentionedAgentIDs(content string, participants []*types.MeetingParticipant, selfID string) []string {
	if content == "" || len(participants) == 0 {
		return nil
	}
	matches := mentionTokenRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		names[m[1]] = struct{}{}
	}
	var out []string
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" || p.AgentID == selfID || p.DisplayName == "" {
			continue
		}
		if _, ok := names[p.DisplayName]; ok {
			out = append(out, p.AgentID)
		}
	}
	return out
}

// meetingSpeaker 一个发言任务：agent 回应 replyTo 这条消息
type meetingSpeaker struct {
	agent   *types.CustomAgent
	replyTo *types.MeetingMessage
}

// runAgentTurns 一轮发言（所有发言者回应同一 trigger）；保留既有签名供既有测试/调用复用
func (e *meetingEngine) runAgentTurns(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, speakers []*types.CustomAgent) []*types.MeetingMessage {
	tasks := make([]meetingSpeaker, 0, len(speakers))
	for _, a := range speakers {
		tasks = append(tasks, meetingSpeaker{agent: a, replyTo: trigger})
	}
	return e.runSpeakerTasks(ctx, room, tasks, nil)
}

// runSpeakerTasks 按广播模式执行一组发言任务，返回成功落库的 agent 消息（顺序确定）
func (e *meetingEngine) runSpeakerTasks(ctx context.Context, room *types.MeetingRoom, tasks []meetingSpeaker, participants []*types.MeetingParticipant) []*types.MeetingMessage {
	if shouldRunParallel(room.BroadcastMode, len(tasks)) {
		if ctx.Err() != nil {
			return nil // ctx 已取消时不再启动并发发言
		}
		slots := make([]*types.MeetingMessage, len(tasks))
		var wg sync.WaitGroup
		for i, task := range tasks {
			wg.Add(1)
			go func(i int, task meetingSpeaker) {
				defer wg.Done()
				msg, err := e.runAgentTurn(ctx, room, task.replyTo, task.agent, participants)
				if errors.Is(err, errAgentPass) {
					return // 弃权：静默跳过，不计失败、不提示
				}
				if err != nil {
					e.handleSpeakFailure(ctx, room, task.agent, err)
					return
				}
				slots[i] = msg // 各 goroutine 写不同下标，无数据竞争
			}(i, task)
		}
		wg.Wait()
		return compactMessages(slots)
	}
	// sequential：严格按顺序逐个发言，每个发言前检查打断标记
	out := make([]*types.MeetingMessage, 0, len(tasks))
	for _, task := range tasks {
		if e.isInterrupted(ctx, room.ID) {
			logger.Infof(ctx, "meeting turn interrupted. room=%s", room.ID)
			return out // 打断收尾（系统提示 + 主持人收敛）统一由 RunTurn 处理
		}
		msg, err := e.runAgentTurn(ctx, room, task.replyTo, task.agent, participants)
		if errors.Is(err, errAgentPass) {
			continue // 弃权：静默跳过，不计失败、不提示
		}
		if err != nil {
			e.handleSpeakFailure(ctx, room, task.agent, err)
			continue
		}
		out = append(out, msg)
	}
	return out
}

// compactMessages 去掉 nil 槽位，保持成功消息相对顺序
func compactMessages(in []*types.MeetingMessage) []*types.MeetingMessage {
	out := make([]*types.MeetingMessage, 0, len(in))
	for _, m := range in {
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

// planReplyRound 依据本轮消息的提及关系规划下一子轮：被提及者各自回应"最早提及它的那条消息"。
// 无任何提及 → nil，表示讨论自然终止。
func planReplyRound(created []*types.MeetingMessage, participants []*types.MeetingParticipant, agentsByID map[string]*types.CustomAgent) []meetingSpeaker {
	targets := make(map[string]*types.MeetingMessage, len(participants))
	for _, m := range created {
		for _, id := range m.MentionedAgentIDs {
			if _, seen := targets[id]; !seen {
				targets[id] = m // 仅记最早一条，保证确定性
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	out := make([]meetingSpeaker, 0, len(targets))
	for _, p := range participants { // 按参会顺序，保证确定性
		if p.Kind != types.ParticipantKindAgent {
			continue
		}
		target, ok := targets[p.AgentID]
		if !ok {
			continue
		}
		if agent := agentsByID[p.AgentID]; agent != nil {
			out = append(out, meetingSpeaker{agent: agent, replyTo: target})
		}
	}
	return out
}

// runDiscussion 执行一轮完整讨论：首轮由 selectSpeakers 决定的发言者回应触发消息，
// 之后按"提及链"展开子轮，直至自然终止（无提及）/达到护栏上限/用户打断/裁判判定收敛。
// 返回 true 表示裁判判定讨论已收敛（调用方据此终止本轮且不再消化排队输入）。
func (e *meetingEngine) runDiscussion(
	ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage,
	speakers, allAgents []*types.CustomAgent, participants []*types.MeetingParticipant,
) bool {
	agentsByID := make(map[string]*types.CustomAgent, len(allAgents))
	for _, a := range allAgents {
		agentsByID[a.ID] = a
	}
	tasks := make([]meetingSpeaker, 0, len(speakers))
	for _, a := range speakers {
		tasks = append(tasks, meetingSpeaker{agent: a, replyTo: trigger})
	}
	maxRounds := e.guardMaxExchange(room)         // 子轮硬上限：防 LLM 无限互相回应
	everyN := room.HostEveryNRounds               // 主持人介入间隔：0 表示不介入
	consensusOn := room.ConsensusDetectionEnabled // 裁判 + 共识检测开关
	completed := 0                                // 已完成的子轮数（用于每 N 轮介入节流）
	for round := 0; round < maxRounds && len(tasks) > 0; round++ {
		created := e.runSpeakerTasks(ctx, room, tasks, participants)
		completed++
		if e.isInterrupted(ctx, room.ID) {
			return false // 打断收尾统一由 RunTurn 处理
		}
		// 裁判 + 共识检测：每子轮结束交由独立裁判判定收敛/相关度/目标覆盖度。
		// 启用议程时以"当前子议题覆盖"为收敛依据，收敛后推进议程；否则沿用整场收敛。
		// 注意：即使本轮没有任何 @ 提及（tasks 将在下面变为空），也要判定一次，
		// 否则启用议程时最后一个子轮永远无法被标记覆盖。
		if (consensusOn || room.AgendaEnabled) && e.detectAndPublishConsensus(ctx, room, completed) {
			return true
		}
		tasks = planReplyRound(created, participants, agentsByID)
		if len(tasks) == 0 {
			return false // 无后续回应，讨论自然终止
		}
		// 每 N 轮介入：仅当讨论仍会继续时插入「阶段总结 + 重定向」，防止跑偏
		if shouldHostIntervene(everyN, completed) {
			e.publishHostIntervention(ctx, room, completed)
		}
	}
	return false
}

// detectAndPublishConsensus 每子轮结束调独立裁判判定状态。
// 收敛（或议程项目标已覆盖）时落共识/小结消息并广播，返回 true 表示本轮讨论应终止。
// 判定为漂移时由主持人生成一次重定向提示，但不终止讨论。
func (e *meetingEngine) detectAndPublishConsensus(ctx context.Context, room *types.MeetingRoom, completed int) bool {
	// 判定范围：启用议程时只看本议题记录（结构性治理的核心），否则看全房间最近消息
	var recent []*types.MeetingMessage
	var item *types.MeetingAgendaItem
	var items []*types.MeetingAgendaItem
	var err error
	if room.AgendaEnabled && e.agendaRepo != nil {
		items, item = e.ensureActiveAgenda(ctx, room)
		if item != nil {
			recent, err = e.messageRepo.ListByAgendaItem(ctx, room.ID, item.ID, meetingAgendaContextMax*2)
		} else {
			recent, err = e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
		}
	} else {
		recent, err = e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
	}
	if err != nil {
		logger.Warnf(ctx, "meeting referee list messages failed, room=%s err=%v", room.ID, err)
		return false
	}
	verdict := e.judgeConsensus(ctx, room, item, recent, completed)

	// 漂移：只做一次重定向，不终止讨论（避免误判打断有效分歧）。
	// 房间最后一条已是主持人消息时不再重复提醒，防止每轮都刷同一条纠偏。
	if verdict.Drift && !verdict.Converged && !e.lastRoomMessageIsHost(ctx, room.ID) {
		itemID := ""
		if item != nil {
			itemID = item.ID
		}
		e.publishHostNote(ctx, room.ID, itemID, e.generateRedirection(ctx, room, item, recent))
	}

	// 收敛判定：有子议题时以"目标覆盖"为准，否则看整体是否收敛
	settled := verdict.Converged
	if item != nil && item.Goal != "" {
		settled = verdict.GoalCovered || verdict.Converged
	}
	if !settled {
		return false
	}

	// 有子议题：落议题小结、推进议程，并决定是否结束本轮（后续议题需要新一轮讨论）
	if item != nil {
		remaining := pendingAgenda(items, item.ID)
		if msg, merr := e.appendConsensusMessage(ctx, room.ID, agendaRecap(item, remaining, verdict)); merr == nil {
			e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventConsensusCreated, Data: map[string]interface{}{
				"room_id": room.ID, "message": msg, "agenda_item_id": item.ID,
				"reason": verdict.Reason, "completed_rounds": completed,
			}})
		}
		hasNext := e.advanceAgenda(ctx, room, item, firstNonEmpty(verdict.Consensus, verdict.Reason))
		e.publishAgendaUpdate(ctx, room.ID)
		// 返回 true：结束本议题的讨论轮（下一议题由 RunTurn 自动开题）
		return hasNext || !verdict.Converged
	}

	msg, err := e.appendConsensusMessage(ctx, room.ID, consensusMessageContent(room, verdict))
	if err != nil {
		logger.Warnf(ctx, "meeting consensus message failed, room=%s err=%v", room.ID, err)
		return false
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventConsensusCreated, Data: map[string]interface{}{
		"room_id": room.ID, "message": msg,
		"reason": verdict.Reason, "completed_rounds": completed,
	}})
	return true
}

// pendingAgenda 返回除当前项外仍未覆盖的议程项
func pendingAgenda(items []*types.MeetingAgendaItem, currentID string) []*types.MeetingAgendaItem {
	out := make([]*types.MeetingAgendaItem, 0, len(items))
	for _, it := range items {
		if it.ID == currentID {
			continue
		}
		if it.Status == types.MeetingAgendaPending || it.Status == types.MeetingAgendaActive {
			out = append(out, it)
		}
	}
	return out
}

// publishAgendaUpdate 广播议程最新状态（推进/重拆后前端刷新）
func (e *meetingEngine) publishAgendaUpdate(ctx context.Context, roomID string) {
	if e.agendaRepo == nil {
		return
	}
	items, err := e.agendaRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventAgendaUpdated, Data: map[string]interface{}{
		"room_id": roomID, "items": items,
	}})
}

// shouldHostIntervene 判断完成 completed 个子轮后是否需要主持人介入：
// everyN<=0 表示关闭；每完成 everyN 轮介入一次。
func shouldHostIntervene(everyN, completed int) bool {
	return everyN > 0 && completed > 0 && completed%everyN == 0
}

// publishHostIntervention 每 N 轮介入：主持人生成阶段总结/重定向消息并落库广播。
// 主持人模型不可用时降级为固定文案；消息经 message.created 回流，并自然进入后续子轮的共享上下文。
func (e *meetingEngine) publishHostIntervention(ctx context.Context, room *types.MeetingRoom, completed int) {
	// 启用议程时只看本议题记录，避免介入总结被其他议题的讨论带偏；
	// 消息本身归属该议题，保证下一轮智能体能读到纠偏要求。
	var recent []*types.MeetingMessage
	var active *types.MeetingAgendaItem
	var err error
	if room.AgendaEnabled && e.agendaRepo != nil {
		if _, active = e.ensureActiveAgenda(ctx, room); active != nil {
			recent, err = e.messageRepo.ListByAgendaItem(ctx, room.ID, active.ID, meetingAgendaContextMax*2)
		}
	}
	if recent == nil && err == nil {
		recent, err = e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
	}
	if err != nil {
		logger.Warnf(ctx, "meeting host intervention list messages failed, room=%s err=%v", room.ID, err)
		recent = nil
	}
	content := e.generateInterventionMessage(ctx, room, recent, completed)
	itemID := ""
	if active != nil {
		itemID = active.ID
	}
	if _, err := e.appendHostMessageForItem(ctx, room.ID, itemID, content); err != nil {
		logger.Warnf(ctx, "meeting host intervention message failed, room=%s err=%v", room.ID, err)
	}
}

// handleSpeakFailure 统一处理单个智能体发言失败：记录日志并落库/广播一条系统提示
func (e *meetingEngine) handleSpeakFailure(ctx context.Context, room *types.MeetingRoom, agent *types.CustomAgent, err error) {
	logger.Errorf(ctx, "[meeting] agent %s speak failed: %v", agent.ID, err)
	e.publishSystemMessage(ctx, room.ID, fmt.Sprintf("「%s」发言失败，已跳过", agent.Name))
}

// Interrupt 强制结束当前讨论轮（释放锁由引擎下一轮进入时自行处理，这里置打断标记）
func (e *meetingEngine) Interrupt(ctx context.Context, roomID string) error {
	return e.redis.Set(ctx, interruptKey(roomID), "1", 10*time.Minute).Err()
}

// IsTurnRunning 查询讨论轮锁是否被持有（无 Redis 的单测/降级环境按"无进行中讨论"处理）
func (e *meetingEngine) IsTurnRunning(ctx context.Context, roomID string) (bool, error) {
	if e.redis == nil {
		return false, nil
	}
	n, err := e.redis.Exists(ctx, turnLockKey(roomID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsArtifactRunning 查询房间产物生成锁是否被持有（无 Redis 环境按"未在生成"处理）
func (e *meetingEngine) IsArtifactRunning(ctx context.Context, roomID string) (bool, error) {
	if e.redis == nil {
		return false, nil
	}
	n, err := e.redis.Exists(ctx, artifactLockKey(roomID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (e *meetingEngine) isInterrupted(ctx context.Context, roomID string) bool {
	n, err := e.redis.Exists(ctx, interruptKey(roomID)).Result()
	if err != nil {
		// fail-open：Redis 故障时按"未打断"处理，仅告警便于观测
		logger.Warnf(ctx, "check meeting interrupt flag failed, room=%s err=%v", roomID, err)
		return false
	}
	return n > 0
}

// runAgentTurn 单个智能体发言：回应 replyTo 这条消息；解析回复中的 @ 提及写入 MentionedAgentIDs
// agent 为已加载的智能体对象（只读）
func (e *meetingEngine) runAgentTurn(ctx context.Context, room *types.MeetingRoom, replyTo *types.MeetingMessage, agent *types.CustomAgent, participants []*types.MeetingParticipant) (*types.MeetingMessage, error) {
	meetingAgent := e.cloneAgentForMeeting(agent, room)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)

	query, agendaItemID, kbIDs := e.buildTurnQuery(ctx, room, replyTo, participants, kbIDs)

	answer, err := e.runAgentOnce(ctx, room, meetingAgent, query, kbIDs)
	if err != nil {
		return nil, err
	}
	// 弃权：智能体以 PASS 表示"无信息增量"，静默跳过（不落库、不广播、不占用讨论产出）
	if isPassReply(answer) {
		logger.Infof(ctx, "[meeting] agent %s passed (room=%s, reply_to=%s)", agent.ID, room.ID, replyTo.ID)
		return nil, errAgentPass
	}
	// 发言准入：结构性治理下先判定是否围绕当前议题，未通过时要求重写一次（不丢弃）
	answer, admissionState, admissionNote := e.admitAnswer(ctx, room, agendaItemID, agent, answer)
	// 重写后智能体可能自愿弃权：同样按 PASS 静默跳过
	if isPassReply(answer) {
		logger.Infof(ctx, "[meeting] agent %s passed after rewrite (room=%s)", agent.ID, room.ID)
		return nil, errAgentPass
	}

	msg := &types.MeetingMessage{
		MeetingRoomID:     room.ID,
		SenderKind:        types.MeetingSenderAgent,
		SenderID:          agent.ID,
		ReplyToMessageID:  replyTo.ID,
		MentionedAgentIDs: types.StringList(parseMentionedAgentIDs(answer, participants, agent.ID)),
		Content:           answer,
		AgendaItemID:      agendaItemID,
		ReviewState:       types.ReviewStateNone,
		AdmissionState:    admissionState,
		AdmissionNote:     admissionNote,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}

// buildTurnQuery 组装本轮发言 query，并返回该发言归属的议程项 ID（未启用议程时为空）与生效的知识库范围。
// 启用议程时只带本议题记录 + 其他议题小结；否则退回全房间最近 contextMax 条。
func (e *meetingEngine) buildTurnQuery(
	ctx context.Context, room *types.MeetingRoom, replyTo *types.MeetingMessage,
	participants []*types.MeetingParticipant, kbIDs []string,
) (query string, agendaItemID string, effectiveKBs []string) {
	if !room.AgendaEnabled || e.agendaRepo == nil {
		contextMsgs, err := e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
		if err != nil {
			logger.Warnf(ctx, "meeting context load failed, room=%s err=%v", room.ID, err)
			contextMsgs = nil
		}
		return buildMeetingQuery(room, replyTo.Content, contextMsgs, participants), "", kbIDs
	}
	items, active := e.ensureActiveAgenda(ctx, room)
	if active == nil {
		// 议程已全部覆盖：仍按主题作答，不再注入子议题上下文
		return buildMeetingQuery(room, replyTo.Content, nil, participants), "", kbIDs
	}
	itemMsgs, err := e.messageRepo.ListByAgendaItem(ctx, room.ID, active.ID, meetingAgendaContextMax)
	if err != nil {
		logger.Warnf(ctx, "meeting agenda context load failed, room=%s item=%s err=%v", room.ID, active.ID, err)
		itemMsgs = nil
	}
	// 议题级知识库优先：子议题指定了 KB 就在其范围内检索，否则沿用会议级/智能体级并集
	if len(active.KnowledgeBaseIDs) > 0 {
		kbIDs = mergeUnique(kbIDs, active.KnowledgeBaseIDs)
	}
	return buildAgendaQuery(room, active, items, replyTo.Content, itemMsgs, participants), active.ID, kbIDs
}

// runAgentOnce 执行一次 AgentQA 调用并收集最终答复
func (e *meetingEngine) runAgentOnce(
	ctx context.Context, room *types.MeetingRoom, meetingAgent *types.CustomAgent, query string, kbIDs []string,
) (string, error) {
	eventBus := event.NewEventBus()
	var answer strings.Builder
	var runErr error
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		answer.WriteString(data.Content)
		return nil
	})
	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			runErr = fmt.Errorf("stage=%s err=%v", data.Stage, data.Error)
		}
		return nil
	})

	req := &types.QARequest{
		Session:            &types.Session{ID: uuid.New().String(), TenantID: room.TenantID}, // 内存态会话：AgentQA 不落库消息
		Query:              query,
		AssistantMessageID: uuid.New().String(),
		CustomAgent:        meetingAgent,
		KnowledgeBaseIDs:   kbIDs,
		// 会议级联网总闸：与 meetingAgent.Config.WebSearchEnabled 同时为真时联网工具才注册
		WebSearchEnabled: room.WebSearchEnabled,
		// 聚焦约束对全部参会智能体一致生效（追加到系统提示词尾部，不替换平台默认提示词）
		RunNote: meetingFocusSystemPrompt,
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	return answer.String(), nil
}

// mergeUnique 合并两个 ID 列表并去重保序
func mergeUnique(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, id := range append(append([]string{}, base...), extra...) {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// publishSystemMessage 系统提示消息（失败提示、护栏提示等）
func (e *meetingEngine) publishSystemMessage(ctx context.Context, roomID string, content string) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderSystem, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err == nil {
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	} else {
		logger.Warnf(ctx, "publish meeting system message failed, room=%s err=%v", roomID, err)
	}
}

// appendHostMessage 落库 sender_kind=host 消息并广播 message.created（护栏收敛/M4 方案生成复用）
func (e *meetingEngine) appendHostMessage(ctx context.Context, roomID string, content string) (*types.MeetingMessage, error) {
	return e.appendHostMessageForItem(ctx, roomID, "", content)
}

// appendHostMessageForItem 与 appendHostMessage 同源，但把消息归属到指定议程项。
// 议题内的主持人引导（重定向、阶段总结）必须带 agenda_item_id，
// 否则按议题取上下文时会被过滤掉，智能体下一轮就看不到纠偏要求。
func (e *meetingEngine) appendHostMessageForItem(ctx context.Context, roomID, agendaItemID, content string) (*types.MeetingMessage, error) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderHost,
		AgendaItemID: agendaItemID, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}

// appendConsensusMessage 落库 sender_kind=host 且 is_consensus=true 的共识消息并广播 message.created
func (e *meetingEngine) appendConsensusMessage(ctx context.Context, roomID, content string) (*types.MeetingMessage, error) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderHost, Content: content, IsConsensus: true,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}

// handleInterrupt 用户打断（"回到正题"）的收尾：落系统提示 + 主持人生成收敛消息并广播 guard.triggered
func (e *meetingEngine) handleInterrupt(ctx context.Context, room *types.MeetingRoom, roomID string) {
	e.publishSystemMessage(ctx, roomID, "讨论已被用户打断")
	recent, err := e.messageRepo.ListByRoom(ctx, roomID, "", e.contextMax)
	if err != nil {
		logger.Warnf(ctx, "meeting interrupt list messages failed, room=%s err=%v", roomID, err)
		recent = nil
	}
	content := e.generateInterruptMessage(ctx, room, recent)
	hostMsg, herr := e.appendHostMessage(ctx, roomID, content)
	if herr != nil {
		logger.Warnf(ctx, "meeting interrupt host message failed, room=%s err=%v", roomID, herr)
		return
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventGuardTriggered, Data: map[string]interface{}{
		"reason": "user_interrupt", "message": hostMsg,
	}})
}

// publishHostNote 落库一条主持人提示并广播；agendaItemID 非空时归属该议程项，
// 使其进入按议题取上下文（否则纠偏提示对智能体不可见）。失败仅告警不阻断。
func (e *meetingEngine) publishHostNote(ctx context.Context, roomID, agendaItemID, content string) {
	if _, err := e.appendHostMessageForItem(ctx, roomID, agendaItemID, content); err != nil {
		logger.Warnf(ctx, "publish meeting host note failed, room=%s err=%v", roomID, err)
	}
}

// lastRoomMessageIsHost 判断房间最后一条消息是否为主持人消息（用于避免连续刷纠偏提示）。
// 查询失败按"不是"处理，宁可多发一次提示也不静默吞掉纠偏。
func (e *meetingEngine) lastRoomMessageIsHost(ctx context.Context, roomID string) bool {
	last, err := e.messageRepo.ListByRoom(ctx, roomID, "", 1)
	if err != nil || len(last) == 0 {
		return false
	}
	return last[len(last)-1].SenderKind == types.MeetingSenderHost
}

// agentDisplayName 参会者列表反查展示名，查不到回退 agentID 前 8 位
func (e *meetingEngine) agentDisplayName(ctx context.Context, roomID string, agentID string) string {
	ps, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err == nil {
		for _, p := range ps {
			if p.AgentID == agentID && p.DisplayName != "" {
				return p.DisplayName
			}
		}
	}
	if len(agentID) > 8 {
		return agentID[:8]
	}
	return agentID
}

// guardMaxExchange 护栏互呛来回上限：优先房间配置，其次全局配置，最后回退 3
func (e *meetingEngine) guardMaxExchange(room *types.MeetingRoom) int {
	if room != nil && room.GuardMaxExchange > 0 {
		return room.GuardMaxExchange
	}
	if e.cfg != nil && e.cfg.Meeting != nil && e.cfg.Meeting.GuardMaxExchange > 0 {
		return e.cfg.Meeting.GuardMaxExchange
	}
	return 3
}

// guardWindowSize 护栏回看窗口：来回上限*2 + 余量（够判定即可，避免全量拉取）
func (e *meetingEngine) guardWindowSize(room *types.MeetingRoom) int {
	return e.guardMaxExchange(room)*2 + 4
}

// clearInterrupt 清除打断标记（本轮结束/进入新一轮时调用）
func (e *meetingEngine) clearInterrupt(ctx context.Context, roomID string) {
	if err := e.redis.Del(ctx, interruptKey(roomID)).Err(); err != nil {
		logger.Warnf(ctx, "clear meeting interrupt flag failed, room=%s err=%v", roomID, err)
	}
}

// ---- 纯逻辑函数（单测覆盖）----

// effectiveTopic 讨论主题的唯一解析入口：TopicBrief（主持人澄清收敛）优先，
// 其次 Title；Description 只作补充描述单独输出。
// 所有治理环节（选人/介入/裁判/方案/产物）都必须经此取值，避免各写一份 room.Title
// 导致「收敛后的主题只对发言生效、对其余环节失效」。
func effectiveTopic(room *types.MeetingRoom) string {
	if room == nil {
		return ""
	}
	if t := strings.TrimSpace(room.TopicBrief); t != "" {
		return t
	}
	return strings.TrimSpace(room.Title)
}

// cloneAgentForMeeting 克隆智能体配置：关闭多轮（会议上下文经 query 注入）、清除会话级沙箱配置，
// 并按会议级配置强制联网总闸、追加会议聚焦约束。
//
// 清空 SandboxConfigID 的原因：会议发言运行在内存态会话上（见 runAgentTurn 的伪造 Session），
// sessions 表中并不存在该会话行；而具名沙箱（Cube/E2B/Docker）在首次解析时会执行
// pinner.Pin 写入 sessions.sandbox_config_id，会话行缺失即返回 record not found，
// 直接导致该智能体发言失败。会议内无持久会话，也就不存在可归属的会话级沙箱，
// 因此这里统一不启用（与"未选择沙箱配置"的智能体行为一致）。
func (e *meetingEngine) cloneAgentForMeeting(agent *types.CustomAgent, room *types.MeetingRoom) *types.CustomAgent {
	clone := *agent
	cfg := agent.Config // 值拷贝
	cfg.MultiTurnEnabled = false
	cfg.HistoryTurns = 0
	// 会议级 KB 由引擎注入 req.KnowledgeBaseIDs；避免"仅@时检索"吞掉注入的 KB
	cfg.RetrieveKBOnlyWhenMentioned = false
	cfg.SandboxConfigID = ""
	// 会议级联网总闸：强制参会智能体启用联网检索（AgentQA 侧还需 req.WebSearchEnabled 同为真）
	cfg.WebSearchEnabled = room.WebSearchEnabled
	// 注意：会议聚焦约束不再写进 SystemPrompt。
	// 该字段一旦非空会被 AgentQA 当作"自定义提示词"整体替换平台 base 提示词，
	// 导致只有本来就配了自定义提示词的智能体才受约束（默认提示词的智能体完全绕开）。
	// 统一改由 runAgentTurn 的 req.RunNote 追加到系统提示词末尾，对所有智能体一致生效。
	clone.Config = cfg
	return &clone
}

// meetingFocusSystemPrompt 会议聚焦系统约束：围绕当前议题作答、对他人回应从简、无增量弃权。
// 通过 QARequest.RunNote 注入（追加到系统提示词尾部），不替换平台 base 提示词。
const meetingFocusSystemPrompt = "【会议聚焦要求】你正在参加一场多人会议，请严格遵守：\n" +
	"1. 所有发言必须围绕【当前子议题】展开；有子议题时不得讨论其他子议题或主题外内容；\n" +
	"2. 核心观点不超过 3 条，直接给出结论与理由，不做发散；\n" +
	"3. 回应其他参会人时最多 2 条、每条不超过 30 字，并直接 @对方姓名；\n" +
	"4. 若相较已有讨论没有新增观点（无信息增量），仅回复 PASS，不要输出其他内容。"

// unionKnowledgeBaseIDs 双层并集：智能体 KB ∪ 会议级 KB
// all 模式返回 nil（AgentQA 回退为智能体全部 KB，天然包含会议级 KB）
func unionKnowledgeBaseIDs(mode string, agentKBs []string, roomKBs []string) []string {
	switch mode {
	case "all":
		return nil
	case "none":
		return append([]string{}, roomKBs...)
	default: // "selected" 及空值
		seen := make(map[string]struct{}, len(agentKBs)+len(roomKBs))
		out := make([]string, 0, len(agentKBs)+len(roomKBs))
		for _, id := range append(append([]string{}, agentKBs...), roomKBs...) {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		return out
	}
}

// filterAgentParticipants 取 agent 类参会者（按 order_index 已排序）
func filterAgentParticipants(ps []*types.MeetingParticipant) []*types.MeetingParticipant {
	out := make([]*types.MeetingParticipant, 0, len(ps))
	for _, p := range ps {
		if p.Kind == types.ParticipantKindAgent {
			out = append(out, p)
		}
	}
	return out
}

// selectSpeakers 按房间配置的路由模式选择发言人
//   - host-assign（默认，含旧数据空值）：消息显式 @ 时优先响应，否则由主持人 LLM 指派，均无果则全员兜底
//   - mention：仅 @ 命中的智能体发言；无人被 @ 时由主持人指派兜底，避免讨论冷场
//   - free：不筛选，全员自由抢麦
func (e *meetingEngine) selectSpeakers(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.CustomAgent) []*types.CustomAgent {
	// 候选不足 1 个时筛选无意义，直接返回（避免无谓调用主持人）
	if len(agents) <= 1 {
		return agents
	}
	switch room.RoutingMode {
	case types.MeetingRoutingFree:
		return agents
	case types.MeetingRoutingMention:
		if picked := filterByIDs(agents, trigger.MentionedAgentIDs); len(picked) > 0 {
			return picked
		}
		return e.assignSpeakersByHost(ctx, room, trigger, agents)
	default:
		// 显式 @ 优先（保持参会者顺序，保证确定性）
		if len(trigger.MentionedAgentIDs) > 0 {
			if picked := filterByIDs(agents, trigger.MentionedAgentIDs); len(picked) > 0 {
				return picked
			}
		}
		return e.assignSpeakersByHost(ctx, room, trigger, agents)
	}
}

// assignSpeakersByHost 主持人 LLM 按相关性指派发言人；不可用或未命中时回退全员
func (e *meetingEngine) assignSpeakersByHost(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.CustomAgent) []*types.CustomAgent {
	candidates := make([]speakerCandidate, len(agents))
	for i, a := range agents {
		candidates[i] = speakerCandidate{
			ID: a.ID, Name: a.Name, Description: a.Description,
			Skills:         agentSkillNames(a),
			KnowledgeScope: describeKnowledgeScope(a.Config),
		}
	}
	if ids := e.selectSpeakersByLLM(ctx, room, trigger, candidates); ids != nil {
		if picked := filterByIDs(agents, ids); len(picked) > 0 {
			return picked
		}
	}
	return agents
}

func filterByIDs(agents []*types.CustomAgent, ids []string) []*types.CustomAgent {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]*types.CustomAgent, 0, len(ids))
	for _, a := range agents {
		if want[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// buildMeetingQuery 把会议主题 + 共享上下文 + 参会名册 + 触发消息拼成单轮 query（MultiTurnEnabled=false）。
// 未启用议程时使用；启用议程走 buildAgendaQuery（按议题聚焦）。
func buildMeetingQuery(room *types.MeetingRoom, triggerContent string, contextMsgs []*types.MeetingMessage, participants []*types.MeetingParticipant) string {
	var b strings.Builder
	b.WriteString("【会议主题】" + effectiveTopic(room))
	if room.Description != "" {
		b.WriteString("\n【会议描述】" + room.Description)
	}
	if room.ContextSummary != "" {
		b.WriteString("\n【早期讨论摘要】" + room.ContextSummary)
	}
	// 参会智能体名册：仅列出可被 @ 的 agent 展示名（主持人不是 @ 目标，不列入）
	if names := collectAgentDisplayNames(participants); len(names) > 0 {
		b.WriteString("\n【参会智能体】" + strings.Join(names, "、"))
	}
	if len(contextMsgs) > 0 {
		b.WriteString("\n【讨论记录】")
		for _, m := range contextMsgs {
			// 跳过触发消息本身，最后单独强调
			if m.Content == triggerContent && m.SenderKind == types.MeetingSenderUser {
				continue
			}
			b.WriteString(fmt.Sprintf("\n%s: %s", senderLabel(m), m.Content))
		}
	}
	b.WriteString("\n【当前议题】" + triggerContent)
	// 聚焦软约束：针对主题作答、回应从简、无增量弃权，抑制相互讨论中的发散
	b.WriteString("\n请以你的身份与职责，严格围绕【会议主题】作答，不要引入与主题无关的内容：")
	b.WriteString("\n- 核心观点不超过 3 条，直接给出结论与理由，不做发散；")
	b.WriteString("\n- 若需赞同或反驳其他参会人，最多 2 条、每条不超过 30 字，并在该句中直接用 @对方姓名 提及；")
	b.WriteString("\n- 若相较上述讨论记录没有新增观点（无信息增量），请仅回复 PASS，不要输出任何其他内容。")
	return b.String()
}

// collectAgentDisplayNames 收集参会智能体的展示名（仅 agent 类参会者，去重并保持参会顺序）
func collectAgentDisplayNames(participants []*types.MeetingParticipant) []string {
	seen := make(map[string]struct{}, len(participants))
	names := make([]string, 0, len(participants))
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.DisplayName == "" {
			continue
		}
		if _, dup := seen[p.DisplayName]; dup {
			continue
		}
		seen[p.DisplayName] = struct{}{}
		names = append(names, p.DisplayName)
	}
	return names
}

func senderLabel(m *types.MeetingMessage) string {
	switch m.SenderKind {
	case types.MeetingSenderUser:
		return "用户"
	case types.MeetingSenderHost:
		return "主持人"
	case types.MeetingSenderSystem:
		return "系统"
	default:
		return "智能体(" + m.SenderID + ")"
	}
}
