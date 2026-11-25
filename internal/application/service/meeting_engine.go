package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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
	eventRepo       interfaces.MeetingEventRepository
	sessionService  interfaces.SessionService
	modelService    interfaces.ModelService
	kbService       interfaces.KnowledgeBaseService     // 产物生成：展开参会智能体的 all 模式知识库范围
	userService     interfaces.UserService              // 解析会议创建者用户名（供智能体 @用户名 向用户提问）
	temporaryDocs   interfaces.TemporaryDocumentService // 会议文件：读取用户上传文件并注入发言上下文
	redis           *redis.Client
	broadcaster     *MeetingBroadcaster
	cfg             *config.Config
	contextMax      int // LLM 上下文消息上限（MEETING_CONTEXT_MAX_MESSAGES，默认 50）
	turnLockLease   time.Duration
	// thinkDelay 发言前的短暂停顿，让节奏更像真人会议；nil 表示不等待（单测精简引擎）
	thinkDelay func() time.Duration

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
	eventRepo interfaces.MeetingEventRepository,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	userService interfaces.UserService,
	temporaryDocs interfaces.TemporaryDocumentService,
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
		eventRepo:      eventRepo,
		sessionService: sessionService, modelService: modelService,
		kbService:     kbService,
		userService:   userService,
		temporaryDocs: temporaryDocs,
		redis:         redisClient,
		broadcaster:   NewMeetingBroadcaster(),
		cfg:           cfg,
		contextMax:    contextMax,
		turnLockLease: 5 * time.Minute,
		thinkDelay: func() time.Duration {
			// 0.8~2.5 秒：足够让用户感知"正在思考"，又不至于拖慢整场会议
			return time.Duration(800+rand.Intn(1700)) * time.Millisecond
		},

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
	lastTrigger := triggerMessageID // 本轮实际消费到的最后一条触发消息（用户插话/议题推进都会前移）
	stopTurn := false               // 讨论是否已收束（由 runDiscussion 返回）
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
		// 每轮讨论的起止都写入事件表，构成回放的时间轴
		e.advancePhase(ctx, room, types.MeetingPhaseDiscussing, types.MeetingEventTurnStarted,
			types.MeetingSenderSystem, "", "讨论轮开始", map[string]any{"trigger_message_id": current})
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]interface{}{
			"room_id": roomID, "trigger_message_id": current,
		}})
		stopTurn, lastTrigger = e.runDiscussion(ctx, room, in.trigger, in.speakers, in.agents, in.participants)
		e.advancePhase(ctx, room, "", types.MeetingEventTurnEnded,
			types.MeetingSenderSystem, "", "讨论轮结束", map[string]any{"trigger_message_id": current, "stopped": stopTurn})
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
			"room_id": roomID, "trigger_message_id": current,
		}})

		// 讨论已收束（全部议题完成 / 整场共识 / 智能体在等用户回答）：结束本轮；
		// 但若用户恰好在收束瞬间插话（消息已落库、自己触发的 RunTurn 因锁被占用而直接返回），
		// 这里补拾一次，避免这条发言被吞掉。
		if stopTurn {
			if recent, rerr := e.messageRepo.ListByRoom(ctx, roomID, "", 50); rerr == nil {
				if next := extractQueuedTrigger(recent, lastTrigger); next != nil {
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
		// 自由抢麦模式下主持人不介入讨论过程，故跳过护栏（子轮仍有硬上限兜底）。
		if hostInDiscussion(room) {
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
		}

		// 拾取排队输入：从"本轮最后消费的触发消息"之后继续找，避免重复回应已处理过的用户消息
		next := e.queuedUserMessageAfter(ctx, roomID, lastTrigger)
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

// passReplyRe 判定弃权回复：整条回复除空白/标点/符号（含全角与 Markdown 强调符）外只剩 PASS
var passReplyRe = regexp.MustCompile(`(?i)^[\s\p{Z}\p{P}\p{S}]*pass[\s\p{Z}\p{P}\p{S}]*$`)

// isPassReply 判断智能体回复是否为弃权（PASS）。
// 采用整条严格匹配，避免把正文里偶然出现 "PASS" 字样的正常发言误判为弃权。
func isPassReply(content string) bool {
	return passReplyRe.MatchString(content)
}

// parseMentionedAgentIDs 从回复文本解析 @展示名 提及并映射为 agentID（按参会顺序去重，排除自己）。
// 直接用展示名做子串匹配：展示名可能含空格或标点（如 "AI 智能体专家"），
// 用正则按词边界切分会把名字截断，导致 @ 提及丢失。
func parseMentionedAgentIDs(content string, participants []*types.MeetingParticipant, selfID string) []string {
	if content == "" || len(participants) == 0 {
		return nil
	}
	var out []string
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" || p.AgentID == selfID || p.DisplayName == "" {
			continue
		}
		if strings.Contains(content, "@"+p.DisplayName) {
			out = append(out, p.AgentID)
		}
	}
	return out
}

// meetingSpeaker 一个发言任务：agent 回应 replyTo 这条消息。
// hint 为主持人点名时附带的具体问题（可为空），用于让接话有明确目的。
type meetingSpeaker struct {
	agent   *types.CustomAgent
	replyTo *types.MeetingMessage
	hint    string
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
				msg, err := e.runAgentTurnWithHint(ctx, room, task.replyTo, task.agent, participants, task.hint)
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
		msg, err := e.runAgentTurnWithHint(ctx, room, task.replyTo, task.agent, participants, task.hint)
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
// 之后按"@提及接力 + 未发言者补位"自由接话；用户可随时插话，智能体可 @用户 提问后暂停。
// 返回 (stop, lastTriggerID)：stop=true 表示讨论已收束（不再消费排队输入）；
// lastTriggerID 为最后消费的触发消息 ID，供调用方避免重复处理同一条用户消息。
func (e *meetingEngine) runDiscussion(
	ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage,
	speakers, allAgents []*types.CustomAgent, participants []*types.MeetingParticipant,
) (bool, string) {
	agentsByID := make(map[string]*types.CustomAgent, len(allAgents))
	for _, a := range allAgents {
		agentsByID[a.ID] = a
	}
	currentTrigger := trigger
	tasks := replyTasks(speakers, trigger)
	// 非议程沿用护栏阈值作为子轮上限；议程模式按"每议题上限 + 单轮总上限"控制，
	// 允许一个讨论轮自然推进完多个议题，而不是每题都等用户点「下一步」。
	maxRounds := e.guardMaxExchange(room)
	if room.AgendaEnabled {
		maxRounds = meetingMaxTotalSubRounds
	} else if isFreeMeeting(room) {
		// 自由模式没有议题推进与裁判收束，给足子轮让智能体相互讨论；
		// 用户每次发言会重新开启一轮，因此这里是单次触发内的硬上限。
		maxRounds = meetingFreeModeMaxSubRounds
	}
	everyN := room.HostEveryNRounds // 主持人介入间隔：0 表示不介入
	if !hostInDiscussion(room) {
		everyN = 0 // 自由抢麦：主持人不在讨论过程中介入（不做阶段总结）
	}

	completed := 0                  // 全局子轮数（介入节流与裁判上下文用）
	agendaRounds := 0               // 当前议题内的子轮数（进入下一议题后重置）
	spoken := make(map[string]bool) // 当前议题内已发过言的智能体（补位用）

	for round := 0; round < maxRounds; round++ {
		if len(tasks) == 0 {
			return false, currentTrigger.ID
		}
		created := e.runSpeakerTasks(ctx, room, tasks, participants)
		completed++
		agendaRounds++
		for _, m := range created {
			spoken[m.SenderID] = true
		}
		if e.isInterrupted(ctx, room.ID) {
			return false, currentTrigger.ID // 打断收尾统一由 RunTurn 处理
		}

		// 用户中途插话：立即切换到用户的新发言，被 @ 者优先回应（不必等整轮结束）
		if queued := e.queuedUserMessageAfter(ctx, room.ID, currentTrigger.ID); queued != nil {
			currentTrigger = queued
			agendaRounds = 0
			spoken = make(map[string]bool)
			tasks = replyTasks(e.selectSpeakers(ctx, room, queued, allAgents), queued)
			continue
		}

		// 智能体向用户提问（@用户）：暂停自动讨论，把发言权交还用户
		if askMsg := asksUserMessage(created); askMsg != nil {
			e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventAwaitingUser, Data: map[string]interface{}{
				"room_id": room.ID, "message": askMsg,
			}})
			return true, currentTrigger.ID
		}

		// 下一子轮：优先按 @ 提及接力
		tasks = planReplyRound(created, participants, agentsByID)
		// 没人被点名：规则筛候选 + 小模型选一人，并附一个具体问题。
		// 这样讨论不会因为"没人 @"而冷场，也不会退化成按参会顺序轮询。
		if len(tasks) == 0 && canAutoContinue(room) && agendaRounds < agendaRoundsPerTopic(len(allAgents)) {
			replyTo := lastMessage(created)
			if replyTo == nil {
				replyTo = currentTrigger
			}
			lastSpeakerID := ""
			if replyTo != nil && replyTo.SenderKind == types.MeetingSenderAgent {
				lastSpeakerID = replyTo.SenderID
			}
			if next, hint := e.chooseNextSpeaker(ctx, room, replyTo, allAgents, spoken, lastSpeakerID); next != nil {
				tasks = []meetingSpeaker{{agent: next, replyTo: replyTo, hint: hint}}
			}
		}

		// 结构性边界判定：议程模式在讨论自然停下或每两个子轮时判定一次；
		// 非议程模式仍按共识检测开关每轮判定，保持既有行为。
		shouldJudge := room.AgendaEnabled && (len(tasks) == 0 || agendaRounds%2 == 0)
		if !room.AgendaEnabled {
			shouldJudge = room.ConsensusDetectionEnabled
		}
		if shouldJudge {
			next, stop := e.evaluateDiscussion(ctx, room, completed)
			if stop {
				return true, currentTrigger.ID
			}
			if next != nil {
				// 当前议题收束且还有后续议题：主持人一句话过渡，自动继续讨论
				currentTrigger = next
				agendaRounds = 0
				spoken = make(map[string]bool)
				tasks = replyTasks(e.selectSpeakers(ctx, room, next, allAgents), next)
				continue
			}
		}

		if len(tasks) == 0 {
			return false, currentTrigger.ID // 讨论自然终止
		}
		// 每 N 轮介入：仅当讨论仍会继续时插入「阶段总结 + 重定向」
		if shouldHostIntervene(everyN, completed) {
			e.publishHostIntervention(ctx, room, completed)
		}
	}
	return false, currentTrigger.ID
}

