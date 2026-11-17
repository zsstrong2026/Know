// 主题分析的两阶段执行：阶段一抽证据并停在 waiting_confirm，阶段二确认后生成报告。
//
// 后台任务使用独立 context.Background()（只注入执行租户），不随 HTTP 请求取消；
// 取消语义由 run 行的 status=cancelled 表达，每篇文档开始前查一次。
package insight

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// runOptions 是写进 insight_runs.options_json 的运行参数。
type runOptions struct {
	Sample       bool     `json:"sample"`
	MaxDocs      int      `json:"max_docs"`
	FolderPath   string   `json:"folder_path"`
	RecentDays   int      `json:"recent_days"`
	KnowledgeIDs []string `json:"knowledge_ids"`
}

// chatModelFor 取聊天模型：租户默认 KnowledgeQA 优先，没有可用模型返回 2408。
// 口径与 workbench/ai.go 的 chatModelFor 保持一致。
func (s *Service) chatModelFor(ctx context.Context) (chat.Chat, error) {
	if s.models == nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	var fallback *types.Model
	for _, m := range models {
		if m.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if m.IsDefault {
			if model, getErr := s.models.GetChatModel(ctx, m.ID); getErr == nil && model != nil {
				return model, nil
			}
		}
		if fallback == nil {
			fallback = m
		}
	}
	if fallback == nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	model, err := s.models.GetChatModel(ctx, fallback.ID)
	if err != nil || model == nil {
		return nil, apperrors.NewWorkbenchNoModel()
	}
	return model, nil
}

// StartRun 创建运行记录并异步启动阶段一。
func (s *Service) StartRun(
	ctx context.Context, themeID string, req types.StartInsightRunRequest,
) (*types.InsightRun, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	theme, err := s.repo.GetTheme(ctx, tenantID, ownerID, themeID)
	if err != nil {
		return nil, mapNotFound(err, "insight theme not found")
	}
	// 启动前先解析模型：没有可用模型时直接返回 2408，而不是异步跑一半再失败。
	if _, err := s.chatModelFor(ctx); err != nil {
		return nil, err
	}
	docs, err := s.listWorkDocs(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	tally := s.classifyDocsWithText(ctx, tenantID, filterDocs(docs, req.ScopeFilter()))
	selected := selectAnalyzedDocs(tally.analyzable, req)
	if len(selected) == 0 {
		return nil, apperrors.NewValidationError("no analyzable documents in work knowledge base")
	}
	ids := make([]string, 0, len(selected))
	for _, doc := range selected {
		ids = append(ids, doc.ID)
	}
	rawOptions, err := json.Marshal(runOptions{
		Sample:       req.Sample,
		MaxDocs:      req.MaxDocs,
		FolderPath:   req.FolderPath,
		RecentDays:   req.RecentDays,
		KnowledgeIDs: ids,
	})
	if err != nil {
		return nil, apperrors.NewValidationError("invalid run options")
	}
	now := time.Now()
	run := &types.InsightRun{
		ID:          uuid.New().String(),
		ThemeID:     theme.ID,
		Status:      types.InsightStatusPending,
		Stage:       types.InsightStageCheckup,
		Progress:    0,
		DocsTotal:   len(selected),
		OptionsJSON: types.JSON(rawOptions),
		Message:     "已创建分析任务",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateRun(ctx, tenantID, ownerID, run); err != nil {
		return nil, err
	}
	go s.executeStageOne(run.ID, tenantID, ownerID)
	return run, nil
}

// GetRun 读取一次运行。
func (s *Service) GetRun(ctx context.Context, id string) (*types.InsightRun, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err, "insight run not found")
	}
	return run, nil
}

// CancelRun 取消运行；已取消时幂等，已结束的运行拒绝取消。
func (s *Service) CancelRun(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, id)
	if err != nil {
		return mapNotFound(err, "insight run not found")
	}
	if run.Status == types.InsightStatusCancelled {
		return nil
	}
	if isTerminalStatus(run.Status) {
		return apperrors.NewValidationError("insight run has already finished")
	}
	now := time.Now()
	return s.repo.UpdateRunFields(ctx, tenantID, ownerID, id, map[string]interface{}{
		"status":      types.InsightStatusCancelled,
		"message":     "已取消",
		"finished_at": now,
	})
}

