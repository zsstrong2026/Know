package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrRoomArchived 会议已归档：一切写操作拒绝（只以原值返回、从不 wrap，handler 判等 == 映射 403）
var ErrRoomArchived = errors.New("meeting: room archived")

// ensureRoomActive 归档守卫：非 active 状态的房间拒绝写操作
func ensureRoomActive(room *types.MeetingRoom) error {
	if room.Status != types.MeetingStatusActive {
		return ErrRoomArchived
	}
	return nil
}

// ArchiveRoom 归档会议（幂等）：置 archived + 落系统消息 + 广播
func (s *meetingService) ArchiveRoom(ctx context.Context, tenantID uint64, roomID string) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err // 租户校验（仓储按 tenant 过滤）
	}
	if room.Status == types.MeetingStatusArchived {
		return nil // 幂等：已归档直接返回
	}
	room.Status = types.MeetingStatusArchived
	if err := s.roomRepo.Update(ctx, room); err != nil {
		return err
	}
	// 落系统消息并广播（复用 T5.2 回滚系统消息模式）
	sysMsg := &types.MeetingMessage{
		MeetingRoomID: roomID,
		SenderKind:    types.MeetingSenderSystem,
		Content:       "会议已归档，进入只读状态",
	}
	if err := s.messageRepo.Create(ctx, sysMsg); err != nil {
		return err
	}
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: sysMsg})
	return nil
}

// ExportArtifact 导出方案为 .md（返回文件名/正文；只读操作，归档房间允许导出）。实现见 T6.3
func (s *meetingService) ExportArtifact(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, string, error) {
	return "", "", errors.New("meeting: export artifact not implemented") // T6.3 实现
}
