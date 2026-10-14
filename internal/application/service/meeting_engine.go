package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

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
	agentRepo       interfaces.CustomAgentRepository
	sessionService  interfaces.SessionService
	modelService    interfaces.ModelService
	redis           *redis.Client
	broadcaster     *MeetingBroadcaster
	cfg             *config.Config
	contextMax      int // LLM 上下文消息上限（MEETING_CONTEXT_MAX_MESSAGES，默认 50）
	turnLockLease   time.Duration
}

func NewMeetingEngine(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
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
		messageRepo: messageRepo, agentRepo: agentRepo,
		sessionService: sessionService, modelService: modelService,
		redis:         redisClient,
		broadcaster:   NewMeetingBroadcaster(),
		cfg:           cfg,
		contextMax:    contextMax,
		turnLockLease: 5 * time.Minute,
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
		e.runDiscussion(ctx, room, in.trigger, in.speakers, in.agents, in.participants)
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
			"room_id": roomID, "trigger_message_id": current,
		}})

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

// mentionTokenRe 匹配 @某人（@ 后接连续的字母/数字/下划线/连字符/汉字）
var mentionTokenRe = regexp.MustCompile(`@([\p{L}\p{N}_\-]+)`)

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
			e.publishSystemMessage(ctx, room.ID, "讨论已被用户打断")
			return out
		}
		msg, err := e.runAgentTurn(ctx, room, task.replyTo, task.agent, participants)
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
// 之后按"提及链"展开子轮，直至自然终止（无提及）/达到护栏上限/用户打断。
func (e *meetingEngine) runDiscussion(
	ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage,
	speakers, allAgents []*types.CustomAgent, participants []*types.MeetingParticipant,
) {
	agentsByID := make(map[string]*types.CustomAgent, len(allAgents))
	for _, a := range allAgents {
		agentsByID[a.ID] = a
	}
	tasks := make([]meetingSpeaker, 0, len(speakers))
	for _, a := range speakers {
		tasks = append(tasks, meetingSpeaker{agent: a, replyTo: trigger})
	}
	maxRounds := e.guardMaxExchange(room) // 子轮硬上限：防 LLM 无限互相回应
	for round := 0; round < maxRounds && len(tasks) > 0; round++ {
		created := e.runSpeakerTasks(ctx, room, tasks, participants)
		if e.isInterrupted(ctx, room.ID) {
			return // sequential 分支已落库打断提示，此处仅停止继续展开子轮
		}
		tasks = planReplyRound(created, participants, agentsByID)
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
	meetingAgent := cloneAgentForMeeting(agent)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)

	// 共享上下文：最近 N 条（正序）
	contextMsgs, err := e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
	if err != nil {
		return nil, err
	}
	query := buildMeetingQuery(room, replyTo.Content, contextMsgs)

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
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, runErr
	}

	msg := &types.MeetingMessage{
		MeetingRoomID:     room.ID,
		SenderKind:        types.MeetingSenderAgent,
		SenderID:          agent.ID,
		ReplyToMessageID:  replyTo.ID,
		MentionedAgentIDs: types.StringList(parseMentionedAgentIDs(answer.String(), participants, agent.ID)),
		Content:           answer.String(),
		ReviewState:       types.ReviewStateNone,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
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
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderHost, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
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

// cloneAgentForMeeting 克隆智能体配置：关闭多轮（会议上下文经 query 注入），保持其余配置
func cloneAgentForMeeting(agent *types.CustomAgent) *types.CustomAgent {
	clone := *agent
	cfg := agent.Config // 值拷贝
	cfg.MultiTurnEnabled = false
	cfg.HistoryTurns = 0
	// 会议级 KB 由引擎注入 req.KnowledgeBaseIDs；避免"仅@时检索"吞掉注入的 KB
	cfg.RetrieveKBOnlyWhenMentioned = false
	clone.Config = cfg
	return &clone
}

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

// selectSpeakers 三级策略：@ 指定 > 主持人 LLM 筛选 > 全员
func (e *meetingEngine) selectSpeakers(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.CustomAgent) []*types.CustomAgent {
	// 候选不足 1 个时筛选无意义，直接返回（避免无谓调用主持人）
	if len(agents) <= 1 {
		return agents
	}
	// 第一级：消息显式 @ 了哪些智能体（保持参会者顺序，保证确定性）
	// 若 @ 的目标无有效命中（无效 id 或智能体已删除），降级到主持人筛选/全员
	if len(trigger.MentionedAgentIDs) > 0 {
		if picked := filterByIDs(agents, trigger.MentionedAgentIDs); len(picked) > 0 {
			return picked
		}
	}
	// 第二级：主持人 LLM 按相关性挑选
	candidates := make([]speakerCandidate, len(agents))
	for i, a := range agents {
		candidates[i] = speakerCandidate{ID: a.ID, Name: a.Name, Description: a.Description}
	}
	if ids := e.selectSpeakersByLLM(ctx, room, trigger, candidates); ids != nil {
		return filterByIDs(agents, ids)
	}
	// 第三级：全员
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

// buildMeetingQuery 把会议主题 + 共享上下文 + 触发消息拼成单轮 query（MultiTurnEnabled=false）
func buildMeetingQuery(room *types.MeetingRoom, triggerContent string, contextMsgs []*types.MeetingMessage) string {
	var b strings.Builder
	b.WriteString("【会议主题】" + room.Title)
	if room.Description != "" {
		b.WriteString("\n【会议描述】" + room.Description)
	}
	if room.ContextSummary != "" {
		b.WriteString("\n【早期讨论摘要】" + room.ContextSummary)
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
	b.WriteString("\n请以你的身份与职责，针对当前议题发表意见，简明扼要。若需赞同或反驳其他参会人，请在该句中直接用 @对方姓名 提及。")
	return b.String()
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