// evaluateDiscussion 由独立裁判判定当前议题/整场是否可收束，并按结果决定下一步：
//   - stop=true：讨论收束（全部议题完成或整场达成共识），调用方结束本轮；
//   - next!=nil：当前议题完成且还有后续议题，返回主持人的开题消息，调用方据此继续讨论；
//   - (nil,false)：尚未收束，继续讨论。
func (e *meetingEngine) evaluateDiscussion(ctx context.Context, room *types.MeetingRoom, completed int) (*types.MeetingMessage, bool) {
	// 未启用议程：沿用"整场收敛"判定
	if !room.AgendaEnabled || e.agendaRepo == nil {
		recent, err := e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
		if err != nil {
			logger.Warnf(ctx, "meeting referee list messages failed, room=%s err=%v", room.ID, err)
			return nil, false
		}
		verdict := e.judgeConsensus(ctx, room, nil, recent, completed)
		if !verdict.Converged {
			return nil, false
		}
		msg, err := e.appendConsensusMessage(ctx, room.ID, consensusMessageContent(room, verdict))
		if err != nil {
			logger.Warnf(ctx, "meeting consensus message failed, room=%s err=%v", room.ID, err)
			return nil, false
		}
		e.advancePhase(ctx, room, "", types.MeetingEventConsensus, types.MeetingSenderHost, "", "讨论达成共识",
			map[string]any{"message_id": msg.ID, "reason": verdict.Reason, "completed_rounds": completed})
		e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventConsensusCreated, Data: map[string]interface{}{
			"room_id": room.ID, "message": msg,
			"reason": verdict.Reason, "completed_rounds": completed, "all_done": true,
		}})
		return nil, true
	}

	items, item := e.ensureActiveAgenda(ctx, room)
	if item == nil {
		return nil, true // 没有活跃议题（全部已完成）
	}
	recent, err := e.messageRepo.ListByAgendaItem(ctx, room.ID, item.ID, meetingAgendaContextMax*2)
	if err != nil {
		logger.Warnf(ctx, "meeting referee list messages failed, room=%s err=%v", room.ID, err)
		return nil, false
	}
	// 议题归属可能因历史数据/降级链路缺失：本议题没有记录时回退到房间最近消息，
	// 否则裁判会因拿不到讨论内容而永远判不出收敛。
	if len(recent) == 0 {
		recent, _ = e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
	}
	verdict := e.judgeConsensus(ctx, room, item, recent, completed)

	// 跑题：只提醒一句，不打断有效分歧（房间最后一条已是主持人消息时不重复刷屏）
	if verdict.Drift && !verdict.Converged && hostInDiscussion(room) && !e.lastRoomMessageIsHost(ctx, room.ID) {
		e.publishHostNote(ctx, room.ID, item.ID, e.generateRedirection(ctx, room, item, recent))
	}

	// 收敛判定：有子议题目标时以"目标覆盖"为准，否则看整体是否收敛
	settled := verdict.Converged
	if item.Goal != "" {
		settled = verdict.GoalCovered || verdict.Converged
	}
	if !settled {
		return nil, false
	}

	// 落议题小结；最后一个议题完成时直接汇总成"会议结论"，不再额外多一条收束提示。
	remaining := pendingAgenda(items, item.ID)
	conclusion := ""
	if len(remaining) == 0 {
		conclusion = buildMeetingConclusion(items, item, verdict)
	}
	recap := agendaRecap(item, remaining, verdict)
	if conclusion != "" {
		recap = conclusion
	}
	if msg, merr := e.appendConsensusMessageForItem(ctx, room.ID, item.ID, recap); merr == nil {
		e.advancePhase(ctx, room, "", types.MeetingEventConsensus, types.MeetingSenderHost, "", "议题收敛",
			map[string]any{"message_id": msg.ID, "agenda_item_id": item.ID, "reason": verdict.Reason})
		e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventConsensusCreated, Data: map[string]interface{}{
			"room_id": room.ID, "message": msg, "agenda_item_id": item.ID,
			"reason": verdict.Reason, "completed_rounds": completed, "all_done": len(remaining) == 0,
		}})
	}
	summary := firstNonEmpty(verdict.Consensus, verdict.Reason)
	if len(verdict.Unresolved) > 0 {
		summary += "；未解决：" + strings.Join(verdict.Unresolved, "；")
	}
	e.advanceAgenda(ctx, room, item, summary)
	e.publishAgendaUpdate(ctx, room.ID)

	if len(remaining) == 0 {
		// 全部议题完成：会议结论已自动落库，后续是否生成正式产物由用户在「下一步」决定
		return nil, true
	}

	// 自动进入下一议题：激活并落一条简短开题消息，讨论无缝继续
	nextItem := remaining[0]
	nextItem.Status = types.MeetingAgendaActive
	if uerr := e.agendaRepo.Update(ctx, nextItem); uerr != nil {
		logger.Warnf(ctx, "meeting agenda activate failed, room=%s item=%s err=%v", room.ID, nextItem.ID, uerr)
		return nil, true
	}
	e.publishAgendaUpdate(ctx, room.ID)
	msg, merr := e.appendHostMessageForItem(ctx, room.ID, nextItem.ID, agendaOpenContent(nextItem, items))
	if merr != nil {
		logger.Warnf(ctx, "meeting agenda open next failed, room=%s item=%s err=%v", room.ID, nextItem.ID, merr)
		return nil, true
	}
	return msg, false
}