// ListEvidence 返回一次运行的全部证据与排除计数。
func (s *Service) ListEvidence(ctx context.Context, runID string) (*types.InsightEvidenceList, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetRun(ctx, tenantID, ownerID, runID); err != nil {
		return nil, mapNotFound(err, "insight run not found")
	}
	items, err := s.repo.ListEvidence(ctx, tenantID, ownerID, runID)
	if err != nil {
		return nil, err
	}
	excluded := 0
	for _, item := range items {
		if item.Excluded {
			excluded++
		}
	}
	return &types.InsightEvidenceList{Items: items, Total: len(items), Excluded: excluded}, nil
}

// ExcludeEvidence 批量排除/恢复证据。
func (s *Service) ExcludeEvidence(
	ctx context.Context, runID string, req types.ExcludeInsightEvidenceRequest,
) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	if _, err := s.repo.GetRun(ctx, tenantID, ownerID, runID); err != nil {
		return mapNotFound(err, "insight run not found")
	}
	if len(req.IDs) == 0 {
		return apperrors.NewValidationError("evidence ids are required")
	}
	if len(req.IDs) > 500 {
		return apperrors.NewValidationError("too many evidence ids")
	}
	return s.repo.SetEvidenceExcluded(ctx, tenantID, ownerID, runID, req.IDs, req.Excluded)
}

// ConfirmRun 确认证据并异步启动阶段二。
func (s *Service) ConfirmRun(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, id)
	if err != nil {
		return mapNotFound(err, "insight run not found")
	}
	if run.Status != types.InsightStatusWaitingConfirm {
		return apperrors.NewValidationError("insight run is not waiting for confirmation")
	}
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, id, map[string]interface{}{
		"status":   types.InsightStatusStage2,
		"stage":    types.InsightStageSynthesis,
		"progress": 92,
		"message":  "正在生成报告",
	}); err != nil {
		return err
	}
	go s.executeStageTwo(id, tenantID, ownerID)
	return nil
}

// ListReports 列出主题下的报告（只返回摘要）。
func (s *Service) ListReports(ctx context.Context, themeID string) ([]*types.InsightReportSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTheme(ctx, tenantID, ownerID, themeID); err != nil {
		return nil, mapNotFound(err, "insight theme not found")
	}
	reports, err := s.repo.ListReportsByTheme(ctx, tenantID, ownerID, themeID)
	if err != nil {
		return nil, err
	}
	out := make([]*types.InsightReportSummary, 0, len(reports))
	for _, report := range reports {
		out = append(out, &types.InsightReportSummary{
			ID:            report.ID,
			ThemeID:       report.ThemeID,
			RunID:         report.RunID,
			Title:         report.Title,
			CreatedAt:     report.CreatedAt,
			DocsCovered:   report.DocsCovered,
			EvidenceCount: report.EvidenceCount,
			ModelID:       report.ModelID,
		})
	}
	return out, nil
}

// GetReport 读取报告详情；主题已删除时仍可读，theme_name 留空。
func (s *Service) GetReport(ctx context.Context, id string) (*types.InsightReportDetail, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err, "insight report not found")
	}
	themeName := ""
	if theme, themeErr := s.repo.GetTheme(ctx, tenantID, ownerID, report.ThemeID); themeErr == nil {
		themeName = theme.Name
	}
	return &types.InsightReportDetail{
		InsightReportSummary: types.InsightReportSummary{
			ID:            report.ID,
			ThemeID:       report.ThemeID,
			Title:         report.Title,
			CreatedAt:     report.CreatedAt,
			DocsCovered:   report.DocsCovered,
			EvidenceCount: report.EvidenceCount,
			ModelID:       report.ModelID,
		},
		ThemeName: themeName,
		Content:   report.ContentJSON,
	}, nil
}

// DeleteReport 删除单份报告。
func (s *Service) DeleteReport(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	if _, err := s.repo.GetReport(ctx, tenantID, ownerID, id); err != nil {
		return mapNotFound(err, "insight report not found")
	}
	// 会话消息挂在报告下，随报告一起清理，避免留下孤儿消息。
	if err := s.repo.DeleteReportMessages(ctx, tenantID, ownerID, id); err != nil {
		return err
	}
	return s.repo.DeleteReport(ctx, tenantID, ownerID, id)
}

