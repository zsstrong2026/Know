package docassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

var (
	// ErrInvalidTemplate 模板校验失败
	ErrInvalidTemplate = errors.New("invalid doc template")
	// ErrInvalidTask 任务参数非法
	ErrInvalidTask = errors.New("invalid doc generation task")
	// ErrChapterNotFound 章节不存在或越界
	ErrChapterNotFound = errors.New("doc chapter not found")
)

// DocAssistantService 文档助手服务实现
type DocAssistantService struct {
	templateRepo interfaces.DocTemplateRepository
	taskRepo     interfaces.DocTaskRepository
	documentRepo interfaces.DocDocumentRepository
	sessionSvc   interfaces.SessionService
	modelSvc     interfaces.ModelService
	taskEnqueuer interfaces.TaskEnqueuer
	tenantRepo   interfaces.TenantRepository
	// kbService 供自动归档校验输出知识库归属（§6.9）；
	// 知识库服务不依赖文档助手，方向是安全的。
	kbService interfaces.KnowledgeBaseService
}

// NewDocAssistantService 创建文档助手服务
func NewDocAssistantService(
	templateRepo interfaces.DocTemplateRepository,
	taskRepo interfaces.DocTaskRepository,
	documentRepo interfaces.DocDocumentRepository,
	sessionSvc interfaces.SessionService,
	modelSvc interfaces.ModelService,
	taskEnqueuer interfaces.TaskEnqueuer,
	tenantRepo interfaces.TenantRepository,
	kbService interfaces.KnowledgeBaseService,
) interfaces.DocAssistantService {
	return &DocAssistantService{
		templateRepo: templateRepo,
		taskRepo:     taskRepo,
		documentRepo: documentRepo,
		sessionSvc:   sessionSvc,
		modelSvc:     modelSvc,
		taskEnqueuer: taskEnqueuer,
		tenantRepo:   tenantRepo,
		kbService:    kbService,
	}
}

// MarkTaskArchiveState 回写自动归档状态（供工作台归档 worker 调用，§6.9）。
func (s *DocAssistantService) MarkTaskArchiveState(
	ctx context.Context, taskID, status, archiveErr string,
) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.taskRepo.MarkArchiveState(ctx, tenantID, ownerID, taskID, status, archiveErr)
}

// ---- 模板 CRUD ----

