package workbench

import (
	"context"
	"encoding/base64"
	"errors"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 文档模板服务（§9.16）。
//
// 关键约束（与条目服务不同的地方）：
//   - 服务端**不做** apply-template：套用由客户端取快照后走普通保存链路，
//     避免出现第二套 Y.Doc 写入路径；
//   - 「从文档另存为模板」时服务端从**已落盘的**条目复制字节，不接受客户端上传快照，
//     因此模板内容与文档版本天然一致；
//   - 「模板管理页新建模板」没有源文档，空白快照只能由客户端提交；
//     这条路径的字节同样过 schema / 体积 / base64 校验后才落库；
//   - 模板硬删，且不参与归档/检索/AI，日志不记录名称、描述与 Y.Doc 内容。

// ListTemplates 取当前用户的模板列表（不含 ydoc）。
func (s *Service) ListTemplates(ctx context.Context) ([]*types.WorkbenchTemplateSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListTemplates(ctx, tenantID, ownerID)
}

// CreateTemplate 创建模板，两种来源：
//   - 给了 source_item_id：从当前已保存的文档复制快照；
//   - 没给 source_item_id：模板管理页「新建模板」，收下客户端提交的空白文档快照。
func (s *Service) CreateTemplate(
	ctx context.Context, req *types.CreateWorkbenchTemplateRequest,
) (*types.WorkbenchTemplateSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	if err := req.Validate(); err != nil {
		return nil, mapTemplateValidationError(err)
	}

	// 配额在 UI 已预检；这里兜底，不新增错误码。
	count, err := s.repo.CountTemplates(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	if count >= types.WorkbenchTemplateMaxPerOwner {
		return nil, apperrors.NewBadRequestError("template quota exceeded")
	}

	if req.SourceItemID == "" {
		return s.createTemplateFromSnapshot(ctx, tenantID, ownerID, req)
	}
	return s.createTemplateFromItem(ctx, tenantID, ownerID, req)
}

// createTemplateFromSnapshot 处理「新建模板」：没有源文档，快照由客户端提交。
func (s *Service) createTemplateFromSnapshot(
	ctx context.Context, tenantID uint64, ownerID string,
	req *types.CreateWorkbenchTemplateRequest,
) (*types.WorkbenchTemplateSummary, error) {
	ydoc, err := decodeTemplateYDoc(req.YDoc)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tpl := &types.WorkbenchTemplate{
		ID:                  uuid.NewString(),
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Name:                req.Name,
		Description:         req.Description,
		YDoc:                ydoc,
		SizeBytes:           len(ydoc),
		EditorSchemaVersion: req.EditorSchemaVersion,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.repo.CreateTemplate(ctx, tpl); err != nil {
		return nil, err
	}
	logger.Infof(ctx,
		"[workbench] template created from snapshot id=%s size=%d schema=%d",
		tpl.ID, tpl.SizeBytes, tpl.EditorSchemaVersion)
	return tpl.ToSummary(), nil
}

// createTemplateFromItem 从已落盘条目复制模板（原「另存为模板」路径）。
func (s *Service) createTemplateFromItem(
	ctx context.Context, tenantID uint64, ownerID string,
	req *types.CreateWorkbenchTemplateRequest,
) (*types.WorkbenchTemplateSummary, error) {
	// 源条目必须存在、未进回收站、是 canvas，且已保存过内容。
	source, err := s.repo.GetItem(ctx, tenantID, ownerID, req.SourceItemID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, notFound("workbench template source item not found")
		}
		return nil, err
	}
	if source == nil || source.Kind != types.WorkbenchKindCanvas {
		return nil, notFound("workbench template source item not found")
	}
	if source.SnapshotVersion <= types.WorkbenchSnapshotVersionInitial || len(source.YDoc) == 0 {
		return nil, apperrors.NewBadRequestError("source item has no saved content")
	}
	if len(source.YDoc) > types.WorkbenchTemplateMaxYDocBytes {
		return nil, apperrors.NewWorkbenchTooLarge(
			types.WorkbenchTemplateMaxYDocBytes, int64(len(source.YDoc)),
		)
	}
	if source.EditorSchemaVersion > types.SupportedEditorSchemaVersion {
		return nil, apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion)
	}

	now := time.Now().UTC()
	sourceID := source.ID
	tpl := &types.WorkbenchTemplate{
		ID:                  uuid.NewString(),
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Name:                req.Name,
		Description:         req.Description,
		YDoc:                source.YDoc,
		SizeBytes:           len(source.YDoc),
		EditorSchemaVersion: source.EditorSchemaVersion,
		SourceItemID:        &sourceID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.repo.CreateTemplate(ctx, tpl); err != nil {
		return nil, err
	}
	// 日志只记 id / 大小 / schema，不记名称、描述与内容。
	logger.Infof(ctx,
		"[workbench] template created id=%s size=%d schema=%d",
		tpl.ID, tpl.SizeBytes, tpl.EditorSchemaVersion)
	return tpl.ToSummary(), nil
}

// UpdateTemplate 只改名称与描述。
func (s *Service) UpdateTemplate(
	ctx context.Context, id string, req *types.UpdateWorkbenchTemplateRequest,
) (*types.WorkbenchTemplateSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	if err := req.Validate(); err != nil {
		return nil, mapTemplateValidationError(err)
	}

	fields := make(map[string]any, 2)
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Description != nil {
		fields["description"] = *req.Description
	}
	updated, err := s.repo.UpdateTemplate(ctx, tenantID, ownerID, id, fields)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, notFound("workbench template not found")
	}
	return updated.ToSummary(), nil
}

// UpdateTemplateContent 用客户端提交的快照替换模板正文。
//
// 与 CreateTemplate 的分工：创建时服务端只复制已落盘条目，避免多一条写入路径；
// 而模板管理页没有"源文档"，用户直接在模板里编辑，只能由客户端把快照提交上来。
// 校验顺序：请求 → base64 解码 → 模板体积上限，全部通过才落库。
func (s *Service) UpdateTemplateContent(
	ctx context.Context, id string, req *types.UpdateWorkbenchTemplateContentRequest,
) (*types.WorkbenchTemplateSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, mapTemplateValidationError(err)
	}
	ydoc, err := decodeTemplateYDoc(req.YDoc)
	if err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateTemplateContent(
		ctx, tenantID, ownerID, id, ydoc, len(ydoc), req.EditorSchemaVersion,
	)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, notFound("workbench template not found")
	}
	// 日志只记 id 与体积，不记模板名与正文。
	logger.Infof(ctx, "[workbench] template content updated id=%s size=%d", updated.ID, updated.SizeBytes)
	return updated.ToSummary(), nil
}

