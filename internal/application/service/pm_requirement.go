package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 需求服务错误哨兵：handler 依赖 errors.Is 区分业务失败与系统错误
var (
	ErrIllegalRequirementTransition = errors.New("pm requirement: illegal status transition")
	ErrRequirementNotLaunched       = errors.New("pm requirement: launch verification requires launched status")
	ErrRequirementNotReactivable    = errors.New("pm requirement: only rejected/closed can be reactivated")
	ErrRequirementValueInvalid      = errors.New("pm requirement: invalid value achieved")
	ErrRequirementAnalysisState     = errors.New("pm requirement: analysis is not available for rejected/closed status")
	ErrRequirementAnalysisInvalid   = errors.New("pm requirement: ai analysis is not valid json")
	ErrRequirementTitleRequired     = errors.New("pm requirement: title is required")
	ErrRequirementFieldInvalid      = errors.New("pm requirement: invalid priority or source")
	ErrRequirementSessionUnready    = errors.New("pm requirement: session service not configured")
	ErrRequirementNoteTooLong       = errors.New("pm requirement: launch verification note exceeds length limit")

	ErrRequirementAttachmentEmpty    = errors.New("pm requirement: attachment is empty")
	ErrRequirementAttachmentTooLarge = errors.New("pm requirement: attachment exceeds size limit")
	ErrRequirementAttachmentType     = errors.New("pm requirement: unsupported attachment type")
	ErrRequirementAttachmentUnready  = errors.New("pm requirement: attachment repository not configured")
)

// pmRequirementAttachmentExts 允许上传的附件扩展名白名单（图片 + 常见文档）
var pmRequirementAttachmentExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {},
	".pdf": {},
	".doc": {}, ".docx": {},
	".xls": {}, ".xlsx": {},
	".ppt": {}, ".pptx": {},
	".txt": {}, ".md": {}, ".csv": {}, ".json": {},
}

// pmRequirementTextExts 可抽取纯文本作为 AI 上下文的扩展名
var pmRequirementTextExts = map[string]struct{}{
	".txt": {}, ".md": {}, ".csv": {}, ".json": {},
}

// pmRequirementService 需求业务：状态机唯一写入口 + AI 分析编排
type pmRequirementService struct {
	repo           interfaces.PMRequirementRepository
	sessionSvc     pmSessionService
	batchRepo      interfaces.PMPlanBatchRepository
	projectRepo    interfaces.PMProjectRepository
	attachmentRepo interfaces.PMRequirementAttachmentRepository
	materialRepo   interfaces.PMMaterialRepository
}

// NewPMRequirementService 依赖均可按场景裁剪；StartAnalysis 需要 sessionSvc，
// 路线图上下文需要 batchRepo，项目名需要 projectRepo，附件与资料上下文缺依赖时自动降级。
func NewPMRequirementService(
	repo interfaces.PMRequirementRepository,
	sessionSvc pmSessionService,
	batchRepo interfaces.PMPlanBatchRepository,
	projectRepo interfaces.PMProjectRepository,
	attachmentRepo interfaces.PMRequirementAttachmentRepository,
	materialRepo interfaces.PMMaterialRepository,
) interfaces.PMRequirementService {
	return &pmRequirementService{
		repo:           repo,
		sessionSvc:     sessionSvc,
		batchRepo:      batchRepo,
		projectRepo:    projectRepo,
		attachmentRepo: attachmentRepo,
		materialRepo:   materialRepo,
	}
}

