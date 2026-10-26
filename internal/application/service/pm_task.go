package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// 任务服务错误哨兵：handler 依赖 errors.Is 区分输入非法与系统错误
var (
	ErrPMTaskTitleRequired   = errors.New("pm task: payload title is required")
	ErrPMTaskTypeUnsupported = errors.New("pm task: unsupported task type")
)

// pmTaskProgressRunning 置 running 时的起始进度，失败时停在此处
const pmTaskProgressRunning = 10

// PMDraftPolisher 可选的 LLM 润色能力；未注入或失败时退回模板草稿
type PMDraftPolisher interface {
	Polish(ctx context.Context, draft string) (string, error)
}

// PMMaterialContentSink 资料生成结果回写能力；未注入时只返回任务结果，不回写正文
type PMMaterialContentSink interface {
	SaveGeneratedContent(ctx context.Context, tenantID uint64, id string, content string) error
}

// pmTaskService 异步任务：入队、进度查询与 worker 执行入口。
// polisher / materialSink 为空表示对应增强能力未启用，模板渲染始终可独立完成。
// artifact 为空表示未装配版本产物生成能力，PRD/原型任务退回模板渲染。
type pmTaskService struct {
	repo         interfaces.PMTaskRepository
	polisher     PMDraftPolisher
	materialSink PMMaterialContentSink
	artifact     interfaces.PMVersionArtifactRunner
}

func NewPMTaskService(
	repo interfaces.PMTaskRepository,
	polisher PMDraftPolisher,
	materialSink PMMaterialContentSink,
) interfaces.PMTaskService {
	return NewPMTaskServiceWithRunner(repo, polisher, materialSink, nil)
}

// NewPMTaskServiceWithRunner 组合根入口：注入版本产物生成能力，
// 带版本 ID 的 PRD/原型任务改由版本服务逐章生成，其余任务保持模板路径。
func NewPMTaskServiceWithRunner(
	repo interfaces.PMTaskRepository,
	polisher PMDraftPolisher,
	materialSink PMMaterialContentSink,
	artifact interfaces.PMVersionArtifactRunner,
) interfaces.PMTaskService {
	return &pmTaskService{
		repo: repo, polisher: polisher, materialSink: materialSink, artifact: artifact,
	}
}

// CreateTask 落库 pending 任务并返回载体，供调用方立即回传 task_id 轮询
func (s *pmTaskService) CreateTask(
	ctx context.Context, tenantID uint64, projectID string, taskType string, payload types.JSON,
) (*types.PMTask, error) {
	task := &types.PMTask{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		ProjectID: projectID,
		TaskType:  taskType,
		Status:    types.PMTaskStatusPending,
		Payload:   payload,
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *pmTaskService) GetTask(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMTask, error) {
	return s.repo.GetByID(ctx, tenantID, id)
}

// ProcessTask asynq 处理器入口（签名对齐 doc_assistant/generate.go）
func (s *pmTaskService) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload types.PMPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("pm task: unmarshal payload: %w", err)
	}
	// 重建租户上下文：asynq worker 无 HTTP 上下文
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	task, err := s.repo.GetByID(ctx, payload.TenantID, payload.TaskID)
	if err != nil {
		return err
	}
	// 终态幂等：避免 asynq 重试导致重复生成
	if task.Status == types.PMTaskStatusSucceeded {
		return nil
	}

	_ = s.repo.UpdateStatus(ctx, task.ID, types.PMTaskStatusRunning, pmTaskProgressRunning)

	// 版本产物任务：逐章生成由版本服务执行，每章推进任务进度
	if s.artifact != nil && payload.TargetID != "" && isVersionArtifactTask(task.TaskType) {
		result, genErr := s.artifact.RunArtifactGeneration(
			ctx, payload.TenantID, task.ID, payload.TargetID, payload.Regenerate, payload.Chapters)
		if genErr != nil {
			_ = s.repo.Fail(ctx, task.ID, genErr.Error())
			return genErr
		}
		return s.repo.Succeed(ctx, task.ID, result)
	}

	result, content, genErr := s.generate(ctx, task, payload)
	if genErr != nil {
		_ = s.repo.Fail(ctx, task.ID, genErr.Error())
		return genErr
	}
	// 资料正文回写失败不回滚任务：结果 JSON 已含正文，前端仍可查看
	s.persistMaterialContent(ctx, task, payload, content)
	return s.repo.Succeed(ctx, task.ID, result)
}

// persistMaterialContent 仅资料生成且注入了回写能力时执行，失败只记日志
func (s *pmTaskService) persistMaterialContent(
	ctx context.Context, task *types.PMTask, payload types.PMPayload, content string,
) {
	if s.materialSink == nil || task.TaskType != types.TypePMMaterialGenerate || payload.TargetID == "" {
		return
	}
	if err := s.materialSink.SaveGeneratedContent(
		ctx, payload.TenantID, payload.TargetID, content,
	); err != nil {
		logger.Warnf(ctx, "[PMTask] save material content failed: task=%s material=%s err=%v",
			task.ID, payload.TargetID, err)
	}
}

// generate 模板渲染必成功，LLM 润色仅作增强：失败只记日志并保留模板结果；
// 返回任务结果 JSON 与正文（正文供资料生成回写）
func (s *pmTaskService) generate(
	ctx context.Context, task *types.PMTask, payload types.PMPayload,
) (types.JSON, string, error) {
	draft, err := renderPMDraft(task.TaskType, payload)
	if err != nil {
		return nil, "", err
	}

	polished := false
	if s.polisher != nil {
		out, polishErr := s.polisher.Polish(ctx, draft)
		if polishErr != nil {
			logger.Warnf(ctx, "[PMTask] polish failed, fallback to template: task=%s err=%v", task.ID, polishErr)
		} else if strings.TrimSpace(out) != "" {
			draft, polished = out, true
		}
	}

	raw, err := json.Marshal(map[string]any{
		"task_type": task.TaskType,
		"target_id": payload.TargetID,
		"title":     payload.Title,
		"content":   draft,
		"polished":  polished,
	})
	if err != nil {
		return nil, "", fmt.Errorf("pm task: marshal result: %w", err)
	}
	return types.JSON(raw), draft, nil
}

// isVersionArtifactTask 版本产物任务类型：PRD 与原型由版本服务统一生成
func isVersionArtifactTask(taskType string) bool {
	return taskType == types.TypePMPRDGenerate || taskType == types.TypePMPrototypeGenerate
}

// renderPMDraft 纯模板渲染，无外部依赖：标题必填，小节按标题+正文输出
func renderPMDraft(taskType string, payload types.PMPayload) (string, error) {
	switch taskType {
	case types.TypePMPRDGenerate, types.TypePMPrototypeGenerate, types.TypePMMaterialGenerate:
	default:
		return "", fmt.Errorf("%w: %q", ErrPMTaskTypeUnsupported, taskType)
	}
	if strings.TrimSpace(payload.Title) == "" {
		return "", ErrPMTaskTitleRequired
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(payload.Title))
	for _, sec := range payload.Sections {
		if strings.TrimSpace(sec.Heading) == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", strings.TrimSpace(sec.Heading), strings.TrimSpace(sec.Body))
	}
	// 不同任务类型的收尾提示，保证生成物语义完整
	if taskType == types.TypePMPrototypeGenerate {
		b.WriteString("> 原型草稿：按上述功能点生成可交互 HTML。\n")
	}
	return b.String(), nil
}