// DeleteTemplate 硬删；已用该模板创建的文档不受影响。
func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	affected, err := s.repo.DeleteTemplate(ctx, tenantID, ownerID, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound("workbench template not found")
	}
	return nil
}

// GetTemplateContent 取模板快照（base64）与 schema，供客户端套用。
//
// 客户端拿到后按普通保存链路写入目标文档（base_version=0），
// 因此服务端没有 apply-template 接口。
func (s *Service) GetTemplateContent(
	ctx context.Context, id string,
) (*types.WorkbenchTemplateContent, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	tpl, err := s.repo.GetTemplateWithContent(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, err
	}
	if tpl == nil {
		return nil, notFound("workbench template not found")
	}
	if len(tpl.YDoc) == 0 {
		// 存量坏数据：不静默返回空快照让前端“套用出一个空文档”。
		return nil, apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion)
	}
	if tpl.EditorSchemaVersion > types.SupportedEditorSchemaVersion {
		return nil, apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion)
	}
	return &types.WorkbenchTemplateContent{
		ID:                  tpl.ID,
		Name:                tpl.Name,
		YDoc:                base64.StdEncoding.EncodeToString(tpl.YDoc),
		EditorSchemaVersion: tpl.EditorSchemaVersion,
		SizeBytes:           len(tpl.YDoc),
	}, nil
}

// decodeTemplateYDoc 解码 base64 快照，并按解码后字节数复核模板上限（1MB）。
func decodeTemplateYDoc(raw string) ([]byte, error) {
	ydoc, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion).
			WithDetails(map[string]any{"reason": "ydoc is not valid base64"})
	}
	if len(ydoc) == 0 {
		return nil, apperrors.NewBadRequestError("template snapshot is empty")
	}
	if len(ydoc) > types.WorkbenchTemplateMaxYDocBytes {
		return nil, apperrors.NewWorkbenchTooLarge(types.WorkbenchTemplateMaxYDocBytes, int64(len(ydoc)))
	}
	return ydoc, nil
}

// mapTemplateValidationError 把 types 里的哨兵翻译成 400 / 413。
func mapTemplateValidationError(err error) error {
	switch {
	case errors.Is(err, types.ErrWorkbenchTemplateTooLarge):
		return apperrors.NewWorkbenchTooLarge(types.WorkbenchTemplateMaxYDocBytes, 0)
	case errors.Is(err, types.ErrWorkbenchTemplateEmpty):
		return apperrors.NewBadRequestError("template snapshot is empty")
	case errors.Is(err, types.ErrWorkbenchInvalidEditorSchemaVersion):
		return apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion)
	case errors.Is(err, types.ErrWorkbenchInvalidTemplateDescription):
		return apperrors.NewWorkbenchInvalidField("description", "at most 200 characters")
	case errors.Is(err, types.ErrWorkbenchInvalidTemplateSource):
		return apperrors.NewWorkbenchInvalidField("source_item_id", "required")
	case errors.Is(err, types.ErrWorkbenchInvalidTemplateName):
		return apperrors.NewWorkbenchInvalidField("name", "1-60 characters")
	default:
		return apperrors.NewBadRequestError(err.Error())
	}
}
