package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MeetingRoomRepository 会议室存储（全部按 tenant_id+created_by 过滤，仅创建者可见）
type MeetingRoomRepository interface {
	Create(ctx context.Context, room *types.MeetingRoom) error
	GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error)
	ListByCreator(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	Update(ctx context.Context, room *types.MeetingRoom) error
	Delete(ctx context.Context, id string, tenantID uint64) error // 软删
}

type MeetingParticipantRepository interface {
	CreateBatch(ctx context.Context, ps []*types.MeetingParticipant) error
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingParticipant, error)
	CountAgentsByRoom(ctx context.Context, roomID string) (int64, error)
}

type MeetingMessageRepository interface {
	Create(ctx context.Context, msg *types.MeetingMessage) error
	GetByID(ctx context.Context, id string) (*types.MeetingMessage, error)
	// ListByRoom 游标分页：beforeID 为空取最新 limit 条；否则取该消息之前更早的 limit 条。返回正序（旧->新）。
	ListByRoom(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
	// ListRecent 取最近 limit 条（正序），供 LLM 上下文使用
	ListRecent(ctx context.Context, roomID string, limit int) ([]*types.MeetingMessage, error)
	UpdateReviewState(ctx context.Context, id string, state string) error
	CountByRoom(ctx context.Context, roomID string) (int64, error)
	CountByReviewStates(ctx context.Context, roomID string, states []string) (int64, error)
	MaxSolutionVersion(ctx context.Context, roomID string) (int, error)
	ListByReviewState(ctx context.Context, roomID string, state string) ([]*types.MeetingMessage, error)
}

type MeetingArtifactRepository interface {
	Create(ctx context.Context, a *types.MeetingArtifact) error
	GetByID(ctx context.Context, id string, roomID string) (*types.MeetingArtifact, error)
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingArtifact, error)
}

// MeetingService 会议室业务服务
type MeetingService interface {
	CreateMeeting(ctx context.Context, room *types.MeetingRoom, agentIDs []string) (*types.MeetingRoom, error)
	GetMeeting(ctx context.Context, tenantID uint64, id string) (*types.MeetingRoom, []*types.MeetingParticipant, error)
	ListMeetings(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	UpdateMeeting(ctx context.Context, room *types.MeetingRoom) error
	DeleteMeeting(ctx context.Context, tenantID uint64, id string) error
	// SendUserMessage 落库用户消息并异步触发讨论轮，返回落库后的消息
	SendUserMessage(ctx context.Context, roomID string, userID string, content string, mentionedAgentIDs []string) (*types.MeetingMessage, error)
	ListMessages(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
}

// MeetingEngine 讨论引擎：同一会议同一时刻仅一个讨论轮次（Redis 锁保证）
type MeetingEngine interface {
	RunTurn(ctx context.Context, roomID string, triggerMessageID string) error
	Interrupt(ctx context.Context, roomID string) error
}
