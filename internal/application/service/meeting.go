package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// meetingService 会议室 CRUD 与消息入口；讨论本身委托给 MeetingEngine
type meetingService struct {
	roomRepo        interfaces.MeetingRoomRepository
	participantRepo interfaces.MeetingParticipantRepository
	messageRepo     interfaces.MeetingMessageRepository
	agentRepo       interfaces.CustomAgentRepository
	artifactRepo    interfaces.MeetingArtifactRepository
	messageService  interfaces.MessageService // 产物详情/下载：读取后台会话消息
	engine          interfaces.MeetingEngine
}

func NewMeetingService(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	artifactRepo interfaces.MeetingArtifactRepository,
	messageService interfaces.MessageService,
	engine interfaces.MeetingEngine,
) interfaces.MeetingService {
	return &meetingService{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo,
		artifactRepo: artifactRepo, messageService: messageService,
		engine: engine,
	}
}

// validateMeetingCreate 创建参数校验（标题必填、至少 1 个智能体、广播模式回退默认）
func validateMeetingCreate(room *types.MeetingRoom, agentIDs []string) error {
	if room.Title == "" {
		return errors.New("title is required")
	}
	if len(agentIDs) == 0 {
		return errors.New("at least one agent is required")
	}
	if room.BroadcastMode != types.MeetingBroadcastParallel &&
		room.BroadcastMode != types.MeetingBroadcastSequential {
		room.BroadcastMode = types.MeetingBroadcastSequential
	}
	// 路由模式白名单归一化：非法/空值回退主持人指派
	switch room.RoutingMode {
	case types.MeetingRoutingMention, types.MeetingRoutingFree:
	default:
		room.RoutingMode = types.MeetingRoutingHostAssign
	}
	if room.GuardMaxExchange <= 0 {
		room.GuardMaxExchange = 3
	}
	// 主持人介入间隔归一化：0（关闭）或 1..10；超范围一律视为关闭
	if room.HostEveryNRounds < 0 || room.HostEveryNRounds > types.MeetingHostEveryNRoundsMax {
		room.HostEveryNRounds = 0
	}
	if room.Status == "" {
		room.Status = types.MeetingStatusAwaitingConfirmation // 创建即待确认；开场与确认交互见 M3
	}
	return nil
}

func (s *meetingService) CreateMeeting(ctx context.Context, room *types.MeetingRoom, agentIDs []string) (*types.MeetingRoom, error) {
	if err := validateMeetingCreate(room, agentIDs); err != nil {
		return nil, err
	}
	// 校验智能体归属本租户并取显示名/头像
	participants := make([]*types.MeetingParticipant, 0, len(agentIDs)+1)
	// 主持人（系统自动担任，唯一不可移除）
	participants = append(participants, &types.MeetingParticipant{
		MeetingRoomID: room.ID, Kind: types.ParticipantKindHost,
		DisplayName: "主持人", OrderIndex: 0,
	})
	for i, agentID := range agentIDs {
		agent, err := s.agentRepo.GetAgentByID(ctx, agentID, room.TenantID)
		if err != nil {
			return nil, errors.New("agent not found: " + agentID)
		}
		participants = append(participants, &types.MeetingParticipant{
			MeetingRoomID: room.ID, Kind: types.ParticipantKindAgent,
			AgentID: agent.ID, DisplayName: agent.Name, Avatar: agent.Avatar, OrderIndex: i + 1,
		})
	}
	if err := s.roomRepo.Create(ctx, room); err != nil {
		return nil, err
	}
	for _, p := range participants {
		p.MeetingRoomID = room.ID
	}
	if err := s.participantRepo.CreateBatch(ctx, participants); err != nil {
		return nil, err
	}
	// 创建成功即异步触发主持人开场（M3）：不阻塞创建返回；失败由引擎落 system 兜底，房间保持待确认
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting GenerateOpening panic, room=%s recover=%v", room.ID, r)
			}
		}()
		if err := s.engine.GenerateOpening(bgCtx, room, ""); err != nil {
			logger.Errorf(bgCtx, "meeting GenerateOpening failed, room=%s err=%v", room.ID, err)
		}
	}()
	return room, nil
}

