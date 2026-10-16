package service

import (
	"context"
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
	engine          interfaces.MeetingEngine
}

func NewMeetingService(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	artifactRepo interfaces.MeetingArtifactRepository,
	engine interfaces.MeetingEngine,
) interfaces.MeetingService {
	return &meetingService{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo,
		artifactRepo: artifactRepo, engine: engine,
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
		room.Status = types.MeetingStatusActive
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
	return room, nil
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

func (s *meetingService) SendUserMessage(ctx context.Context, tenantID uint64, roomID string, userID string, content string, mentionedAgentIDs []string) (*types.MeetingMessage, error) {
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
		d := &types.MeetingArtifactDetail{MeetingArtifact: a}
		if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil {
			d.Content = msg.Content // 正文关联失败降级为空串，不拖垮整个列表
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
