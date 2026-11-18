package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrInvalidArtifactKind 形态不在 8 种白名单内（handler 判等映射 400）
	ErrInvalidArtifactKind = errors.New("meeting: invalid artifact kind_type")
	// ErrNotRegenerable 仅 generated 产物支持重新生成（handler 判等映射 400）
	ErrNotRegenerable = errors.New("meeting: only generated artifacts can be regenerated")
)

// RequestArtifact 发起产物生成：房间/形态校验 → 互斥预检 → 落 generating 行（version 共用序列）
// → 广播 artifact.generating → 异步执行引擎链路。
func (s *meetingService) RequestArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, kindType, title, requirement string) (*types.MeetingArtifact, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // archived 只读；awaiting_confirmation 允许
	}
	if !types.ValidMeetingArtifactType(kindType) {
		return nil, ErrInvalidArtifactKind
	}
	// 方案需基于真实讨论：空会议直接拒绝，避免生成无信息量的空方案
	if kindType == types.MeetingArtifactTypeSolution {
		if n, err := s.messageRepo.CountByRoom(ctx, roomID); err == nil && n == 0 {
			return nil, ErrNoDiscussionMessages
		}
	}
	if title == "" {
		title = defaultArtifactTitle(kindType)
	}
	running, err := s.engine.IsArtifactRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrArtifactBusy
	}
	version, err := s.artifactRepo.MaxVersionByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	version++ // solution 与 generated 共用房间内 version 序列（UNIQUE(room_id, version)）
	a := &types.MeetingArtifact{
		MeetingRoomID: roomID, Version: version,
		Kind: types.MeetingArtifactKindGenerated, KindType: kindType,
		Title: title, Requirement: requirement,
		Status:    types.MeetingArtifactStatusGenerating,
		CreatedBy: userID,
	}
	if err := s.artifactRepo.Create(ctx, a); err != nil {
		return nil, err
	}
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventArtifactGenerating, Data: map[string]interface{}{
		"artifact_id": a.ID, "kind_type": a.KindType, "title": a.Title,
	}})
	snapshot := *a // 先取值快照再交后台协程：返回值与协程内的字段回写解耦，避免并发读写同一结构
	s.spawnArtifactGeneration(ctx, room, a)
	return &snapshot, nil
}

// RegenerateArtifact 按原参数重跑指定 generated 产物：重置结果字段、沿用原行与版本号。
func (s *meetingService) RegenerateArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, artifactID string) (*types.MeetingArtifact, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return nil, err
	}
	a = types.NormalizeMeetingArtifact(a)
	if a.Kind != types.MeetingArtifactKindGenerated {
		return nil, ErrNotRegenerable
	}
	if a.Status == types.MeetingArtifactStatusGenerating {
		return nil, ErrArtifactBusy
	}
	running, err := s.engine.IsArtifactRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrArtifactBusy
	}
	a.Status = types.MeetingArtifactStatusGenerating
	a.Error = ""
	// 结果字段整体清零：文件态转文本态时，残留的旧文件名/体积/存储引用会污染详情与下载解析
	a.FileName, a.FileType, a.URL, a.ContentHash, a.SourceMessageID = "", "", "", "", ""
	a.FileSize = 0
	if err := s.artifactRepo.Update(ctx, a); err != nil {
		return nil, err
	}
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventArtifactGenerating, Data: map[string]interface{}{
		"artifact_id": a.ID, "kind_type": a.KindType, "title": a.Title,
	}})
	snapshot := *a // 同 RequestArtifact：快照后再交后台，避免并发读写同一结构
	s.spawnArtifactGeneration(ctx, room, a)
	return &snapshot, nil
}