var (
	// ErrTurnRunning 讨论轮进行中（handler 判等映射 409）
	ErrTurnRunning = errors.New("meeting: discussion turn is running")
	// ErrNoAgents 全量替换至少保留一个智能体（handler 判等映射 400）
	ErrNoAgents = errors.New("meeting: at least one agent is required")
)

// ReplaceParticipants 全量替换参会智能体（host 记录由仓储层强制保留，请求无法移除主持人）。
// 顺序：归档拒绝 → ≥1 agent → 讨论锁 409 → 全量预校验 → 先删后插。
func (s *meetingService) ReplaceParticipants(ctx context.Context, tenantID uint64, roomID string, agentIDs []string) ([]*types.MeetingParticipant, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // archived 只读；awaiting_confirmation 允许
	}
	ids := dedupeAgentIDs(agentIDs)
	if len(ids) == 0 {
		return nil, ErrNoAgents
	}
	running, err := s.engine.IsTurnRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrTurnRunning
	}
	// 先全量预校验并构建参与者，避免"删了一半发现 agent 不存在"的部分替换
	agents := make([]*types.CustomAgent, 0, len(ids))
	for _, id := range ids {
		agent, err := s.agentRepo.GetAgentByID(ctx, id, room.TenantID)
		if err != nil {
			return nil, errors.New("agent not found: " + id)
		}
		agents = append(agents, agent)
	}
	if err := s.participantRepo.DeleteAgentsByRoom(ctx, roomID); err != nil {
		return nil, err
	}
	ps := make([]*types.MeetingParticipant, 0, len(agents))
	for i, agent := range agents {
		ps = append(ps, &types.MeetingParticipant{
			MeetingRoomID: roomID, Kind: types.ParticipantKindAgent,
			AgentID: agent.ID, DisplayName: agent.Name, Avatar: agent.Avatar, OrderIndex: i + 1,
		})
	}
	if err := s.participantRepo.CreateBatch(ctx, ps); err != nil {
		return nil, err
	}
	return s.participantRepo.ListByRoom(ctx, roomID)
}

// dedupeAgentIDs 去重保序（全量替换语义下重复 ID 无意义）
func dedupeAgentIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (s *meetingService) GetMeeting(ctx context.Context, tenantID uint64, id string) (*types.MeetingRoom, []*types.MeetingParticipant, error) {
	room, err := s.roomRepo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, nil, err
	}
	participants, err := s.participantRepo.ListByRoom(ctx, id)
	return room, participants, err
}

func (s *meetingService) ListMeetings(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.roomRepo.ListByCreator(ctx, tenantID, createdBy, status, offset, limit)
}

func (s *meetingService) UpdateMeeting(ctx context.Context, room *types.MeetingRoom) error {
	return s.roomRepo.Update(ctx, room)
}

func (s *meetingService) DeleteMeeting(ctx context.Context, tenantID uint64, id string) error {
	return s.roomRepo.Delete(ctx, id, tenantID)
}

// ConfirmFlow 「开始会议」：awaiting_confirmation → active（幂等），以最近一条开场消息为触发自动开跑首轮。
func (s *meetingService) ConfirmFlow(ctx context.Context, tenantID uint64, roomID string) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	switch room.Status {
	case types.MeetingStatusActive:
		return nil // 幂等：已确认直接返回
	case types.MeetingStatusArchived:
		return ErrRoomArchived
	}
	room.Status = types.MeetingStatusActive
	if err := s.roomRepo.Update(ctx, room); err != nil {
		return err
	}
	// 触发消息取最近一条开场（含主题分析 + 建议流程，天然的讨论起点）；无开场则等用户首条发言
	triggerID := s.latestOpeningMessageID(ctx, roomID)
	if triggerID == "" {
		return nil
	}
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			// 兜底 recover：讨论轮异步重管线，单次 panic 不应拖垮服务进程
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunTurn panic, room=%s recover=%v", roomID, r)
			}
		}()
		if err := s.engine.RunTurn(bgCtx, roomID, triggerID); err != nil {
			logger.Errorf(bgCtx, "meeting RunTurn failed, room=%s err=%v", roomID, err)
		}
	}()
	return nil
}