// DeleteRun 删除运行及其证据；进行中的运行必须先取消。
func (s *Service) DeleteRun(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, id)
	if err != nil {
		return mapNotFound(err, "insight run not found")
	}
	if isRunningStatus(run.Status) {
		return apperrors.NewValidationError("cancel the insight run before deleting it")
	}
	if err := s.repo.DeleteEvidenceByRun(ctx, tenantID, ownerID, id); err != nil {
		return err
	}
	return s.repo.DeleteRun(ctx, tenantID, ownerID, id)
}

// runEngine 承载一次阶段一的并发状态。
type runEngine struct {
	svc      *Service
	ctx      context.Context
	tenantID uint64
	ownerID  string
	run      *types.InsightRun
	theme    *types.InsightTheme
	model    chat.Chat
	total    int

	mu       sync.Mutex
	done     int
	evidence int

	failOnce sync.Once
	failed   atomic.Bool
}

// executeStageOne 阶段一：并发抽取证据，完成后进入 waiting_confirm。
func (s *Service) executeStageOne(runID string, tenantID uint64, ownerID string) {
	ctx := types.WithExecutionTenant(context.Background(), tenantID)
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, runID)
	if err != nil || run == nil {
		logger.Warnf(ctx, "insight stage1: run %s not found: %v", runID, err)
		return
	}
	if run.Status == types.InsightStatusCancelled {
		return
	}
	theme, err := s.repo.GetTheme(ctx, tenantID, ownerID, run.ThemeID)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "主题已不存在")
		return
	}
	model, err := s.chatModelFor(ctx)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "没有可用的聊天模型")
		return
	}
	started := time.Now()
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, runID, map[string]interface{}{
		"status":     types.InsightStatusStage1,
		"stage":      types.InsightStageEvidence,
		"progress":   5,
		"message":    "正在读取文档",
		"started_at": started,
		"error":      "",
	}); err != nil {
		logger.Warnf(ctx, "insight stage1: update run failed: %v", err)
		return
	}

	var options runOptions
	if len(run.OptionsJSON) > 0 {
		_ = json.Unmarshal(run.OptionsJSON, &options)
	}
	docs, err := s.listWorkDocs(ctx, tenantID, ownerID)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "工作知识库不可用")
		return
	}
	byID := make(map[string]*types.Knowledge, len(docs))
	for _, doc := range docs {
		byID[doc.ID] = doc
	}
	selected := make([]*types.Knowledge, 0, len(options.KnowledgeIDs))
	for _, id := range options.KnowledgeIDs {
		if doc, ok := byID[id]; ok {
			selected = append(selected, doc)
		}
	}
	if len(selected) == 0 {
		s.failRun(ctx, tenantID, ownerID, runID, "没有可分析的文档")
		return
	}
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, runID, map[string]interface{}{
		"docs_total": len(selected),
		"message":    "正在抽取证据",
	}); err != nil {
		logger.Warnf(ctx, "insight stage1: update docs_total failed: %v", err)
	}

	engine := &runEngine{
		svc: s, ctx: ctx, tenantID: tenantID, ownerID: ownerID,
		run: run, theme: theme, model: model, total: len(selected),
	}
	var next int64
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(atomic.AddInt64(&next, 1) - 1)
				if idx >= len(selected) || engine.failed.Load() {
					return
				}
				if engine.cancelled() {
					return
				}
				engine.processDoc(selected[idx])
			}
		}()
	}
	wg.Wait()

	if engine.failed.Load() {
		return
	}
	if engine.cancelled() {
		return
	}
	engine.mu.Lock()
	evidenceCount := engine.evidence
	engine.mu.Unlock()
	// 即使证据为空也进入等待确认：让用户看到“没有证据”并可取消，
	// 而不是把一次正常跑完的任务伪装成失败。
	message := "证据已生成，请确认后生成报告"
	if evidenceCount == 0 {
		message = "未抽取到可用证据，可取消后调整主题再试"
	}
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, runID, map[string]interface{}{
		"status":   types.InsightStatusWaitingConfirm,
		"stage":    types.InsightStageEvidence,
		"progress": 90,
		"message":  message,
	}); err != nil {
		logger.Warnf(ctx, "insight stage1: finalize run failed: %v", err)
	}
}

