package docassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// 检索/生成控制常量
const (
	minMaterialRunes      = 50    // 命中素材总字符数下限，低于视为素材不足
	chunkContentMaxRunes  = 600   // 单个检索片段送入 LLM 的最大字符数
	contextMaxRunes       = 6000  // 全部片段拼接后的最大字符数
	referencesMaxChunks   = 200   // 单章引用 chunk 数上限（防引用爆炸）
)

// ProcessGeneration 异步任务 handler：逐章生成，checkpoint 续跑
func (s *DocAssistantService) ProcessGeneration(ctx context.Context, t *asynq.Task) error {
	var payload types.DocGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return nil // 不可反序列化不重试
	}

	// worker 上下文无租户，从 payload 重建
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	// 检索依赖完整租户信息（embedding/向量库配置），参照数据源同步的注入方式
	tenant, terr := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if terr != nil {
		logger.Errorf(ctx, "[DocAssistant] tenant %d info lookup failed: %v", payload.TenantID, terr)
		return terr
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	task, err := s.taskRepo.FindByID(ctx, payload.TenantID, payload.OwnerID, payload.TaskID)
	if err != nil {
		logger.Errorf(ctx, "[DocAssistant] task %s not found: %v", payload.TaskID, err)
		return nil // 任务已删，无需重试
	}
	// 幂等：已终态不重跑
	if task.Status == types.DocTaskStatusSuccess ||
		task.Status == types.DocTaskStatusPartial ||
		task.Status == types.DocTaskStatusFailed ||
		task.Status == types.DocTaskStatusCanceled {
		return nil
	}

	task.Status = types.DocTaskStatusRunning
	task.Error = ""
	if err := s.taskRepo.Update(ctx, task); err != nil {
		return err
	}

	// 解码任务上下文
	tpl, kbIDs, chapters, progress, err := s.decodeTaskContext(task)
	if err != nil {
		task.Status = types.DocTaskStatusFailed
		task.Error = err.Error()
		task.FinishedAt = ptrTime(time.Now().UTC())
		_ = s.taskRepo.Update(ctx, task)
		return err
	}
	_ = tpl // 快照模板对象用于全局配置

	chatModel, err := s.getChatModelFor(ctx, task.ModelID)
	if err != nil {
		task.Status = types.DocTaskStatusFailed
		task.Error = err.Error()
		task.FinishedAt = ptrTime(time.Now().UTC())
		_ = s.taskRepo.Update(ctx, task)
		return err
	}

	// 文档 v1：存在则续写，不存在则创建
	doc, err := s.ensureDocumentV1(ctx, task)
	if err != nil {
		return err
	}
	docChapters, err := decodeDocChapters(doc)
	if err != nil {
		return err
	}

	theme := task.Theme
	extra := task.ExtraRequirement
	globalConfig, _ := tpl.DecodeGlobalConfig()

	// 逐章（从第一个非 done 章节续跑）
	start := 0
	for i := range progress.Chapters {
		if progress.Chapters[i].Status == types.DocChapterStatusDone {
			start = i + 1
			continue
		}
		break
	}

	for i := start; i < len(chapters); i++ {
		chapter := chapters[i]
		logger.Infof(ctx, "[DocAssistant] task %s chapter %d/%d: %s", task.ID, i+1, len(chapters), chapter.Title)

		progress.Chapters[i].Status = types.DocChapterStatusRetrieving
		task.Progress = encodeProgress(progress)
		_ = s.taskRepo.Update(ctx, task)

		generated, err := s.generateOneChapter(ctx, chapter, theme, extra, kbIDs, globalConfig, chatModel)
		if err != nil {
			progress.Chapters[i].Status = types.DocChapterStatusFailed
			progress.Chapters[i].Title = chapter.Title
			logger.Errorf(ctx, "[DocAssistant] chapter %d failed: %v", i+1, err)
		} else {
			progress.Chapters[i].Status = types.DocChapterStatusDone
			progress.Chapters[i].Title = chapter.Title
		}

		// 章节写回文档（skipped/failed 也落位，便于预览与重生成）
		upsertDocChapter(&docChapters, i+1, chapter.Title, generated)
		doc.Chapters = encodeDocChapters(docChapters)
		doc.Status = types.DocDocumentStatusGenerated
		_ = s.documentRepo.Update(ctx, doc)

		task.Progress = encodeProgress(progress)
		_ = s.taskRepo.Update(ctx, task)
	}

	// 收尾状态
	succeed, failed, skipped := 0, 0, 0
	for _, p := range progress.Chapters {
		switch p.Status {
		case types.DocChapterStatusDone:
			succeed++
		case types.DocChapterStatusFailed:
			failed++
		case types.DocChapterStatusSkipped:
			skipped++
		}
	}
	now := time.Now().UTC()
	task.FinishedAt = ptrTime(now)
	switch {
	case succeed == 0 && skipped == 0:
		task.Status = types.DocTaskStatusFailed
		task.Error = "all chapters failed to generate"
	case succeed == 0:
		task.Status = types.DocTaskStatusFailed
		task.Error = "no material found in selected knowledge bases for any chapter"
	case failed > 0:
		task.Status = types.DocTaskStatusPartial
	default:
		task.Status = types.DocTaskStatusSuccess
	}
	if err := s.taskRepo.Update(ctx, task); err != nil {
		return err
	}
	logger.Infof(ctx, "[DocAssistant] task %s finished status=%s succeed=%d failed=%d skipped=%d",
		task.ID, task.Status, succeed, failed, skipped)
	return nil
}

