package workbench

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DocArchiveSource 是章节文档归档所需的最小能力面（§6.2）。
//
// 由 doc_assistant 服务实现，在组合根用窄接口注入，避免 workbench 包
// 反向 import doc_assistant 形成循环依赖。
type DocArchiveSource interface {
	// GetTask 读任务（owner 由 ctx 取），用于标题与自动归档开关。
	GetTask(ctx context.Context, taskID string) (*types.DocGenerationTask, error)
	// ListDocuments 返回任务下全部版本（倒序），第一条即最新。
	ListDocuments(ctx context.Context, taskID string) ([]*types.DocDocument, error)
	// RenderMarkdown 渲染某版本的完整 Markdown。
	RenderMarkdown(ctx context.Context, doc *types.DocDocument) (string, error)
	// MarkTaskArchiveState 回写 doc_generation_tasks 的自动归档状态。
	MarkTaskArchiveState(ctx context.Context, taskID, status, archiveErr string) error
}

// WithDocArchive 注入章节文档归档依赖，返回自身便于链式装配。
func (s *Service) WithDocArchive(src DocArchiveSource) *Service {
	s.docArchive = src
	return s
}

// docArchiveState 计算章节文档（source_type='doc'，source_id=任务 id）的归档状态。
//
// 章节文档每次编辑都会新增一条 DocDocument，因此当前版本取最新文档的 version，
// 而不是用 doc id 当 source（§6.1）。
func (s *Service) docArchiveState(
	ctx context.Context, tenantID uint64, ownerID, taskID string,
) (*types.WorkbenchArchiveState, error) {
	if s.docArchive == nil {
		return nil, notFound("doc generation task not found")
	}
	if taskID == "" {
		return nil, nil
	}
	if _, err := s.docArchive.GetTask(ctx, taskID); err != nil {
		return nil, notFound("doc generation task not found")
	}
	docs, err := s.docArchive.ListDocuments(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var version *int
	if len(docs) > 0 && docs[0] != nil {
		v := docs[0].Version
		version = &v
	}
	return s.snapshotState(ctx, tenantID, ownerID, types.WorkbenchSourceDoc, taskID, version, false)
}

// archiveDoc 显式归档章节文档：服务端渲染 Markdown 后走与条目相同的写入算法。
//
// 请求只带 source_type/source_id（source_id = 任务 id）；不接受前端传入的正文，
// 避免把过期文本写进知识库（§6.2）。
func (s *Service) archiveDoc(
	ctx context.Context, tenantID uint64, ownerID, taskID string, force bool,
) (*types.WorkbenchArchiveState, error) {
	if s.docArchive == nil {
		return nil, notFound("doc generation task not found")
	}
	task, err := s.docArchive.GetTask(ctx, taskID)
	if err != nil {
		return nil, notFound("doc generation task not found")
	}
	docs, err := s.docArchive.ListDocuments(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 || docs[0] == nil {
		return nil, nil
	}
	latest := docs[0]
	markdown, err := s.docArchive.RenderMarkdown(ctx, latest)
	if err != nil {
		// 渲染失败也要回写任务状态，否则任务会永远停在 pending。
		s.recordDocArchiveState(ctx, task.ID, nil, err)
		return nil, err
	}
	parts, err := splitArchiveMarkdown(task.Theme, markdown)
	if err != nil {
		s.recordDocArchiveState(ctx, task.ID, nil, err)
		return nil, err
	}
	src := &archiveSource{
		TenantID:      tenantID,
		OwnerID:       ownerID,
		SourceType:    types.WorkbenchSourceDoc,
		SourceID:      task.ID,
		Title:         task.Theme,
		SourceVersion: latest.Version,
	}
	state, err := s.writeParts(ctx, src, parts, force)
	// 结果回写任务行：只影响自动归档的可观测性，失败不回滚已写入的分片。
	s.recordDocArchiveState(ctx, task.ID, state, err)
	return state, err
}

// recordDocArchiveState 把结果回写到任务行（§6.2 第 9 步）。
func (s *Service) recordDocArchiveState(
	ctx context.Context, taskID string, state *types.WorkbenchArchiveState, archiveErr error,
) {
	if s.docArchive == nil || taskID == "" {
		return
	}
	status := types.DocArchiveStatusArchived
	message := ""
	switch {
	case archiveErr != nil:
		status = types.DocArchiveStatusFailed
		message = archiveErr.Error()
	case state != nil && state.Status == archiveStatusPartial:
		status = types.DocArchiveStatusFailed
		message = "partial archive failed"
	}
	if err := s.docArchive.MarkTaskArchiveState(ctx, taskID, status, message); err != nil {
		logger.Warnf(ctx, "workbench: failed to record doc archive state for %s: %v", taskID, err)
	}
}

// ProcessDocArchive 是自动归档的异步 worker（§6.9）。
//
// 幂等与“不重建”口径：
//   - 任务必须仍是 AutoArchive 且 archive_status 为空，否则跳过；
//   - 若该 source 已有任何映射（含 deleted），写 skipped 而不重建；
//   - 成功写 archived，失败写 failed + archive_error 并写 archive_failed 通知。
func (s *Service) ProcessDocArchive(ctx context.Context, t *asynq.Task) error {
	if s.docArchive == nil {
		return nil
	}
	var payload types.DocArchivePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return nil // 不可反序列化不重试
	}
	if payload.TaskID == "" || payload.TenantID == 0 || payload.OwnerID == "" {
		return nil
	}
	ctx = withDocOwner(ctx, payload.TenantID, payload.OwnerID)

	task, err := s.docArchive.GetTask(ctx, payload.TaskID)
	if err != nil {
		logger.Warnf(ctx, "workbench: auto archive task %s not found: %v", payload.TaskID, err)
		return nil
	}
	// 入队方会先把状态置为 pending（避免重复入队），因此这里只拦终态：
	// archived / failed / skipped / deleted 都表示本任务已经处理过，不再重建。
	switch task.ArchiveStatus {
	case types.DocArchiveStatusNone, types.DocArchiveStatusPending:
		// 继续
	default:
		return nil
	}
	if !task.AutoArchive {
		return nil
	}
	// 已有映射（含 deleted）时不重建，与「删除不重建」同一条规则（§6.7）。
	existing, err := s.repo.CountAnyBySource(
		ctx, payload.TenantID, payload.OwnerID, types.WorkbenchSourceDoc, payload.TaskID)
	if err != nil {
		return err
	}
	if existing > 0 {
		_ = s.docArchive.MarkTaskArchiveState(
			ctx, payload.TaskID, types.DocArchiveStatusSkipped, "")
		return nil
	}

	// 先标 pending，避免同一任务被并发执行两次自动归档。
	if err := s.docArchive.MarkTaskArchiveState(
		ctx, payload.TaskID, types.DocArchiveStatusPending, ""); err != nil {
		return err
	}
	if _, err := s.archiveDoc(ctx, payload.TenantID, payload.OwnerID, payload.TaskID, false); err != nil {
		// archiveDoc 内部已把失败写进 archive_error；这里补一条用户通知。
		logger.Warnf(ctx, "workbench: auto archive doc %s failed: %v", payload.TaskID, err)
		s.NotifyArchiveFailed(ctx, &archiveSource{
			TenantID:   payload.TenantID,
			OwnerID:    payload.OwnerID,
			SourceType: types.WorkbenchSourceDoc,
			SourceID:   payload.TaskID,
			Title:      task.Theme,
		}, []int{1})
		// 不返回错误：生成任务本身已成功，重试由用户手动“更新归档”触发。
		return nil
	}
	return nil
}

// DocArchiveTask 构造自动归档任务（供 doc_assistant 入队使用）。
func DocArchiveTask(taskID string, tenantID uint64, ownerID string) (*asynq.Task, error) {
	payload, err := json.Marshal(types.DocArchivePayload{
		TaskID:   taskID,
		TenantID: tenantID,
		OwnerID:  ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode doc archive payload: %w", err)
	}
	return asynq.NewTask(types.TypeWorkbenchArchiveDoc, payload), nil
}

// withDocOwner 给 worker 上下文补上 owner 身份；workbench 的 callerScope 依赖它。
func withDocOwner(ctx context.Context, tenantID uint64, ownerID string) context.Context {
	ctx = types.WithExecutionTenant(ctx, tenantID)
	ctx = types.WithCaller(ctx, types.Caller{
		TenantID: tenantID,
		UserID:   ownerID,
		Role:     types.TenantRoleViewer,
	})
	return context.WithValue(ctx, types.UserIDContextKey, ownerID)
}
