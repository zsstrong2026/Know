package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrRoomArchived 会议已归档：一切写操作拒绝（只以原值返回、从不 wrap，handler 判等 == 映射 403）
var ErrRoomArchived = errors.New("meeting: room archived")

// ensureRoomActive 可写守卫：仅 archived（只读终态）拒绝；
// active 与 awaiting_confirmation 均可写（待确认态允许发言/评审/调整流程/改设置）。
func ensureRoomActive(room *types.MeetingRoom) error {
	if room.Status == types.MeetingStatusArchived {
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

// ExportArtifact 导出方案为 .md（返回文件名/正文；只读操作，归档房间允许导出）
func (s *meetingService) ExportArtifact(ctx context.Context, tenantID uint64, roomID, artifactID string) (string, string, error) {
	if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil {
		return "", "", err // 租户校验（仓储按 tenant 过滤）
	}
	artifact, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return "", "", err
	}
	msg, err := s.messageRepo.GetByID(ctx, artifact.SolutionMessageID)
	if err != nil {
		return "", "", err
	}
	// 防跨房间指针污染：方案消息必须属于本房间且确为方案
	if !msg.IsSolution || msg.MeetingRoomID != roomID {
		return "", "", errors.New("meeting: artifact points to invalid solution message")
	}
	// 记录导出格式；失败不阻断导出
	_ = s.artifactRepo.UpdateExportedFormat(ctx, artifactID, "md")
	return fmt.Sprintf("meeting-solution-v%d.md", artifact.Version), msg.Content, nil
}