// generateOneChapter 单章：检索 → 素材判定 → LLM 生成 → 引用归并
func (s *DocAssistantService) generateOneChapter(
	ctx context.Context,
	chapter types.DocTemplateChapter,
	theme, extra string,
	kbIDs []string,
	globalConfig types.DocTemplateGlobalConfig,
	chatModel chat.Chat,
) (types.DocChapter, error) {
	query := buildChapterQuery(theme, extra, chapter)
	logger.Infof(ctx, "[DocAssistant] retrieving chapter=%q query=%q", chapter.Title, truncateRunes(query, 120))

	// v0.8.3 上游将 SearchKnowledge 新增 opts 参数，返回值改为 *RetrievalResult
	retrieval, err := s.sessionSvc.SearchKnowledge(ctx, kbIDs, nil, nil, query, nil)
	if err != nil {
		return types.DocChapter{}, fmt.Errorf("retrieve failed: %w", err)
	}
	results := retrieval.Results

	material := buildMaterialContext(results)
	insufficient := len(results) == 0 || runeLen(material) < minMaterialRunes

	if insufficient && chapter.MustReference {
		// 素材不足且强制引用 → 跳过该章
		logger.Infof(ctx, "[DocAssistant] chapter %q skipped: insufficient material", chapter.Title)
		return types.DocChapter{
			Status:  types.DocChapterStatusSkipped,
			Message: "素材不足，本章未生成（未在所选知识库中找到相关素材）",
		}, nil
	}

	content, err := s.callChapterLLM(ctx, chatModel, chapter, theme, extra, material, globalConfig, insufficient)
	if err != nil {
		return types.DocChapter{}, err
	}

	return types.DocChapter{
		Status:     types.DocChapterStatusDone,
		Content:    content,
		References: buildReferences(results),
	}, nil
}

// callChapterLLM 组装章节 prompt 并调用模型
func (s *DocAssistantService) callChapterLLM(
	ctx context.Context,
	chatModel chat.Chat,
	chapter types.DocTemplateChapter,
	theme, extra, material string,
	globalConfig types.DocTemplateGlobalConfig,
	insufficient bool,
) (string, error) {
	var systemParts []string
	systemParts = append(systemParts, "你是一位资深文档撰写专家。请严格按用户给定的章节要求撰写文档章节，输出 Markdown 正文。章节标题由系统统一添加，正文中不要重复输出本章节标题行；如需小节标题，请从 ### 三级标题开始使用。")
	systemParts = append(systemParts, fmt.Sprintf("文档主题：%s", theme))
	if extra != "" {
		systemParts = append(systemParts, fmt.Sprintf("补充要求：%s", extra))
	}
	if globalConfig.TargetAudience != "" {
		systemParts = append(systemParts, fmt.Sprintf("目标读者：%s", globalConfig.TargetAudience))
	}
	if globalConfig.DefaultTone != "" {
		systemParts = append(systemParts, fmt.Sprintf("语气风格：%s", globalConfig.DefaultTone))
	}

	var userParts []string
	userParts = append(userParts, fmt.Sprintf("章节标题：%s", chapter.Title))
	if chapter.Instruction != "" {
		userParts = append(userParts, fmt.Sprintf("本章撰写要求：%s", chapter.Instruction))
	}
	maxWords := chapter.MaxWords
	if maxWords <= 0 {
		maxWords = types.DocTemplateMaxWordsUpper
	}
	userParts = append(userParts, fmt.Sprintf("期望篇幅：不超过 %d 字。", maxWords))
	if insufficient {
		userParts = append(userParts, "注意：所选知识库中未检索到本章相关素材，请基于通用知识撰写，并在章节末尾标注「本章基于通用知识，未引用知识库」。")
	} else if material != "" {
		userParts = append(userParts, "以下是从知识库检索到的素材，请优先基于素材撰写，不要编造素材之外的事实：")
		userParts = append(userParts, "【知识库素材】")
		userParts = append(userParts, material)
	}

	modelCtx := types.WithLLMCallMetadata(ctx, "doc_assistant_chapter", "")
	resp, err := chatModel.Chat(modelCtx, []chat.Message{
		{Role: "system", Content: strings.Join(systemParts, "\n")},
		{Role: "user", Content: strings.Join(userParts, "\n")},
	}, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   clampInt(maxWords*2, 800, 8000),
	})
	if err != nil {
		return "", fmt.Errorf("llm call failed: %w", err)
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", fmt.Errorf("llm returned empty content")
	}
	return content, nil
}