// replyTasks 为一组智能体构造"回应同一条消息"的发言任务
func replyTasks(agents []*types.CustomAgent, replyTo *types.MeetingMessage) []meetingSpeaker {
	if replyTo == nil {
		return nil
	}
	out := make([]meetingSpeaker, 0, len(agents))
	for _, a := range agents {
		out = append(out, meetingSpeaker{agent: a, replyTo: replyTo})
	}
	return out
}

// lastMessage 取本轮最后一条成功落库的消息（补位接话的回应目标）
func lastMessage(msgs []*types.MeetingMessage) *types.MeetingMessage {
	if len(msgs) == 0 {
		return nil
	}
	return msgs[len(msgs)-1]
}

// queuedUserMessageAfter 查询触发消息之后是否有新的用户消息（用户中途插话）。
// 先在最近窗口中找；触发消息已滚出窗口时，退化为按创建时间比较最后一条用户消息。
func (e *meetingEngine) queuedUserMessageAfter(ctx context.Context, roomID, afterID string) *types.MeetingMessage {
	recent, err := e.messageRepo.ListByRoom(ctx, roomID, "", 50)
	if err != nil {
		return nil
	}
	if msg := extractQueuedTrigger(recent, afterID); msg != nil {
		return msg
	}
	after, err := e.messageRepo.GetByID(ctx, afterID)
	if err != nil || after == nil {
		return nil
	}
	for i := len(recent) - 1; i >= 0; i-- {
		m := recent[i]
		if m.SenderKind != types.MeetingSenderUser {
			continue // 跳过智能体/主持人消息，找最近一条用户消息
		}
		if m.ID == afterID {
			return nil
		}
		if !m.CreatedAt.IsZero() && !after.CreatedAt.IsZero() && m.CreatedAt.After(after.CreatedAt) {
			return m
		}
		return nil
	}
	return nil
}