// cancelled 读取数据库中的运行状态，作为跨 goroutine 的取消信号。
func (e *runEngine) cancelled() bool {
	run, err := e.svc.repo.GetRun(e.ctx, e.tenantID, e.ownerID, e.run.ID)
	if err != nil {
		return true
	}
	return run.Status == types.InsightStatusCancelled
}

// processDoc 分析单篇文档：拼接正文 → 调模型 → 写证据 → 更新进度。
func (e *runEngine) processDoc(doc *types.Knowledge) {
	chunks, err := e.svc.chunks.ListChunksByKnowledgeID(e.ctx, e.tenantID, doc.ID)
	if err != nil {
		logger.Warnf(e.ctx, "insight stage1: list chunks for %s failed: %v", doc.ID, err)
		e.markProcessed(0)
		return
	}
	var body strings.Builder
	for _, chunk := range chunks {
		if chunk == nil || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		fmt.Fprintf(&body, "[段%d] %s\n", chunk.ChunkIndex, chunk.Content)
	}
	text := truncateRunes(body.String(), types.InsightDocTextLimit)
	if strings.TrimSpace(text) == "" {
		e.markProcessed(0)
		return
	}

	var out evidenceOutput
	messages := buildEvidenceMessages(e.theme, doc, text)
	if err := callChatJSON(e.ctx, e.model, messages, &out); err != nil {
		// 单篇失败不影响整次运行：记录后继续下一篇。
		logger.Warnf(e.ctx, "insight stage1: extract evidence for %s failed: %v", doc.ID, err)
		e.markProcessed(0)
		return
	}

	allowed := make(map[string]struct{})
	for _, dim := range e.theme.DimensionList() {
		allowed[dim.Name] = struct{}{}
	}
	now := time.Now()
	items := make([]*types.InsightEvidence, 0, len(out.Evidence))
	for _, item := range out.Evidence {
		dimension := strings.TrimSpace(item.Dimension)
		if _, ok := allowed[dimension]; !ok {
			continue
		}
		quote := truncateRunes(strings.TrimSpace(item.Quote), types.InsightQuoteRuneLimit)
		if quote == "" {
			continue
		}
		items = append(items, &types.InsightEvidence{
			ID:             uuid.New().String(),
			RunID:          e.run.ID,
			ThemeID:        e.theme.ID,
			KnowledgeID:    doc.ID,
			KnowledgeTitle: documentTitle(doc),
			ChunkIndex:     item.ChunkIndex,
			Dimension:      dimension,
			Quote:          quote,
			EvidenceType:   normalizeEvidenceType(item.EvidenceType),
			Confidence:     normalizeConfidence(item.Confidence),
			CreatedAt:      now,
		})
	}
	if len(items) > 0 {
		if err := e.svc.repo.CreateEvidenceBatch(e.ctx, e.tenantID, e.ownerID, items); err != nil {
			logger.Warnf(e.ctx, "insight stage1: save evidence failed: %v", err)
			e.fail("证据写入失败")
			return
		}
	}
	e.markProcessed(len(items))
}

// markProcessed 串行更新计数与进度，避免并发读改写互相覆盖。
func (e *runEngine) markProcessed(evidenceDelta int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.done++
	e.evidence += evidenceDelta
	progress := 10
	if e.total > 0 {
		progress = 10 + 80*e.done/e.total
	}
	if progress > 90 {
		progress = 90
	}
	message := fmt.Sprintf("已分析 %d/%d 篇文档", e.done, e.total)
	if err := e.svc.repo.UpdateRunFields(e.ctx, e.tenantID, e.ownerID, e.run.ID, map[string]interface{}{
		"docs_processed": e.done,
		"evidence_count": e.evidence,
		"progress":       progress,
		"message":        message,
	}); err != nil {
		logger.Warnf(e.ctx, "insight stage1: update progress failed: %v", err)
	}
}

// fail 只记录第一次失败的文档错误，避免多个 worker 互相覆盖状态。
func (e *runEngine) fail(message string) {
	e.failOnce.Do(func() {
		e.failed.Store(true)
		e.svc.failRun(e.ctx, e.tenantID, e.ownerID, e.run.ID, message)
	})
}

