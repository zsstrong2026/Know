package service

import (
	"context"
	"fmt"
	"strings"
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
		sessionService: sessionService, redis: redisClient,
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

// RunTurn 执行一个讨论轮次：同一会议同一时刻仅允许一个轮次
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
		// 已有讨论轮进行中：本条消息排队（已落库，下一轮会带上）
		logger.Infof(ctx, "meeting turn already running, queued. room=%s", roomID)
		return nil
	}
	defer func() {
		if _, err := redislock.Release(ctx, e.redis, turnLockKey(roomID), token); err != nil {
			// 释放失败有 5min lease 兜底，仅记录便于观测
			logger.Warnf(ctx, "meeting turn lock release failed, room=%s err=%v", roomID, err)
		}
	}()
	// 进入新一轮即清除打断标记
	if err := e.redis.Del(ctx, interruptKey(roomID)).Err(); err != nil {
		logger.Warnf(ctx, "clear meeting interrupt flag failed, room=%s err=%v", roomID, err)
	}

	// 会议与消息均为引擎内部消费对象，不校验 created_by（租户隔离由 HTTP 层保证）
	room, err := e.roomRepo.GetByIDInternal(ctx, roomID)
	if err != nil {
		return err
	}
	trigger, err := e.messageRepo.GetByID(ctx, triggerMessageID)
	if err != nil {
		return err
	}
	participants, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return err
	}
	agents := filterAgentParticipants(participants)

	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]interface{}{
		"room_id": roomID, "trigger_message_id": triggerMessageID,
	}})
	defer e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
		"room_id": roomID,
	}})

	// 顺序广播：按 order_index 逐个智能体发言（M2 加并发模式与智能筛选）
	speakers := selectSpeakers(room, trigger, agents)
	for _, p := range speakers {
		if e.isInterrupted(ctx, roomID) {
			logger.Infof(ctx, "meeting turn interrupted. room=%s", roomID)
			return nil
		}
		if err := e.runAgentTurn(ctx, room, p, trigger, nil); err != nil {
			logger.Errorf(ctx, "agent turn failed, room=%s agent=%s err=%v", roomID, p.AgentID, err)
			// 单个智能体失败不阻塞整轮，广播 system 消息提示
			e.publishSystemMessage(ctx, roomID, fmt.Sprintf("智能体「%s」本轮发言失败：%v", p.DisplayName, err))
		}
	}
	return nil
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

// runAgentTurn 单个智能体发言：程序化调用 AgentQA，捕获最终答案并落库+广播
func (e *meetingEngine) runAgentTurn(ctx context.Context, room *types.MeetingRoom, p *types.MeetingParticipant, trigger *types.MeetingMessage, replyTo *types.MeetingMessage) error {
	agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID)
	if err != nil {
		return err
	}
	meetingAgent := cloneAgentForMeeting(agent)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)

	// 共享上下文：最近 N 条（正序）
	contextMsgs, err := e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
	if err != nil {
		return err
	}
	query := buildMeetingQuery(room, trigger.Content, contextMsgs)

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
		return err
	}
	if runErr != nil {
		return runErr
	}

	msg := &types.MeetingMessage{
		MeetingRoomID: room.ID,
		SenderKind:    types.MeetingSenderAgent,
		SenderID:      p.AgentID,
		Content:       answer.String(),
		ReviewState:   types.ReviewStateNone,
	}
	if replyTo != nil {
		msg.ReplyToMessageID = replyTo.ID
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return nil
}

// publishSystemMessage 系统提示消息（失败提示、护栏提示等）
func (e *meetingEngine) publishSystemMessage(ctx context.Context, roomID string, content string) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderSystem, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err == nil {
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
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

// selectSpeakers 选择本轮发言者：@ 点名优先；M1 默认全员（智能筛选在 M2）
func selectSpeakers(room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.MeetingParticipant) []*types.MeetingParticipant {
	if len(trigger.MentionedAgentIDs) > 0 {
		mentioned := make(map[string]struct{}, len(trigger.MentionedAgentIDs))
		for _, id := range trigger.MentionedAgentIDs {
			mentioned[id] = struct{}{}
		}
		out := make([]*types.MeetingParticipant, 0, len(agents))
		for _, p := range agents {
			if _, ok := mentioned[p.AgentID]; ok {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return agents
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
	b.WriteString("\n请以你的身份与职责，针对当前议题发表意见，简明扼要。")
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