// CreateRequirement 新建需求：状态固定从「待分析」起步，来源缺省为内部提出
func (s *pmRequirementService) CreateRequirement(
	ctx context.Context, tenantID uint64, r *types.PMRequirement,
) (*types.PMRequirement, error) {
	if strings.TrimSpace(r.Title) == "" {
		return nil, ErrRequirementTitleRequired
	}
	if r.Priority == "" {
		r.Priority = types.RequirementPriorityP2
	}
	if r.Source == "" {
		r.Source = types.RequirementSourceInternal
	}
	if !types.IsValidRequirementPriority(r.Priority) || !types.IsValidRequirementSource(r.Source) {
		return nil, ErrRequirementFieldInvalid
	}
	r.TenantID = tenantID
	r.Status = types.RequirementStatusPendingAnalysis
	if err := s.repo.Create(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *pmRequirementService) GetRequirement(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMRequirement, error) {
	return s.repo.GetByID(ctx, tenantID, id)
}

func (s *pmRequirementService) ListRequirements(
	ctx context.Context, tenantID uint64, projectID string, status string,
) ([]*types.PMRequirement, error) {
	return s.repo.List(ctx, tenantID, projectID, status)
}

// UpdateRequirement 仅更新描述性字段；状态一律保留库中原值，
// 确保 UpdateStatus 是唯一的状态写入口（版本联动也走它）。
func (s *pmRequirementService) UpdateRequirement(
	ctx context.Context, tenantID uint64, r *types.PMRequirement,
) error {
	cur, err := s.repo.GetByID(ctx, tenantID, r.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Title) == "" {
		return ErrRequirementTitleRequired
	}
	if r.Priority != "" && !types.IsValidRequirementPriority(r.Priority) {
		return ErrRequirementFieldInvalid
	}
	if r.Source != "" && !types.IsValidRequirementSource(r.Source) {
		return ErrRequirementFieldInvalid
	}
	r.TenantID = tenantID
	r.Status = cur.Status
	r.CreatedBy = cur.CreatedBy
	return s.repo.Update(ctx, r)
}

// UpdateStatus 唯一的状态写入入口，内部统一过 CanTransitRequirement
func (s *pmRequirementService) UpdateStatus(
	ctx context.Context, tenantID uint64, id string, to string,
) error {
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return s.transit(ctx, r, to)
}

// transit 状态迁移的唯一实现：非法迁移不落库
func (s *pmRequirementService) transit(
	ctx context.Context, r *types.PMRequirement, to string,
) error {
	if r.Status == to {
		return nil
	}
	if !types.CanTransitRequirement(r.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalRequirementTransition, r.Status, to)
	}
	r.Status = to
	return s.repo.Update(ctx, r)
}

// Reactivate 重新激活：仅终止态可调用，重置为待分析并清空 AI 结论与会话
func (s *pmRequirementService) Reactivate(
	ctx context.Context, tenantID uint64, id string,
) error {
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if r.Status != types.RequirementStatusRejected && r.Status != types.RequirementStatusClosed {
		return fmt.Errorf("%w: %s", ErrRequirementNotReactivable, r.Status)
	}
	r.Status = types.RequirementStatusPendingAnalysis
	r.AIAnalysis = nil
	r.DesignPlan = ""
	r.SessionID = ""
	return s.repo.Update(ctx, r)
}

// DeleteRequirement 删除需求：先确认归属与存在性，再由仓储事务级联清理
func (s *pmRequirementService) DeleteRequirement(
	ctx context.Context, tenantID uint64, id string,
) error {
	if _, err := s.repo.GetByID(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, id)
}

// StartAnalysis 触发 AI 引导分析（辅助能力，不改变需求状态）：
// 1) 建会话 2) 拼上下文（项目路线图最新快照 + 需求初稿）
// 3) 返回 session_id 供前端 SSE 订阅；分析结果由 SaveAnalysis 回写。
func (s *pmRequirementService) StartAnalysis(
	ctx context.Context, tenantID uint64, id string,
) (string, error) {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	// 终止态不再提供分析入口，避免对已归档需求继续产出结论
	if r.Status == types.RequirementStatusRejected || r.Status == types.RequirementStatusClosed {
		return "", fmt.Errorf("%w: %s", ErrRequirementAnalysisState, r.Status)
	}
	if s.sessionSvc == nil {
		return "", ErrRequirementSessionUnready
	}

	prompt := s.buildAnalysisPrompt(ctx, tenantID, r)
	session, err := s.sessionSvc.CreateSession(ctx, &types.Session{
		Title: r.Title + " - 需求分析",
		// 前缀标记用于首页会话列表隐藏该 PM 分析会话，其后仍承载分析上下文
		Description: types.PMAnalysisSessionMarker + prompt,
		TenantID:    tenantID,
	})
	if err != nil {
		return "", err
	}

	r.SessionID = session.ID
	if err := s.repo.Update(ctx, r); err != nil {
		return "", err
	}
	return session.ID, nil
}