// executeStageTwo 阶段二：用未排除证据生成报告。
func (s *Service) executeStageTwo(runID string, tenantID uint64, ownerID string) {
	ctx := types.WithExecutionTenant(context.Background(), tenantID)
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, runID)
	if err != nil || run == nil {
		logger.Warnf(ctx, "insight stage2: run %s not found: %v", runID, err)
		return
	}
	if run.Status != types.InsightStatusStage2 {
		return
	}
	theme, err := s.repo.GetTheme(ctx, tenantID, ownerID, run.ThemeID)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "主题已不存在")
		return
	}
	evidence, err := s.repo.ListEvidenceForReport(
		ctx, tenantID, ownerID, runID, types.InsightMaxEvidence)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "读取证据失败")
		return
	}
	if len(evidence) == 0 {
		s.failRun(ctx, tenantID, ownerID, runID, "没有可用证据")
		return
	}
	model, err := s.chatModelFor(ctx)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "没有可用的聊天模型")
		return
	}

	var out reportOutput
	messages := buildReportMessages(theme, evidence, run.DocsProcessed)
	if err := callChatJSON(ctx, model, messages, &out); err != nil {
		logger.Warnf(ctx, "insight stage2: generate report failed: %v", err)
		s.failRun(ctx, tenantID, ownerID, runID, "报告生成失败")
		return
	}
	if strings.TrimSpace(out.Conclusion) == "" || len(out.Scores) == 0 {
		s.failRun(ctx, tenantID, ownerID, runID, "模型返回的报告结构不完整")
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		s.failRun(ctx, tenantID, ownerID, runID, "报告序列化失败")
		return
	}
	if s.runCancelled(ctx, tenantID, ownerID, runID) {
		return
	}

	report := &types.InsightReport{
		ID:            uuid.New().String(),
		ThemeID:       theme.ID,
		RunID:         run.ID,
		Title:         theme.Name + " · 洞察报告",
		ModelID:       model.GetModelID(),
		DocsCovered:   run.DocsProcessed,
		EvidenceCount: len(evidence),
		ContentJSON:   types.JSON(raw),
		CreatedAt:     time.Now(),
	}
	if err := s.repo.CreateReport(ctx, tenantID, ownerID, report); err != nil {
		logger.Warnf(ctx, "insight stage2: save report failed: %v", err)
		s.failRun(ctx, tenantID, ownerID, runID, "报告保存失败")
		return
	}
	finished := time.Now()
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, runID, map[string]interface{}{
		"status":      types.InsightStatusDone,
		"stage":       types.InsightStageDone,
		"progress":    100,
		"message":     "报告已生成",
		"finished_at": finished,
	}); err != nil {
		logger.Warnf(ctx, "insight stage2: finalize run failed: %v", err)
	}
}

// runCancelled 在写报告前再确认一次，避免取消后仍然落库。
func (s *Service) runCancelled(ctx context.Context, tenantID uint64, ownerID, runID string) bool {
	run, err := s.repo.GetRun(ctx, tenantID, ownerID, runID)
	if err != nil {
		return true
	}
	return run.Status == types.InsightStatusCancelled
}

// failRun 把运行置为 failed，并记录对外可展示的简短原因。
func (s *Service) failRun(ctx context.Context, tenantID uint64, ownerID, runID, message string) {
	finished := time.Now()
	if err := s.repo.UpdateRunFields(ctx, tenantID, ownerID, runID, map[string]interface{}{
		"status":      types.InsightStatusFailed,
		"error":       message,
		"message":     message,
		"finished_at": finished,
	}); err != nil {
		logger.Warnf(ctx, "insight: mark run %s failed: %v", runID, err)
	}
}

// truncateRunes 按字符截断，避免把 UTF-8 截成半个汉字。
func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

// normalizeEvidenceType 只接受 evidence / inference，其他一律按推断处理。
func normalizeEvidenceType(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "evidence") {
		return "evidence"
	}
	return "inference"
}

// normalizeConfidence 只接受 高/中/低，缺省为“中”。
func normalizeConfidence(value string) string {
	switch strings.TrimSpace(value) {
	case "高", "中", "低":
		return strings.TrimSpace(value)
	default:
		return "中"
	}
}