// ---- 解码/编码辅助 ----

func (s *DocAssistantService) decodeTaskContext(task *types.DocGenerationTask) (
	*types.DocTemplate, []string, []types.DocTemplateChapter, *types.DocTaskProgress, error,
) {
	var tpl types.DocTemplate
	if err := json.Unmarshal(task.TemplateSnapshot, &tpl); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("invalid template snapshot: %w", err)
	}
	chapters, err := tpl.DecodeChapters()
	if err != nil || len(chapters) == 0 {
		return nil, nil, nil, nil, ErrInvalidTemplate
	}
	var kbIDs []string
	if err := json.Unmarshal(task.KbIDs, &kbIDs); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("invalid kb_ids: %w", err)
	}
	progress := &types.DocTaskProgress{}
	if len(task.Progress) > 0 {
		if err := json.Unmarshal(task.Progress, progress); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("invalid progress: %w", err)
		}
	}
	// 进度与骨架对齐（骨架变了也按当前进度续跑）
	if len(progress.Chapters) != len(chapters) {
		progress.Chapters = make([]types.DocChapterProgress, 0, len(chapters))
		for i, ch := range chapters {
			progress.Chapters = append(progress.Chapters, types.DocChapterProgress{
				No: i + 1, Title: ch.Title, Status: types.DocChapterStatusPending,
			})
		}
	}
	return &tpl, kbIDs, chapters, progress, nil
}