// SaveAnalysis 对话结束后回写结构化分析与具体设计方案（任意非终止态均可回写）
func (s *pmRequirementService) SaveAnalysis(
	ctx context.Context, tenantID uint64, id string,
	aiAnalysis types.JSON, designPlan string,
) error {
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if len(aiAnalysis) > 0 && !json.Valid(aiAnalysis) {
		return ErrRequirementAnalysisInvalid
	}
	r.AIAnalysis = aiAnalysis
	r.DesignPlan = designPlan
	return s.repo.Update(ctx, r)
}

// VerifyLaunch 上线验证回填：仅 launched 可执行，写入效果数据、价值达成与复盘说明。
// 重复回填视为修正，覆盖旧值并刷新 value_achieved_at。
func (s *pmRequirementService) VerifyLaunch(
	ctx context.Context, tenantID uint64, id string, in types.LaunchVerificationInput,
) (*types.PMRequirement, error) {
	in.Normalize()
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if r.Status != types.RequirementStatusLaunched {
		return nil, fmt.Errorf("%w: %s", ErrRequirementNotLaunched, r.Status)
	}
	switch in.ValueAchieved {
	case types.RequirementValueAchieved, types.RequirementValuePartial, types.RequirementValueNotAchieved:
	default:
		return nil, fmt.Errorf("%w: %s", ErrRequirementValueInvalid, in.ValueAchieved)
	}
	if len([]rune(in.EffectNotes)) > types.LaunchVerificationNoteMaxLen ||
		len([]rune(in.RetroNotes)) > types.LaunchVerificationNoteMaxLen {
		return nil, ErrRequirementNoteTooLong
	}
	now := time.Now()
	r.ValueAchieved = in.ValueAchieved
	r.EffectNotes = in.EffectNotes
	r.RetroNotes = in.RetroNotes
	r.ValueAchievedAt = &now
	if err := s.repo.Update(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// buildAnalysisPrompt 拼装分析上下文：需求初稿 + 需求附件 + 项目路线图 + 项目资料
func (s *pmRequirementService) buildAnalysisPrompt(
	ctx context.Context, tenantID uint64, r *types.PMRequirement,
) string {
	var sb strings.Builder
	sb.WriteString("你是 PM 工作站的需求分析助手（pm-requirement-analysis）。")
	sb.WriteString("你的职责是辅助 PM 澄清需求，而不是替 PM 做决定：")
	sb.WriteString("先基于上下文连续追问（每轮最多 3 个问题，问题要具体，并附上你的推测答案），")
	sb.WriteString("逐步补齐需求背景、目标用户、使用场景、成功标准与边界；")
	sb.WriteString("在用户要求给出方案或信息足够时，再输出结构化分析结论。\n\n")

	if s.projectRepo != nil {
		if p, err := s.projectRepo.GetByID(ctx, tenantID, r.ProjectID); err == nil && p != nil {
			sb.WriteString("## 所属项目\n\n")
			sb.WriteString(p.Name + "\n\n")
		}
	}

	sb.WriteString("## 需求初稿\n\n")
	writePromptKV(&sb, "标题", r.Title)
	writePromptKV(&sb, "描述", r.Description)
	writePromptKV(&sb, "优先级", r.Priority)
	writePromptKV(&sb, "来源", r.Source)
	writePromptKV(&sb, "提出人", r.Proposer)

	s.writeAttachmentPrompt(ctx, tenantID, &sb, r.ID)

	sb.WriteString("\n## 项目功能规划与功能清单（最新生效快照）\n\n")
	sb.WriteString(s.roadmapPromptContext(ctx, tenantID, r.ProjectID))

	sb.WriteString("\n## 项目资料\n\n")
	sb.WriteString(s.materialPromptContext(ctx, tenantID, r.ProjectID))

	sb.WriteString("\n## 输出要求\n\n")
	sb.WriteString("1. 追问阶段只提问，不要急于下结论；每轮问题附带你的推荐答案。\n")
	sb.WriteString("2. 输出方案时严格按以下字段标题分段：\n")
	sb.WriteString("- 需求价值：…\n")
	sb.WriteString("- 关联影响：…\n")
	sb.WriteString("- 技术风险：…\n")
	sb.WriteString("- 实现方案：…\n")
	return sb.String()
}

// materialPromptContext 注入已发布且未归档的项目资料最新版本正文；
// 同名同类型只取更新时间最新一条，单条与总量均截断，避免 prompt 过长。
func (s *pmRequirementService) materialPromptContext(
	ctx context.Context, tenantID uint64, projectID string,
) string {
	const (
		empty        = "（暂无项目资料）"
		maxTotalRune = 8000
		maxPerRune   = 2000
	)
	if s.materialRepo == nil {
		return empty
	}
	list, err := s.materialRepo.List(ctx, tenantID, projectID, "")
	if err != nil || len(list) == 0 {
		return empty
	}

	latest := make(map[string]*types.PMMaterial, len(list))
	for _, m := range list {
		if m == nil || m.Archived || m.Status != types.MaterialStatusPublished {
			continue
		}
		key := m.MaterialType + "|" + m.Title
		if cur, ok := latest[key]; !ok || m.UpdatedAt.After(cur.UpdatedAt) {
			latest[key] = m
		}
	}
	// map 遍历无序，按 key 排序保证同一需求每次分析上下文稳定
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var sb strings.Builder
	total := 0
	for _, key := range keys {
		content := strings.TrimSpace(latest[key].Content)
		if content == "" {
			continue
		}
		block := "### " + latest[key].Title + "\n\n" + clipPromptRunes(content, maxPerRune) + "\n\n"
		used := len([]rune(block))
		if total+used > maxTotalRune {
			sb.WriteString("（项目资料较多，以下内容已截断）\n\n")
			break
		}
		sb.WriteString(block)
		total += used
	}
	if sb.Len() == 0 {
		return empty
	}
	return sb.String()
}

// clipPromptRunes 按字符截断长文本，避免按字节切断中文
func clipPromptRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// roadmapPromptContext 从生效批次的草稿渲染路线图摘要；缺依赖或未生效时降级为空提示
func (s *pmRequirementService) roadmapPromptContext(
	ctx context.Context, tenantID uint64, projectID string,
) string {
	const empty = "（暂无路线图快照）"
	if s.batchRepo == nil {
		return empty
	}
	b, err := s.batchRepo.GetActiveByProject(ctx, tenantID, projectID)
	if err != nil || b == nil {
		return empty
	}

	var sb strings.Builder
	if b.SnapshotKnowledgeID != "" {
		sb.WriteString("路线图快照知识条目：" + b.SnapshotKnowledgeID + "\n\n")
	}
	draft, err := parseRoadmapDraft(b.Draft)
	if err != nil {
		sb.WriteString("（快照内容暂不可用）")
		return sb.String()
	}
	if c := draft.Cognition; c != nil {
		writePromptKV(&sb, "目标用户", c.TargetUsers)
		writePromptKV(&sb, "核心价值", c.CoreValue)
	}
	if len(draft.Scope) > 0 {
		sb.WriteString("首期范围：" + strings.Join(draft.Scope, "；") + "\n")
	}
	if len(draft.Features) > 0 {
		sb.WriteString("\n功能清单（模块 / 功能点 / 操作项）：\n")
		for _, f := range draft.Features {
			sb.WriteString("- " + f.ModuleName + " / " + f.FeatureName + " / " + f.OperationName + "\n")
		}
	}
	if sb.Len() == 0 {
		return empty
	}
	return sb.String()
}

// writePromptKV 跳过空值，避免 prompt 出现无意义占位
func writePromptKV(sb *strings.Builder, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	sb.WriteString("- " + key + "：" + value + "\n")
}

// AddAttachment 上传需求附件：校验大小与扩展名，文本类附件额外抽取正文供 AI 分析使用
func (s *pmRequirementService) AddAttachment(
	ctx context.Context, tenantID uint64, userID string, requirementID string,
	fileName string, contentType string, data []byte,
) (*types.PMRequirementAttachment, error) {
	if s.attachmentRepo == nil {
		return nil, ErrRequirementAttachmentUnready
	}
	if _, err := s.repo.GetByID(ctx, tenantID, requirementID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(path.Base(fileName))
	if name == "" || len(data) == 0 {
		return nil, ErrRequirementAttachmentEmpty
	}
	if len(data) > types.PMRequirementAttachmentMaxSize {
		return nil, ErrRequirementAttachmentTooLarge
	}
	ext := strings.ToLower(path.Ext(name))
	if _, ok := pmRequirementAttachmentExts[ext]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrRequirementAttachmentType, ext)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = mime.TypeByExtension(ext)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	text := ""
	if _, ok := pmRequirementTextExts[ext]; ok {
		text = truncateAttachmentText(string(data), types.PMRequirementAttachmentTextMaxLen)
	}

	a := &types.PMRequirementAttachment{
		TenantID:      tenantID,
		RequirementID: requirementID,
		FileName:      name,
		ContentType:   contentType,
		Size:          int64(len(data)),
		TextContent:   text,
		Data:          data,
		CreatedBy:     userID,
	}
	if err := s.attachmentRepo.Create(ctx, a); err != nil {
		return nil, err
	}
	// 响应不回传文件正文
	a.Data = nil
	return a, nil
}

func (s *pmRequirementService) ListAttachments(
	ctx context.Context, tenantID uint64, requirementID string,
) ([]*types.PMRequirementAttachment, error) {
	if s.attachmentRepo == nil {
		return nil, ErrRequirementAttachmentUnready
	}
	if _, err := s.repo.GetByID(ctx, tenantID, requirementID); err != nil {
		return nil, err
	}
	return s.attachmentRepo.ListByRequirement(ctx, tenantID, requirementID)
}

func (s *pmRequirementService) DeleteAttachment(
	ctx context.Context, tenantID uint64, requirementID string, attachmentID string,
) error {
	if s.attachmentRepo == nil {
		return ErrRequirementAttachmentUnready
	}
	a, err := s.attachmentRepo.GetByID(ctx, tenantID, attachmentID)
	if err != nil {
		return err
	}
	if a.RequirementID != requirementID {
		return apprepo.ErrPMRequirementAttachmentNotFound
	}
	return s.attachmentRepo.Delete(ctx, tenantID, attachmentID)
}

// GetAttachmentFile 读取附件正文用于下载/预览，校验附件确实属于该需求
func (s *pmRequirementService) GetAttachmentFile(
	ctx context.Context, tenantID uint64, requirementID string, attachmentID string,
) (*types.PMRequirementAttachment, error) {
	if s.attachmentRepo == nil {
		return nil, ErrRequirementAttachmentUnready
	}
	a, err := s.attachmentRepo.GetByID(ctx, tenantID, attachmentID)
	if err != nil {
		return nil, err
	}
	if a.RequirementID != requirementID {
		return nil, apprepo.ErrPMRequirementAttachmentNotFound
	}
	return a, nil
}

// writeAttachmentPrompt 附件清单与文本摘要写入分析上下文；无附件时整段省略
func (s *pmRequirementService) writeAttachmentPrompt(
	ctx context.Context, tenantID uint64, sb *strings.Builder, requirementID string,
) {
	if s.attachmentRepo == nil {
		return
	}
	list, err := s.attachmentRepo.ListByRequirement(ctx, tenantID, requirementID)
	if err != nil || len(list) == 0 {
		return
	}
	sb.WriteString("\n## 需求附件\n\n")
	for _, a := range list {
		sb.WriteString(fmt.Sprintf("- %s（%s，%d KB）\n", a.FileName, a.ContentType, (a.Size+1023)/1024))
		if text := strings.TrimSpace(a.TextContent); text != "" {
			sb.WriteString("\n附件正文（" + a.FileName + "）：\n\n```\n" + text + "\n```\n\n")
		}
	}
}

// truncateAttachmentText 按字符数截断，避免中文被切坏
func truncateAttachmentText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n…（内容已截断）"
}
