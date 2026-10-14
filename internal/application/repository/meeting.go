package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrMeetingRoomNotFound = errors.New("meeting room not found")
var ErrMeetingMessageNotFound = errors.New("meeting message not found")
var ErrMeetingArtifactNotFound = errors.New("meeting artifact not found")

type meetingRoomRepository struct{ db *gorm.DB }

func NewMeetingRoomRepository(db *gorm.DB) interfaces.MeetingRoomRepository {
	return &meetingRoomRepository{db: db}
}

func (r *meetingRoomRepository) Create(ctx context.Context, room *types.MeetingRoom) error {
	return r.db.WithContext(ctx).Create(room).Error
}

func (r *meetingRoomRepository) GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error) {
	var room types.MeetingRoom
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingRoomNotFound
	}
	return &room, err
}

func (r *meetingRoomRepository) GetByIDInternal(ctx context.Context, id string) (*types.MeetingRoom, error) {
	var room types.MeetingRoom
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingRoomNotFound
	}
	return &room, err
}

func (r *meetingRoomRepository) ListByCreator(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error) {
	var rooms []*types.MeetingRoom
	var total int64
	q := r.db.WithContext(ctx).Model(&types.MeetingRoom{}).
		Where("tenant_id = ? AND created_by = ?", tenantID, createdBy)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&rooms).Error
	return rooms, total, err
}

func (r *meetingRoomRepository) Update(ctx context.Context, room *types.MeetingRoom) error {
	return r.db.WithContext(ctx).Save(room).Error
}

func (r *meetingRoomRepository) Delete(ctx context.Context, id string, tenantID uint64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.MeetingRoom{}).Error
}

type meetingParticipantRepository struct{ db *gorm.DB }

func NewMeetingParticipantRepository(db *gorm.DB) interfaces.MeetingParticipantRepository {
	return &meetingParticipantRepository{db: db}
}

func (r *meetingParticipantRepository) CreateBatch(ctx context.Context, ps []*types.MeetingParticipant) error {
	if len(ps) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(ps).Error
}

func (r *meetingParticipantRepository) ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	var ps []*types.MeetingParticipant
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ?", roomID).
		Order("order_index ASC, created_at ASC").Find(&ps).Error
	return ps, err
}

func (r *meetingParticipantRepository) CountAgentsByRoom(ctx context.Context, roomID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingParticipant{}).
		Where("meeting_room_id = ? AND kind = ?", roomID, types.ParticipantKindAgent).
		Count(&n).Error
	return n, err
}

type meetingMessageRepository struct{ db *gorm.DB }

func NewMeetingMessageRepository(db *gorm.DB) interfaces.MeetingMessageRepository {
	return &meetingMessageRepository{db: db}
}

func (r *meetingMessageRepository) Create(ctx context.Context, msg *types.MeetingMessage) error {
	return r.db.WithContext(ctx).Create(msg).Error
}

func (r *meetingMessageRepository) GetByID(ctx context.Context, id string) (*types.MeetingMessage, error) {
	var msg types.MeetingMessage
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&msg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingMessageNotFound
	}
	return &msg, err
}

func (r *meetingMessageRepository) ListByRoom(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error) {
	q := r.db.WithContext(ctx).Where("meeting_room_id = ?", roomID)
	if beforeID != "" {
		// 游标：取 beforeID 这条消息的 (created_at,id)，查询更早的
		// 必须同样限定 meeting_room_id，避免跨房间传入 beforeID 时静默返回错误分页
		var pivot types.MeetingMessage
		if err := r.db.WithContext(ctx).Select("created_at", "id").
			Where("id = ? AND meeting_room_id = ?", beforeID, roomID).
			First(&pivot).Error; err != nil {
			return nil, ErrMeetingMessageNotFound
		}
		q = q.Where("(created_at, id) < (?, ?)", pivot.CreatedAt, pivot.ID)
	}
	var desc []*types.MeetingMessage
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Find(&desc).Error; err != nil {
		return nil, err
	}
	// 反转为正序（旧->新）
	msgs := make([]*types.MeetingMessage, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		msgs = append(msgs, desc[i])
	}
	return msgs, nil
}

func (r *meetingMessageRepository) ListRecent(ctx context.Context, roomID string, limit int) ([]*types.MeetingMessage, error) {
	return r.ListByRoom(ctx, roomID, "", limit)
}

func (r *meetingMessageRepository) UpdateReviewState(ctx context.Context, id string, state string) error {
	return r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("id = ?", id).Update("review_state", state).Error
}

func (r *meetingMessageRepository) CountByRoom(ctx context.Context, roomID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ?", roomID).Count(&n).Error
	return n, err
}

func (r *meetingMessageRepository) CountByReviewStates(ctx context.Context, roomID string, states []string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ? AND review_state IN ?", roomID, states).
		Count(&n).Error
	return n, err
}

func (r *meetingMessageRepository) MaxSolutionVersion(ctx context.Context, roomID string) (int, error) {
	var v int
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ?", roomID).
		Select("COALESCE(MAX(solution_version), 0)").Scan(&v).Error
	return v, err
}

func (r *meetingMessageRepository) ListByReviewState(ctx context.Context, roomID string, state string) ([]*types.MeetingMessage, error) {
	var msgs []*types.MeetingMessage
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ? AND review_state = ?", roomID, state).
		Order("created_at ASC").Find(&msgs).Error
	return msgs, err
}

type meetingArtifactRepository struct{ db *gorm.DB }

func NewMeetingArtifactRepository(db *gorm.DB) interfaces.MeetingArtifactRepository {
	return &meetingArtifactRepository{db: db}
}

func (r *meetingArtifactRepository) Create(ctx context.Context, a *types.MeetingArtifact) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *meetingArtifactRepository) GetByID(ctx context.Context, id string, roomID string) (*types.MeetingArtifact, error) {
	var a types.MeetingArtifact
	err := r.db.WithContext(ctx).
		Where("id = ? AND meeting_room_id = ?", id, roomID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingArtifactNotFound
	}
	return &a, err
}

func (r *meetingArtifactRepository) ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingArtifact, error) {
	var as []*types.MeetingArtifact
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ?", roomID).
		Order("version DESC").Find(&as).Error
	return as, err
}
