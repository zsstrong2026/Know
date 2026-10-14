package service

import (
	"context"
	"errors"

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
	engine          interfaces.MeetingEngine
}

func NewMeetingService(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	engine interfaces.MeetingEngine,
) interfaces.MeetingService {
	return &meetingService{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo, engine: engine,
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
	if room.GuardMaxExchange <= 0 {
		room.GuardMaxExchange = 3
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

func (s *meetingService) SendUserMessage(ctx context.Context, roomID string, userID string, content string, mentionedAgentIDs []string) (*types.MeetingMessage, error) {
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
	if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil {
		return err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	return s.engine.Interrupt(ctx, roomID)
}