// asksUserMessage 找出本轮中向用户提问的智能体消息；无则返回 nil。
// 优先读取落库时的 payload 标记（兼容 @用户名 与 @用户），旧消息回退文本匹配 @用户。
func asksUserMessage(msgs []*types.MeetingMessage) *types.MeetingMessage {
	for _, m := range msgs {
		if m.SenderKind != types.MeetingSenderAgent {
			continue
		}
		if len(m.Payload) > 0 {
			var payload struct {
				AsksUser bool `json:"asks_user"`
			}
			if json.Unmarshal(m.Payload, &payload) == nil && payload.AsksUser {
				return m
			}
		}
		if strings.Contains(m.Content, meetingAwaitUserToken) {
			return m
		}
	}
	return nil
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

// canAutoContinue 无人被 @ 时是否允许引擎自动选下一位接话。
// 新规则会议（启用议题治理）与自由模式启用；历史房间保持"没人 @ 就停"的原有节奏。
func canAutoContinue(room *types.MeetingRoom) bool {
	return room.AgendaEnabled || isFreeMeeting(room)
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
	return e.runAgentTurnWithHint(ctx, room, replyTo, agent, participants, "")
}

// runAgentTurnWithHint 单个智能体发言，并把主持人点名的具体问题注入 query。
// hint 为空时等价于普通的"回应上一条发言"。
func (e *meetingEngine) runAgentTurnWithHint(ctx context.Context, room *types.MeetingRoom, replyTo *types.MeetingMessage, agent *types.CustomAgent, participants []*types.MeetingParticipant, hint string) (*types.MeetingMessage, error) {
	meetingAgent := e.cloneAgentForMeeting(agent, room)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)

	// 会议创建者用户名：注入 query 供智能体 @用户名 称呼用户；取不到时退回通用 @用户
	userName := e.meetingUserName(ctx, room)
	query, agendaItemID, kbIDs := e.buildTurnQuery(ctx, room, replyTo, participants, kbIDs, userName)
	// 明确告知智能体自己的参会展示名：否则模型不知道自己是谁，容易在 @ 时点到自己
	if selfName := agentNamesByID(participants)[agent.ID]; selfName != "" {
		query += "\n【你的参会名】" + selfName + "（不要 @ 自己）"
	}
	if h := strings.TrimSpace(hint); h != "" {
		query += "\n【主持人点名】请优先回应这个问题：" + h
	}

	// 先广播"正在思考"并做一次短暂停顿，再发起真实生成：
	// 用户能看到谁准备发言，整场节奏也不会像接口批量返回。
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSpeakerThinking, Data: map[string]interface{}{
		"room_id": room.ID, "agent_id": agent.ID, "agent_name": agent.Name, "agenda_item_id": agendaItemID,
	}})
	e.waitThinkDelay(ctx)

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
	// 发言里 @用户 或 @用户名 表示在向用户提问：打上 payload 标记，前端据此提示"待你回应"，引擎暂停自动讨论
	if asksUserByAnswer(answer, userName) {
		if b, merr := json.Marshal(map[string]bool{"asks_user": true}); merr == nil {
			msg.Payload = b
		}
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
	participants []*types.MeetingParticipant, kbIDs []string, userName string,
) (query string, agendaItemID string, effectiveKBs []string) {
	// 会议文件资料前置注入：所有议题的发言都基于同一批用户上传文件
	attachmentCtx := e.meetingAttachmentContext(ctx, room, replyTo.Content)
	if !room.AgendaEnabled || e.agendaRepo == nil {
		contextMsgs, err := e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
		if err != nil {
			logger.Warnf(ctx, "meeting context load failed, room=%s err=%v", room.ID, err)
			contextMsgs = nil
		}
		return attachmentCtx + buildMeetingQuery(room, replyTo.Content, contextMsgs, participants, userName), "", kbIDs
	}
	items, active := e.ensureActiveAgenda(ctx, room)
	if active == nil {
		// 议程已全部覆盖：仍按主题作答，不再注入子议题上下文
		return attachmentCtx + buildMeetingQuery(room, replyTo.Content, nil, participants, userName), "", kbIDs
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
	return attachmentCtx + buildAgendaQuery(
		room, active, items, replyTo.Content, replyToSenderName(replyTo, agentNamesByID(participants)),
		itemMsgs, participants, userName,
	), active.ID, kbIDs
}

// meetingAttachmentContext 把本会议已解析完成的文件内容渲染为发言上下文。
// 按当前待回应发言选择相关片段，避免整份大文件灌满每次发言；未解析完成的文件只提示状态。
func (e *meetingEngine) meetingAttachmentContext(ctx context.Context, room *types.MeetingRoom, query string) string {
	if e.temporaryDocs == nil || room == nil {
		return ""
	}
	scope := meetingAttachmentScope(room.ID)
	docs, err := e.temporaryDocs.List(ctx, room.TenantID, scope)
	if err != nil || len(docs) == 0 {
		return ""
	}
	ids := make([]string, 0, len(docs))
	pending := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc.Status == types.TemporaryDocumentStatusReady {
			if len(ids) < types.MaxTemporaryAttachmentsPerMessage {
				ids = append(ids, doc.ID)
			}
			continue
		}
		if doc.Status == types.TemporaryDocumentStatusUploaded ||
			doc.Status == types.TemporaryDocumentStatusProcessing {
			pending = append(pending, doc.FileName)
		}
	}

	var b strings.Builder
	if len(ids) > 0 {
		result, rerr := e.temporaryDocs.ResolveForPrompt(ctx, room.TenantID, scope, ids, query)
		if rerr != nil {
			logger.Warnf(ctx, "meeting attachment resolve failed, room=%s err=%v", room.ID, rerr)
		} else if result != nil && len(result.Attachments) > 0 {
			b.WriteString("【会议文件资料】用户上传了以下文件，请优先基于文件内容讨论；引用时注明文件名。\n")
			for _, att := range result.Attachments {
				fmt.Fprintf(&b, "\n### 文件：%s", att.FileName)
				if att.ContentMode != types.AttachmentContentModeFull {
					b.WriteString("（已按当前议题选取相关片段）")
				}
				b.WriteString("\n<content>\n")
				b.WriteString(att.Content)
				b.WriteString("\n</content>\n")
			}
		}
	}
	if len(pending) > 0 {
		b.WriteString("【文件解析中】" + strings.Join(pending, "、") + "，暂不可读，请先基于其他信息讨论。\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "\n"
}

// meetingUserName 解析会议创建者的用户名：智能体可以用 @用户名 直接称呼用户。
// 解析失败返回空串，此时智能体仍可使用 @用户 的通用标记。
func (e *meetingEngine) meetingUserName(ctx context.Context, room *types.MeetingRoom) string {
	if e.userService == nil || room == nil || room.CreatedBy == "" {
		return ""
	}
	user, err := e.userService.GetUserByID(ctx, room.CreatedBy)
	if err != nil || user == nil {
		return ""
	}
	return strings.TrimSpace(user.Username)
}

// asksUserByAnswer 判断发言是否在向用户提问：兼容 @用户名 与通用 @用户 两种写法。
func asksUserByAnswer(content, userName string) bool {
	if strings.Contains(content, meetingAwaitUserToken) {
		return true
	}
	return userName != "" && strings.Contains(content, "@"+userName)
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

	// 聚焦约束对全部参会智能体一致生效（追加到系统提示词尾部，不替换平台默认提示词）；
	// 自由模式额外补充"主动向用户挖掘信息、相互讨论、服从用户收束"的协议。
	runNote := meetingFocusSystemPrompt
	if isFreeMeeting(room) {
		runNote += meetingFreeModeRunNote
	}
	req := &types.QARequest{
		Session:            &types.Session{ID: uuid.New().String(), TenantID: room.TenantID}, // 内存态会话：AgentQA 不落库消息
		Query:              query,
		AssistantMessageID: uuid.New().String(),
		CustomAgent:        meetingAgent,
		KnowledgeBaseIDs:   kbIDs,
		// 会议级联网总闸：与 meetingAgent.Config.WebSearchEnabled 同时为真时联网工具才注册
		WebSearchEnabled: room.WebSearchEnabled,
		RunNote:          runNote,
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	return answer.String(), nil
}

// waitThinkDelay 在发言前等待一小段随机时间；引擎未配置延迟（单测）时不等待。
func (e *meetingEngine) waitThinkDelay(ctx context.Context) {
	if e.thinkDelay == nil {
		return
	}
	d := e.thinkDelay()
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
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
	return e.appendConsensusMessageForItem(ctx, roomID, "", content)
}

// appendConsensusMessageForItem 同 appendConsensusMessage，但把共识/议题小结归属到指定议程项，
// 使后续议题按议题取上下文时能看到前面议题的结论。
func (e *meetingEngine) appendConsensusMessageForItem(ctx context.Context, roomID, agendaItemID, content string) (*types.MeetingMessage, error) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderHost,
		AgendaItemID: agendaItemID, Content: content, IsConsensus: true,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}

// handleInterrupt 用户打断（"回到正题"）的收尾：落系统提示 + 主持人生成收敛消息并广播 guard.triggered。
// 自由模式没有主持人，只落系统提示，把发言权直接交还用户。
func (e *meetingEngine) handleInterrupt(ctx context.Context, room *types.MeetingRoom, roomID string) {
	if isFreeMeeting(room) {
		e.publishSystemMessage(ctx, roomID, "讨论已被用户打断，请继续输入你的指示")
		return
	}
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
	if e.participantRepo == nil {
		return shortID(agentID)
	}
	ps, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err == nil {
		for _, p := range ps {
			if p.AgentID == agentID && p.DisplayName != "" {
				return p.DisplayName
			}
		}
	}
	return shortID(agentID)
}

// shortID 兜底展示：取 ID 前 8 位，避免空名/长 ID 影响主持人提示词。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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

// meetingFocusSystemPrompt 会议发言协议：像圆桌讨论一样短句接话，而不是写报告。
// 通过 QARequest.RunNote 注入（追加到系统提示词尾部），不替换平台 base 提示词。
const meetingFocusSystemPrompt = "【会议发言要求】你正在参加一场多人圆桌会议，请像现场发言一样交流，不要写成报告：\n" +
	"1. 常态 1-3 句话、不超过 120 字；被追问或关键论证可放宽到 200 字，不要每条都一样长；\n" +
	"2. 先给判断，再给一条核心理由；允许用一句短话附议，也允许直接追问；\n" +
	"3. 禁止使用标题、编号列表、表格、代码块；不要复述背景，也不要重复别人刚说过的内容；\n" +
	"4. 可以明确反对；不同意就说出反对点和能改变你立场的证据，不要为了和气而附和；\n" +
	"5. 如果你强烈不同意上一位，可以用「先打断一下」开头直接反驳并 @ 对方，这是允许的抢话；\n" +
	"6. 直接回应上一位发言者或用户；想让某位参会人接话，就在句末用 @对方姓名；只能 @ 其他参会人，不要 @ 自己；\n" +
	"7. 需要用户拍板或补充信息时，在句末 @用户姓名（用会议信息【用户】给出的名字；没有名字时写 @用户）提一个问题，然后停下等用户回答；\n" +
	"8. 所有发言围绕【当前子议题】；若相较已有讨论没有新增观点，仅回复 PASS。"

// meetingFreeModeRunNote 自由模式补充协议：没有主持人，智能体自行推进讨论并向用户挖掘信息。
const meetingFreeModeRunNote = "\n【自由讨论补充】本次会议没有主持人，由智能体自行推进：" +
	"请主动说明对议题的理解与假设，信息不足时向用户提出具体问题；" +
	"可以与其他参会智能体相互追问、补充或反驳；用户输入优先，" +
	"随时根据用户的最新指示纠正或收束讨论，不要等待主持人点名。"

// meetingAwaitUserToken 智能体向用户提问的显式标记（提示词协议约定）
const meetingAwaitUserToken = "@用户"

// meetingMaxTotalSubRounds 单次讨论轮的总子轮上限（跨议题累计），防止一次异步任务失控
const meetingMaxTotalSubRounds = 30

// meetingFreeModeMaxSubRounds 自由模式单次讨论轮的子轮上限：没有议题收束，给足轮次让智能体相互讨论
const meetingFreeModeMaxSubRounds = 8

const (
	meetingAgendaRoundsMin = 3
	meetingAgendaRoundsMax = 6
)

// agendaRoundsPerTopic 单个议题的子轮上限：随参会人数自适应，保证 5~6 人会议里每人都有机会发言。
func agendaRoundsPerTopic(agentCount int) int {
	if agentCount < meetingAgendaRoundsMin {
		return meetingAgendaRoundsMin
	}
	if agentCount > meetingAgendaRoundsMax {
		return meetingAgendaRoundsMax
	}
	return agentCount
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

// hostInDiscussion 讨论过程中主持人是否需要介入（阶段总结 / 互呛打断 / 偏离重定向）。
// 自由模式没有主持人；自由抢麦的语义同样是智能体自行争抢发言、不需要主持人调度，故一律不介入；
// 其余路由模式仍按房间配置生效（是否每 N 轮介入由 HostEveryNRounds 单独控制）。
func hostInDiscussion(room *types.MeetingRoom) bool {
	return !isFreeMeeting(room) && room.RoutingMode != types.MeetingRoutingFree
}

// selectSpeakers 选择本轮首位发言人：
//   - 显式 @ 优先（自由抢麦路由除外），@ 了几个就回应几个；
//   - 自由模式虽然是全员发言，但用户显式 @ 时仍优先点名对象，便于用户收束讨论；
//   - free 模式保留"全员自由抢麦"的历史语义；
//   - 其余情况只选一人：规则排除上一位发言者并优先未发言者，再由小模型选人并附具体问题。
//
// 后续子轮由 runDiscussion 用 chooseNextSpeaker 逐个接话，避免"一次点名多人、依次念稿"。
func (e *meetingEngine) selectSpeakers(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.CustomAgent) []*types.CustomAgent {
	if len(agents) == 0 {
		return nil
	}
	mentionFirst := room.RoutingMode != types.MeetingRoutingFree || isFreeMeeting(room)
	if mentionFirst && len(trigger.MentionedAgentIDs) > 0 {
		if picked := filterByIDs(agents, trigger.MentionedAgentIDs); len(picked) > 0 {
			return picked
		}
	}
	if room.RoutingMode == types.MeetingRoutingFree {
		return agents
	}
	// 历史房间（未启用议题治理）保留原有"首轮批量发言"语义，避免升级前行为突变；
	// 新会议默认启用议题治理，走下面的动态单人接话。
	if !room.AgendaEnabled {
		return agents
	}
	if len(agents) == 1 {
		return agents
	}
	if next, _ := e.chooseNextSpeaker(ctx, room, trigger, agents, nil, ""); next != nil {
		return []*types.CustomAgent{next}
	}
	return agents[:1]
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
func buildMeetingQuery(
	room *types.MeetingRoom, triggerContent string,
	contextMsgs []*types.MeetingMessage, participants []*types.MeetingParticipant, userName string,
) string {
	var b strings.Builder
	b.WriteString("【会议主题】" + effectiveTopic(room))
	if room.Description != "" {
		b.WriteString("\n【会议描述】" + room.Description)
	}
	if room.ContextSummary != "" {
		b.WriteString("\n【早期讨论摘要】" + room.ContextSummary)
	}
	if isFreeMeeting(room) {
		// 自由模式：没有主持人拆题与点名，智能体需要自己推进讨论并主动向用户挖掘信息
		b.WriteString("\n【自由讨论模式】本次会议没有主持人，由参会智能体自行推进：")
		b.WriteString("\n- 先基于【会议主题】与【会议描述】说明你的理解与关键假设；")
		b.WriteString("\n- 信息不足时，直接 @用户名（或 @用户）向用户提出一个最关键的具体问题，挖掘目标、背景、范围、约束与评价标准；")
		b.WriteString("\n- 可以 @其他参会智能体 追问、补充或反驳，推动讨论逐步收敛；")
		b.WriteString("\n- 用户会随时输入来纠正或收束讨论，用户发言优先，请先响应并服从用户的最新指示；")
		b.WriteString("\n- 不要等待主持人点名，也不要输出会议流程或长篇报告。")
	}
	// 参会智能体名册：仅列出可被 @ 的 agent 展示名（主持人不是 @ 目标，不列入）
	if names := collectAgentDisplayNames(participants); len(names) > 0 {
		b.WriteString("\n【参会智能体】" + strings.Join(names, "、"))
	}
	// 用户姓名：让智能体能像真人会议一样直接称呼用户，而不是只会 @用户 这个占位词
	if userName != "" {
		b.WriteString("\n【用户】" + userName + "（可 @用户名 向用户提问）")
	}
	agentNames := agentNamesByID(participants)
	if len(contextMsgs) > 0 {
		b.WriteString("\n【讨论记录】")
		for _, m := range contextMsgs {
			// 跳过触发消息本身，最后单独强调
			if m.Content == triggerContent && m.SenderKind == types.MeetingSenderUser {
				continue
			}
			b.WriteString(fmt.Sprintf("\n%s: %s", senderLabelWithNames(m, agentNames), m.Content))
		}
	}
	b.WriteString("\n【当前议题】" + triggerContent)
	if isFreeMeeting(room) {
		b.WriteString("\n请围绕会议主题主动推进讨论，直接回应上一条发言或用户的最新指示：")
	} else {
		b.WriteString("\n请像在会议桌前发言一样，直接回应【当前议题】：")
	}
	b.WriteString("\n- 常态 1-3 句话、不超过 120 字；被追问或关键论证可放宽到 200 字，不要每条都一样长；")
	b.WriteString("\n- 先表态再给理由；不要用标题、列表或代码块，也不要复述背景；")
	b.WriteString("\n- 可以明确反对；不同意就说清反对点和能改变你立场的证据，不要为了和气而附和；")
	b.WriteString("\n- 强烈不同意上一位时，可以用「先打断一下」开头直接反驳并 @ 对方；")
	b.WriteString("\n- 要请某位参会人接话，在句末 @对方姓名；只能 @ 其他参会智能体，不要 @ 自己；")
	if userName != "" {
		b.WriteString("\n- 需要用户拍板或补充，在句末 @" + userName + "（或 @用户）提一个问题；")
	} else {
		b.WriteString("\n- 需要用户拍板或补充，在句末 @用户 提一个问题；")
	}
	b.WriteString("\n- 若相较上述讨论记录没有新增观点，请仅回复 PASS，不要输出其他任何内容。")
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

// agentNamesByID 构建 agentID -> 参会展示名 映射。
// 讨论记录必须用展示名标注发言者，否则模型只能看到 UUID，无从判断是谁在说话，容易 @ 错对象。
func agentNamesByID(participants []*types.MeetingParticipant) map[string]string {
	names := make(map[string]string, len(participants))
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" || p.DisplayName == "" {
			continue
		}
		names[p.AgentID] = p.DisplayName
	}
	return names
}

// senderLabelWithNames 与 senderLabel 相同，但智能体发言优先显示参会展示名。
func senderLabelWithNames(m *types.MeetingMessage, names map[string]string) string {
	if m.SenderKind == types.MeetingSenderAgent {
		if name := names[m.SenderID]; name != "" {
			return name
		}
	}
	return senderLabel(m)
}

// replyToSenderName 返回待回应消息的发言者展示名（用户/主持人/智能体展示名）。
func replyToSenderName(replyTo *types.MeetingMessage, names map[string]string) string {
	if replyTo == nil {
		return ""
	}
	return senderLabelWithNames(replyTo, names)
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