// spawnArtifactGeneration 后台执行产物生成（recover 兜底，失败态由引擎 failArtifact 落库）；
// room 由调用方传入，避免后台协程内重复按租户查询。
func (s *meetingService) spawnArtifactGeneration(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact) {
	go func() {
		// 后台会话属主恒为房间创建者：落消息前先把身份对齐，否则非创建者（如租户管理员）触发时会被会话校验拒绝
		bgCtx := meetingOwnerContext(logger.CloneContext(ctx), room)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunArtifactGeneration panic, room=%s recover=%v", room.ID, r)
				// panic 绕过引擎的 failArtifact 收口，此处补落 failed，避免产物永久停在 generating 而无法重生成
				a.Status = types.MeetingArtifactStatusFailed
				a.Error = fmt.Sprintf("internal panic: %v", r)
				if uerr := s.artifactRepo.Update(bgCtx, a); uerr != nil {
					logger.Errorf(bgCtx, "meeting artifact panic fail-mark failed, room=%s artifact=%s err=%v", room.ID, a.ID, uerr)
				}
				publishEvent(s.engine, room.ID, MeetingEvent{Type: MeetingEventArtifactFailed, Data: map[string]interface{}{
					"artifact_id": a.ID, "error": a.Error,
				}})
			}
		}()
		if err := s.engine.RunArtifactGeneration(bgCtx, room, a); err != nil {
			logger.Errorf(bgCtx, "meeting RunArtifactGeneration failed, room=%s artifact=%s err=%v", room.ID, a.ID, err)
		}
	}()
}

// meetingOwnerContext 把内部的会话/消息访问身份对齐到房间创建者：
// 后台产物会话的属主恒为 room.CreatedBy，若沿用调用者（可能是同租户管理员等非创建者）身份，
// 会话属主校验会拒绝读取（详情降级为空正文、下载恒 404）或拒绝落消息（整条生成链路失败）。
func meetingOwnerContext(ctx context.Context, room *types.MeetingRoom) context.Context {
	if room == nil || room.CreatedBy == "" {
		return ctx
	}
	// UserID 与 Principal 一并覆盖：SessionOwnerIDFromContext 对普通 Web 用户按 UserID 键取值
	ctx = context.WithValue(ctx, types.UserIDContextKey, room.CreatedBy)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: room.CreatedBy})
}

// GetArtifactDetail 产物详情：solution 正文联会议消息；generated 正文联后台会话消息（agent 最终回答）。
func (s *meetingService) GetArtifactDetail(ctx context.Context, tenantID uint64, roomID string, artifactID string) (*types.MeetingArtifactDetail, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return nil, err
	}
	a = types.NormalizeMeetingArtifact(a)
	d := &types.MeetingArtifactDetail{MeetingArtifact: a}
	switch {
	case a.SolutionMessageID != "":
		if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil && msg.MeetingRoomID == roomID {
			d.Content = msg.Content
		}
	case a.SourceMessageID != "" && room.ArtifactSessionID != "" && s.messageService != nil:
		if msg, err := s.messageService.GetMessage(meetingOwnerContext(ctx, room), room.ArtifactSessionID, a.SourceMessageID); err == nil {
			d.Content = msg.Content // 文本形态即交付物正文；文件形态为 agent 的说明
		}
	}
	return d, nil
}

// ResolveArtifactDownload 解析下载跳转地址：在后台会话消息的产物列表中按文件名定位索引，
// 返回既有会话产物下载端点路径（属主校验/存储解析/tombstone 检查全部由该端点承担）。
func (s *meetingService) ResolveArtifactDownload(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return "", err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return "", err
	}
	a = types.NormalizeMeetingArtifact(a)
	if a.Kind != types.MeetingArtifactKindGenerated || a.SourceMessageID == "" || room.ArtifactSessionID == "" {
		return "", errors.New("meeting: artifact is not downloadable")
	}
	if s.messageService == nil {
		return "", errors.New("meeting: message service unavailable")
	}
	msg, err := s.messageService.GetMessage(meetingOwnerContext(ctx, room), room.ArtifactSessionID, a.SourceMessageID)
	if err != nil || msg == nil {
		return "", errors.New("meeting: artifact source message not found")
	}
	index := -1
	for i, art := range msg.Artifacts {
		if art.FileName == a.FileName && !art.Deleted() {
			index = i
			break
		}
	}
	if index < 0 {
		return "", errors.New("meeting: artifact file not found on source message")
	}
	return fmt.Sprintf("/api/v1/sessions/%s/messages/%s/artifacts/%d/download",
		room.ArtifactSessionID, a.SourceMessageID, index), nil
}