// latestOpeningMessageID 倒序找最近一条开场消息 ID（payload.stage=opening）；无则空串
func (s *meetingService) latestOpeningMessageID(ctx context.Context, roomID string) string {
	msgs, err := s.messageRepo.ListByRoom(ctx, roomID, "", 100)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		var p openingPayload
		if json.Unmarshal(msgs[i].Payload, &p) == nil && p.Stage == MeetingOpeningStage {
			return msgs[i].ID
		}
	}
	return ""
}

func (s *meetingService) SendUserMessage(ctx context.Context, tenantID uint64, roomID string, userID string, content string, mentionedAgentIDs []string, adjustFlow bool) (*types.MeetingMessage, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // 归档守卫：archived 房间只读
	}
	msg := &types.MeetingMessage{
		MeetingRoomID:     roomID,
		SenderKind:        types.MeetingSenderUser,
		SenderID:          userID,
		Content:           content,
		MentionedAgentIDs: mentionedAgentIDs,
	}
	if err := s.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	// 「调整流程」：保持 awaiting_confirmation，主持人结合该意见重出开场（新的 opening 消息）
	if adjustFlow && room.Status == types.MeetingStatusAwaitingConfirmation {
		go func() {
			bgCtx := logger.CloneContext(ctx)
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf(bgCtx, "meeting GenerateOpening panic, room=%s recover=%v", roomID, r)
				}
			}()
			if err := s.engine.GenerateOpening(bgCtx, room, msg.ID); err != nil {
				logger.Errorf(bgCtx, "meeting GenerateOpening failed, room=%s err=%v", roomID, err)
			}
		}()
		return msg, nil
	}
	// 直接打字 = 隐式确认：awaiting_confirmation → active，再进入正常讨论轮
	if room.Status == types.MeetingStatusAwaitingConfirmation {
		room.Status = types.MeetingStatusActive
		if err := s.roomRepo.Update(ctx, room); err != nil {
			return nil, err
		}
	}
	// 异步触发讨论轮；CloneContext 保留 trace，避免请求 ctx 取消中断讨论
	go func() {
		bgCtx := logger.CloneContext(ctx)
		// 兜底 recover：讨论轮是异步重管线，单次 panic 不应拖垮整个服务进程
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunTurn panic, room=%s recover=%v", roomID, r)
			}
		}()
		if err := s.engine.RunTurn(bgCtx, roomID, msg.ID); err != nil {
			logger.Errorf(bgCtx, "meeting RunTurn failed, room=%s err=%v", roomID, err)
		}
	}()
	return msg, nil
}

func (s *meetingService) ListMessages(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 100 // 前端展示默认 100 条 + 顶部"加载更早"
	}
	return s.messageRepo.ListByRoom(ctx, roomID, beforeID, limit)
}

// InterruptDiscussion 用户主动打断进行中的讨论轮（先做租户归属校验，再置打断标记）
func (s *meetingService) InterruptDiscussion(ctx context.Context, tenantID uint64, roomID string) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return err // 归档守卫：archived 房间只读
	}
	return s.engine.Interrupt(ctx, roomID)
}

// validReviewStates 允许前端设置的评审状态（none 只是初始值，不允许回设）
var validReviewStates = map[string]bool{
	types.ReviewStatePending:  true,
	types.ReviewStateAdopted:  true,
	types.ReviewStateRejected: true,
}

// broadcasterProvider 引擎侧暴露广播器的收窄接口（与 handler.engineBroadcaster 同一模式）
type broadcasterProvider interface{ Broadcaster() *MeetingBroadcaster }