// ensureDocumentV1 获取或创建任务下的 v1 文档
func (s *DocAssistantService) ensureDocumentV1(ctx context.Context, task *types.DocGenerationTask) (*types.DocDocument, error) {
	docs, err := s.documentRepo.ListByTask(ctx, task.TenantID, task.OwnerID, task.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		if d.Version == 1 {
			return d, nil
		}
	}
	doc := &types.DocDocument{
		ID:        types.NewDocAssistantID(),
		TenantID:  task.TenantID,
		OwnerID:   task.OwnerID,
		TaskID:    task.ID,
		Version:   1,
		Status:    types.DocDocumentStatusGenerated,
		Chapters:  types.JSON([]byte("[]")),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.documentRepo.Create(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// RegenerateChapter 单章重生成（新版本仅替换该章）
func (s *DocAssistantService) RegenerateChapter(ctx context.Context, docID string, chapterNo int) (*types.DocDocument, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)

	base, err := s.documentRepo.FindByID(ctx, tenantID, ownerID, docID)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRepo.FindByID(ctx, base.TenantID, base.OwnerID, base.TaskID)
	if err != nil {
		return nil, err
	}
	_, kbIDs, chapters, _, err := s.decodeTaskContext(task)
	if err != nil {
		return nil, err
	}
	if chapterNo < 1 || chapterNo > len(chapters) {
		return nil, fmt.Errorf("%w: chapter %d out of range", ErrChapterNotFound, chapterNo)
	}
	chatModel, err := s.getChatModelFor(ctx, task.ModelID)
	if err != nil {
		return nil, err
	}

	baseChapters, err := decodeDocChapters(base)
	if err != nil {
		return nil, err
	}
	generated, err := s.generateOneChapter(ctx, chapters[chapterNo-1], task.Theme, task.ExtraRequirement, kbIDs, taskGlobalConfig(task), chatModel)
	if err != nil {
		return nil, err
	}
	upsertDocChapter(&baseChapters, chapterNo, chapters[chapterNo-1].Title, generated)

	newDoc := cloneDocumentForNextVersion(base)
	newDoc.Chapters = encodeDocChapters(baseChapters)
	newDoc.Status = types.DocDocumentStatusGenerated
	if err := s.documentRepo.Create(ctx, newDoc); err != nil {
		return nil, err
	}
	return newDoc, nil
}

// UpdateChapterContent 编辑章节文字（新版本）
func (s *DocAssistantService) UpdateChapterContent(ctx context.Context, docID string, chapterNo int, content string) (*types.DocDocument, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	ownerID, _ := types.UserIDFromContext(ctx)

	base, err := s.documentRepo.FindByID(ctx, tenantID, ownerID, docID)
	if err != nil {
		return nil, err
	}
	baseChapters, err := decodeDocChapters(base)
	if err != nil {
		return nil, err
	}
	if chapterNo < 1 || chapterNo > len(baseChapters) {
		return nil, fmt.Errorf("%w: chapter %d out of range", ErrChapterNotFound, chapterNo)
	}
	baseChapters[chapterNo-1].Content = content
	baseChapters[chapterNo-1].Status = types.DocChapterStatusDone

	newDoc := cloneDocumentForNextVersion(base)
	newDoc.Chapters = encodeDocChapters(baseChapters)
	newDoc.Status = types.DocDocumentStatusEdited
	if err := s.documentRepo.Create(ctx, newDoc); err != nil {
		return nil, err
	}
	return newDoc, nil
}

// cloneDocumentForNextVersion 基于旧文档复制出下一个版本（内容已替换后调用）
func cloneDocumentForNextVersion(base *types.DocDocument) *types.DocDocument {
	return &types.DocDocument{
		ID:        types.NewDocAssistantID(),
		TenantID:  base.TenantID,
		OwnerID:   base.OwnerID,
		TaskID:    base.TaskID,
		Version:   base.Version + 1,
		CreatedAt: time.Now().UTC(),
	}
}

// ---- 纯函数辅助 ----

func buildChapterQuery(theme, extra string, chapter types.DocTemplateChapter) string {
	var parts []string
	parts = append(parts, theme)
	if extra != "" {
		parts = append(parts, extra)
	}
	parts = append(parts, chapter.Title)
	if chapter.Instruction != "" {
		parts = append(parts, chapter.Instruction)
	}
	return strings.Join(parts, " ")
}

// buildMaterialContext 将检索片段按知识归并、截断为 LLM 素材文本
func buildMaterialContext(results []*types.SearchResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	total := 0
	for _, r := range results {
		if r == nil || strings.TrimSpace(r.Content) == "" {
			continue
		}
		piece := truncateRunes(r.Content, chunkContentMaxRunes)
		if total+runeLen(piece) > contextMaxRunes {
			sb.WriteString("[...内容省略...]\n")
			break
		}
		title := r.KnowledgeTitle
		if title == "" {
			title = r.KnowledgeFilename
		}
		if title == "" {
			title = "(未命名文档)"
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", title, piece))
		total += runeLen(piece)
	}
	return sb.String()
}

// buildReferences 按来源知识归并引用
func buildReferences(results []*types.SearchResult) []types.DocReference {
	if len(results) == 0 {
		return nil
	}
	byID := make(map[string]*types.DocReference, len(results))
	var order []string
	for _, r := range results {
		if r == nil || r.KnowledgeID == "" {
			continue
		}
		ref, ok := byID[r.KnowledgeID]
		if !ok {
			title := r.KnowledgeTitle
			if title == "" {
				title = r.KnowledgeFilename
			}
			ref = &types.DocReference{
				KnowledgeID: r.KnowledgeID,
				Title:       title,
				Filename:    r.KnowledgeFilename,
			}
			byID[r.KnowledgeID] = ref
			order = append(order, r.KnowledgeID)
		}
		if len(ref.ChunkIDs) < referencesMaxChunks && r.ID != "" {
			ref.ChunkIDs = append(ref.ChunkIDs, r.ID)
		}
	}
	refs := make([]types.DocReference, 0, len(order))
	for _, id := range order {
		refs = append(refs, *byID[id])
	}
	return refs
}

func taskGlobalConfig(task *types.DocGenerationTask) types.DocTemplateGlobalConfig {
	var tpl types.DocTemplate
	_ = json.Unmarshal(task.TemplateSnapshot, &tpl)
	cfg, _ := tpl.DecodeGlobalConfig()
	return cfg
}

func decodeDocChapters(doc *types.DocDocument) ([]types.DocChapter, error) {
	var chapters []types.DocChapter
	if len(doc.Chapters) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(doc.Chapters, &chapters); err != nil {
		return nil, err
	}
	return chapters, nil
}

func encodeDocChapters(chapters []types.DocChapter) types.JSON {
	b, _ := json.Marshal(chapters)
	return types.JSON(b)
}

func upsertDocChapter(chapters *[]types.DocChapter, no int, title string, ch types.DocChapter) {
	idx := -1
	for i := range *chapters {
		if (*chapters)[i].No == no {
			idx = i
			break
		}
	}
	ch.No = no
	ch.Title = title
	if idx >= 0 {
		(*chapters)[idx] = ch
		return
	}
	*chapters = append(*chapters, ch)
}

func encodeProgress(p *types.DocTaskProgress) types.JSON {
	b, _ := json.Marshal(p)
	return types.JSON(b)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func runeLen(s string) int {
	return len([]rune(s))
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