func (s *DocAssistantService) CreateTemplate(ctx context.Context, tpl *types.DocTemplate) (*types.DocTemplate, error) {
	if tpl == nil {
		return nil, ErrInvalidTemplate
	}
	if err := validateTemplate(tpl); err != nil {
		return nil, err
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	tpl.ID = types.NewDocAssistantID()
	tpl.TenantID = tenantID
	tpl.OwnerID = ownerID
	tpl.CreatedAt = time.Now().UTC()
	tpl.UpdatedAt = time.Now().UTC()
	if err := s.templateRepo.Create(ctx, tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

func (s *DocAssistantService) UpdateTemplate(ctx context.Context, tpl *types.DocTemplate) (*types.DocTemplate, error) {
	if tpl == nil || tpl.ID == "" {
		return nil, ErrInvalidTemplate
	}
	if err := validateTemplate(tpl); err != nil {
		return nil, err
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	existing, err := s.templateRepo.FindByID(ctx, tenantID, ownerID, tpl.ID)
	if err != nil {
		return nil, err
	}
	existing.Name = tpl.Name
	existing.Description = tpl.Description
	existing.GlobalConfig = tpl.GlobalConfig
	existing.Chapters = tpl.Chapters
	if err := s.templateRepo.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *DocAssistantService) DeleteTemplate(ctx context.Context, id string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	if err := s.templateRepo.Delete(ctx, tenantID, ownerID, id); err != nil {
		return err
	}
	return nil
}

func (s *DocAssistantService) GetTemplate(ctx context.Context, id string) (*types.DocTemplate, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.templateRepo.FindByID(ctx, tenantID, ownerID, id)
}

func (s *DocAssistantService) ListTemplates(ctx context.Context) ([]*types.DocTemplate, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.templateRepo.ListByOwner(ctx, tenantID, ownerID)
}

// validateTemplate 校验模板骨架（章节非空、数量与数值夹紧）
func validateTemplate(tpl *types.DocTemplate) error {
	if tpl == nil {
		return ErrInvalidTemplate
	}
	name := tpl.Name
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidTemplate)
	}
	chapters, err := tpl.DecodeChapters()
	if err != nil {
		return fmt.Errorf("%w: invalid chapters json: %v", ErrInvalidTemplate, err)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("%w: at least one chapter is required", ErrInvalidTemplate)
	}
	if len(chapters) > types.DocTemplateMaxChapters {
		chapters = chapters[:types.DocTemplateMaxChapters]
	}
	for i := range chapters {
		if chapters[i].Title == "" {
			return fmt.Errorf("%w: chapter %d title is required", ErrInvalidTemplate, i+1)
		}
		chapters[i].MaxWords = clampInt(chapters[i].MaxWords, types.DocTemplateMaxWordsLower, types.DocTemplateMaxWordsUpper)
	}
	clamped, _ := json.Marshal(chapters)
	tpl.Chapters = types.JSON(clamped)
	cfg, err := tpl.DecodeGlobalConfig()
	if err != nil {
		return fmt.Errorf("%w: invalid global_config json: %v", ErrInvalidTemplate, err)
	}
	if cfg.TopK <= 0 {
		cfg.TopK = types.DocDefaultTopK
	}
	if cfg.TopK > types.DocMaxTopK {
		cfg.TopK = types.DocMaxTopK
	}
	cfgBytes, _ := json.Marshal(cfg)
	tpl.GlobalConfig = types.JSON(cfgBytes)
	return nil
}

func clampInt(v, lo, hi int) int {
	if v == 0 {
		return hi
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// defaultDocChapters 不使用模板时的内置默认骨架（单章「正文」，按主题自由组织全文）
func defaultDocChapters() types.JSON {
	chapters := []types.DocTemplateChapter{
		{
			Title:         "正文",
			Instruction:   "围绕文档主题，结合知识库素材，完整、结构化地组织全文，章内可按需使用小标题分层",
			MaxWords:      types.DocTemplateMaxWordsUpper,
			MustReference: true,
		},
	}
	b, _ := json.Marshal(chapters)
	return types.JSON(b)
}

// ---- 生成任务 ----

// CreateGenerationTask 校验参数、写入快照、创建任务并入队
// template_id 为空时使用内置默认骨架（单章「正文」，按主题自由组织全文），实现"不使用模板"生成
func (s *DocAssistantService) CreateGenerationTask(ctx context.Context, task *types.DocGenerationTask) (*types.DocGenerationTask, error) {
	if task == nil || task.Theme == "" {
		return nil, ErrInvalidTask
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)

	var tpl *types.DocTemplate
	var chapters []types.DocTemplateChapter
	if task.TemplateID == "" {
		tpl = &types.DocTemplate{
			Name:        "不使用模板",
			Description: "按主题自由组织的默认骨架",
			Chapters:    defaultDocChapters(),
		}
		chapters, _ = tpl.DecodeChapters()
	} else {
		var err error
		tpl, err = s.templateRepo.FindByID(ctx, tenantID, ownerID, task.TemplateID)
		if err != nil {
			return nil, fmt.Errorf("%w: template %s", repository.ErrDocTemplateNotFound, task.TemplateID)
		}
		chapters, err = tpl.DecodeChapters()
		if err != nil || len(chapters) == 0 {
			return nil, ErrInvalidTemplate
		}
	}

	// 模板快照：模板被改后历史仍可还原当时骨架
	snapshotBytes, err := json.Marshal(tpl)
	if err != nil {
		return nil, err
	}
	var kbIDs []string
	if len(task.KbIDs) > 0 {
		if err := json.Unmarshal(task.KbIDs, &kbIDs); err != nil {
			return nil, fmt.Errorf("%w: invalid kb_ids", ErrInvalidTask)
		}
	}
	if len(kbIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one knowledge base is required", ErrInvalidTask)
	}

	// 自动归档目标（§6.9）：output_kb_id 为空时懒创建工作知识库；
	// 非空时必须是当前用户的私有工作知识库，不允许归档到 workspace 公共库。
	if task.AutoArchive {
		outputKBID, err := s.resolveArchiveKB(ctx, tenantID, ownerID, task.OutputKBID)
		if err != nil {
			return nil, err
		}
		task.OutputKBID = outputKBID
	}

	task.ID = types.NewDocAssistantID()
	task.TenantID = tenantID
	task.OwnerID = ownerID
	task.TemplateSnapshot = types.JSON(snapshotBytes)
	task.Status = types.DocTaskStatusPending
	task.ArchiveStatus = types.DocArchiveStatusNone
	task.ArchiveError = ""
	task.CreatedAt = time.Now().UTC()
	task.Progress = mustProgressJSON(chapters)

	if err := s.taskRepo.Create(ctx, task); err != nil {
		return nil, err
	}
	if err := s.enqueueGeneration(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *DocAssistantService) enqueueGeneration(ctx context.Context, task *types.DocGenerationTask) error {
	payload, err := json.Marshal(types.DocGenerationPayload{
		TaskID:   task.ID,
		TenantID: task.TenantID,
		OwnerID:  task.OwnerID,
	})
	if err != nil {
		return err
	}
	_, err = s.taskEnqueuer.Enqueue(
		asynq.NewTask(types.TypeDocumentGenerate, payload),
		asynq.MaxRetry(3),
	)
	if err != nil {
		logger.Errorf(ctx, "[DocAssistant] failed to enqueue generation task %s: %v", task.ID, err)
		return err
	}
	return nil
}

// resolveArchiveKB 校验/补齐自动归档目标知识库（§6.9）。
//
// 只接受当前用户的私有工作知识库：workspace 公共库、他人的私有库一律 400，
// 避免把个人文档写进共享空间。
func (s *DocAssistantService) resolveArchiveKB(
	ctx context.Context, tenantID uint64, ownerID, outputKBID string,
) (string, error) {
	if s.kbService == nil {
		return "", fmt.Errorf("%w: archive knowledge base service is not configured", ErrInvalidTask)
	}
	if outputKBID == "" {
		kb, err := s.kbService.EnsureWorkKnowledgeBase(ctx, tenantID, ownerID)
		if err != nil {
			return "", err
		}
		if kb == nil || kb.ID == "" {
			return "", fmt.Errorf("%w: work knowledge base is unavailable", ErrInvalidTask)
		}
		return kb.ID, nil
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, outputKBID)
	if err != nil || kb == nil {
		return "", fmt.Errorf("%w: output knowledge base not found", ErrInvalidTask)
	}
	if !kb.IsWorkKB() || !kb.IsPrivate() || kb.CreatorID != ownerID {
		return "", fmt.Errorf("%w: output knowledge base must be the caller's private work knowledge base", ErrInvalidTask)
	}
	return kb.ID, nil
}

// enqueueAutoArchive 在生成成功后入队一次自动归档（§6.9 第 2 步）。
//
// 入队失败只把任务标 failed + 写通知，生成结果本身仍然成功。
func (s *DocAssistantService) enqueueAutoArchive(ctx context.Context, task *types.DocGenerationTask) {
	if task == nil || !task.AutoArchive {
		return
	}
	if task.ArchiveStatus != types.DocArchiveStatusNone {
		// 已有终态（archived/skipped/failed）：不重复入队，避免用户删除归档后被重建。
		return
	}
	task.ArchiveStatus = types.DocArchiveStatusPending
	if err := s.taskRepo.MarkArchiveState(
		ctx, task.TenantID, task.OwnerID, task.ID, types.DocArchiveStatusPending, ""); err != nil {
		logger.Warnf(ctx, "[DocAssistant] failed to mark archive pending for %s: %v", task.ID, err)
	}
	payload, err := json.Marshal(types.DocArchivePayload{
		TaskID:   task.ID,
		TenantID: task.TenantID,
		OwnerID:  task.OwnerID,
	})
	if err == nil {
		_, err = s.taskEnqueuer.Enqueue(
			asynq.NewTask(types.TypeWorkbenchArchiveDoc, payload),
			asynq.MaxRetry(3),
		)
	}
	if err != nil {
		logger.Errorf(ctx, "[DocAssistant] failed to enqueue auto archive for %s: %v", task.ID, err)
		_ = s.taskRepo.MarkArchiveState(
			ctx, task.TenantID, task.OwnerID, task.ID,
			types.DocArchiveStatusFailed, "failed to enqueue auto archive")
	}
}

func (s *DocAssistantService) ListTasks(ctx context.Context) ([]*types.DocGenerationTask, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.taskRepo.ListByOwner(ctx, tenantID, ownerID)
}

func (s *DocAssistantService) GetTask(ctx context.Context, taskID string) (*types.DocGenerationTask, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.taskRepo.FindByID(ctx, tenantID, ownerID, taskID)
}

func (s *DocAssistantService) DeleteTask(ctx context.Context, taskID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	if _, err := s.taskRepo.FindByID(ctx, tenantID, ownerID, taskID); err != nil {
		return err
	}
	// 级联删除该任务下的文档（任务删除后历史列表不再展示）
	docs, err := s.documentRepo.ListByTask(ctx, tenantID, ownerID, taskID)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if err := s.documentRepo.Delete(ctx, tenantID, ownerID, doc.ID); err != nil {
			logger.Warnf(ctx, "[DocAssistant] failed to delete document %s: %v", doc.ID, err)
		}
	}
	return s.taskRepo.Delete(ctx, tenantID, ownerID, taskID)
}

// ---- 文档 ----

func (s *DocAssistantService) GetDocument(ctx context.Context, docID string) (*types.DocDocument, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.documentRepo.FindByID(ctx, tenantID, ownerID, docID)
}

func (s *DocAssistantService) ListDocuments(ctx context.Context, taskID string) ([]*types.DocDocument, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.documentRepo.ListByTask(ctx, tenantID, ownerID, taskID)
}

// ListAllDocuments 全局文档列表（P2-5）。
//
// 只返回摘要（标题取任务主题、不含 chapters），并按 latest_only 决定是否只取
// 每个任务的最新版本。归档状态由工作台服务在 handler 层补，避免 doc_assistant
// 反向依赖 workbench 包。
func (s *DocAssistantService) ListAllDocuments(
	ctx context.Context, filter types.DocDocumentFilter,
) (*types.DocDocumentPage, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	if filter.Limit <= 0 {
		filter.Limit = types.DocDocumentsDefaultLimit
	}
	if filter.Limit > types.DocDocumentsMaxLimit {
		filter.Limit = types.DocDocumentsMaxLimit
	}
	rows, hasMore, err := s.documentRepo.ListDocumentsByOwner(ctx, tenantID, ownerID, filter)
	if err != nil {
		if errors.Is(err, types.ErrWorkbenchInvalidCursor) {
			return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidTask)
		}
		return nil, err
	}
	page := &types.DocDocumentPage{Documents: rows}
	if page.Documents == nil {
		page.Documents = make([]*types.DocDocumentSummary, 0)
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor = repository.EncodeItemsCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *DocAssistantService) DeleteDocument(ctx context.Context, docID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)
	return s.documentRepo.Delete(ctx, tenantID, ownerID, docID)
}

// mustProgressJSON 依据章节骨架初始化进度 JSON
func mustProgressJSON(chapters []types.DocTemplateChapter) types.JSON {
	progress := types.DocTaskProgress{Chapters: make([]types.DocChapterProgress, 0, len(chapters))}
	for i, ch := range chapters {
		progress.Chapters = append(progress.Chapters, types.DocChapterProgress{
			No:     i + 1,
			Title:  ch.Title,
			Status: types.DocChapterStatusPending,
		})
	}
	b, _ := json.Marshal(progress)
	return types.JSON(b)
}

// getChatModelFor 解析生成模型：model_id 为空时取租户默认 KnowledgeQA 模型
func (s *DocAssistantService) getChatModelFor(ctx context.Context, modelID string) (chat.Chat, error) {
	if modelID != "" {
		return s.modelSvc.GetChatModel(ctx, modelID)
	}
	models, err := s.modelSvc.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
			return s.modelSvc.GetChatModel(ctx, m.ID)
		}
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA {
			return s.modelSvc.GetChatModel(ctx, m.ID)
		}
	}
	return nil, fmt.Errorf("%w: no chat model available, configure one in model management first", ErrInvalidTask)
}