// publishEvent 经引擎广播器发 SSE 事件；引擎类型不符时静默跳过（不阻断主流程）
func publishEvent(engine interfaces.MeetingEngine, roomID string, evt MeetingEvent) {
	if bp, ok := engine.(broadcasterProvider); ok {
		bp.Broadcaster().Publish(roomID, evt)
	}
}

// UpdateReviewState 更新智能体消息的评审状态并广播；仅允许评审 agent 讨论消息
func (s *meetingService) UpdateReviewState(ctx context.Context, tenantID uint64, roomID string, messageID string, state string) (*types.MeetingMessage, error) {
	if !validReviewStates[state] {
		return nil, fmt.Errorf("invalid review state: %s", state)
	}
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID) // 租户校验（仓储按 tenant 过滤）
	if err != nil {
		return nil, err
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // 归档守卫：archived 房间只读
	}
	msg, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if msg.MeetingRoomID != room.ID {
		return nil, fmt.Errorf("message %s not in room %s", messageID, roomID)
	}
	if msg.SenderKind != types.MeetingSenderAgent || msg.IsSolution {
		return nil, fmt.Errorf("only agent discussion messages can be reviewed")
	}
	if err := s.messageRepo.UpdateReviewState(ctx, messageID, state); err != nil {
		return nil, err
	}
	msg.ReviewState = state
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventMessageReviewUpdated, Data: msg})
	return msg, nil
}

// GenerateSolution 先做租户归属校验取 room，再委托引擎生成方案（引擎无租户上下文）
func (s *meetingService) GenerateSolution(ctx context.Context, tenantID uint64, roomID string, userID string) (*types.MeetingMessage, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // 归档守卫：archived 房间只读
	}
	return s.engine.GenerateSolution(ctx, room, userID)
}

// ListArtifacts 本会议历史方案产物（含正文），按版本升序返回
func (s *meetingService) ListArtifacts(ctx context.Context, tenantID uint64, roomID string) ([]*types.MeetingArtifactDetail, error) {
	if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil { // 租户校验
		return nil, err
	}
	artifacts, err := s.artifactRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	// 仓储按 version DESC 返回，接口约定升序（v1 -> vN），这里就地重排
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Version < artifacts[j].Version })
	out := make([]*types.MeetingArtifactDetail, 0, len(artifacts))
	for _, a := range artifacts {
		a = types.NormalizeMeetingArtifact(a) // 历史行 kind/status 兜底
		d := &types.MeetingArtifactDetail{MeetingArtifact: a}
		if a.SolutionMessageID != "" {
			if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil {
				d.Content = msg.Content // 正文关联失败降级为空串，不拖垮整个列表
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// RollbackSolution 把房间当前方案指针切到指定历史版本，并落系统消息广播
func (s *meetingService) RollbackSolution(ctx context.Context, tenantID uint64, roomID string, version int) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err
	}
	if err := ensureRoomActive(room); err != nil {
		return err // 归档守卫：archived 房间只读
	}
	artifacts, err := s.artifactRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return err
	}
	var target *types.MeetingArtifact
	for _, a := range artifacts {
		if a.Version == version {
			target = a
			break
		}
	}
	if target == nil {
		return fmt.Errorf("solution version %d not found", version)
	}
	msg, err := s.messageRepo.GetByID(ctx, target.SolutionMessageID)
	if err != nil {
		return err
	}
	if !msg.IsSolution || msg.MeetingRoomID != roomID { // 防跨房间指针污染
		return fmt.Errorf("invalid solution message for version %d", version)
	}
	room.CurrentSolutionMessageID = msg.ID
	if err := s.roomRepo.Update(ctx, room); err != nil {
		return err
	}
	// 系统消息告知回滚（SSE 对端经 message.created 同步）
	sysMsg := &types.MeetingMessage{
		MeetingRoomID: roomID,
		SenderKind:    types.MeetingSenderSystem,
		Content:       fmt.Sprintf("已将当前方案回滚到 v%d", version),
	}
	if err := s.messageRepo.Create(ctx, sysMsg); err == nil {
		publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: sysMsg})
	}
	return nil
}
