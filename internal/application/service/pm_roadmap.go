package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrRoadmapExistingNotEmpty = errors.New("pm roadmap: existing roadmap is not empty")
	ErrRoadmapDraftEmpty       = errors.New("pm roadmap: draft is empty")
	ErrRoadmapDraftInvalid     = errors.New("pm roadmap: draft is invalid")
	ErrRoadmapDraftDuplicate   = errors.New("pm roadmap: duplicate operation in draft")
	ErrRoadmapPlanTypeUnknown  = errors.New("pm roadmap: unknown plan type")
	ErrRoadmapBatchNotEditable = errors.New("pm roadmap: batch is not editable")
	ErrRoadmapNotGenerated     = errors.New("pm roadmap: roadmap not generated yet")
	ErrRoadmapSessionUnready   = errors.New("pm roadmap: session service not configured")
)

// RoadmapMergeResult 合并结果：Kept 为保留的既有项，Added 为新增项，Removed 仅重新规划产生
type RoadmapMergeResult struct {
	Kept    []types.PMFeature
	Added   []types.PMFeature
	Removed []types.PMFeature
}

// MergeRoadmap 按规划类型合并草稿与既有路线图（纯函数，便于单测与快照回溯）：
//   - 首期规划：既有必须为空，避免误覆盖；
//   - 增量规划：保留全部既有项，仅追加草稿中的新项；
//   - 重新规划：以草稿为准，未出现在草稿中的既有项标记移除（不物理删除）。
func MergeRoadmap(
	existing, draft []types.PMFeature, planType string,
) (*RoadmapMergeResult, error) {
	switch planType {
	case types.PlanTypeInitial:
		if len(existing) > 0 {
			return nil, ErrRoadmapExistingNotEmpty
		}
	case types.PlanTypeIteration, types.PlanTypeReplan:
	default:
		return nil, fmt.Errorf("%w: %s", ErrRoadmapPlanTypeUnknown, planType)
	}

	// 三级路径是路线图的自然键
	existingByKey := make(map[string]types.PMFeature, len(existing))
	for _, f := range existing {
		existingByKey[roadmapKey(f)] = f
	}

	result := &RoadmapMergeResult{}
	if planType != types.PlanTypeReplan {
		result.Kept = append(result.Kept, existing...)
	}

	matched := make(map[string]bool, len(draft))
	for _, f := range draft {
		key := roadmapKey(f)
		matched[key] = true
		if old, ok := existingByKey[key]; ok {
			// 重新规划只保留草稿中仍存在的既有项；增量规划已在 Kept 中预置
			if planType == types.PlanTypeReplan {
				result.Kept = append(result.Kept, old)
			}
			continue
		}
		result.Added = append(result.Added, f)
	}

	if planType == types.PlanTypeReplan {
		for _, f := range existing {
			if !matched[roadmapKey(f)] {
				result.Removed = append(result.Removed, f)
			}
		}
	}
	return result, nil
}

// ValidateRoadmapDraft 校验三级结构必填且同一操作不重复
func ValidateRoadmapDraft(features []types.PMFeature) error {
	if len(features) == 0 {
		return ErrRoadmapDraftEmpty
	}
	seen := make(map[string]bool, len(features))
	for i, f := range features {
		m, fn, op := strings.TrimSpace(f.ModuleName),
			strings.TrimSpace(f.FeatureName), strings.TrimSpace(f.OperationName)
		if m == "" || fn == "" || op == "" {
			return fmt.Errorf("%w: 第 %d 项缺少模块/功能点/操作项", ErrRoadmapDraftInvalid, i+1)
		}
		key := roadmapKey(f)
		if seen[key] {
			return fmt.Errorf("%w: %s/%s/%s", ErrRoadmapDraftDuplicate, m, fn, op)
		}
		seen[key] = true
	}
	return nil
}

// roadmapKey 以三级名称生成自然键，首尾空白不敏感
func roadmapKey(f types.PMFeature) string {
	return strings.Join([]string{
		strings.TrimSpace(f.ModuleName),
		strings.TrimSpace(f.FeatureName),
		strings.TrimSpace(f.OperationName),
	}, "\x1f")
}

// pmRoadmapKnowledge 路线图落库只需要「同步写入知识库」一个能力
type pmRoadmapKnowledge interface {
	CreateKnowledgeFromPassageSync(
		ctx context.Context, kbID string, passage []string, channel string,
	) (*types.Knowledge, error)
}

// PMRoadmapDeps 规划服务依赖，按可选性聚合，测试可按需注入 fake
type PMRoadmapDeps struct {
	ProjectRepo interfaces.PMProjectRepository
	BatchRepo   interfaces.PMPlanBatchRepository
	TaskRepo    interfaces.PMTaskRepository
	Knowledge   pmRoadmapKnowledge
	FeatureSvc  interfaces.PMFeatureService
	KBDir       interfaces.PMKBDirService
	// SessionSvc 用于创建隐藏的 AI 引导会话；为空时引导接口不可用
	SessionSvc pmSessionService
}

type pmRoadmapService struct {
	deps PMRoadmapDeps
}

// NewPMRoadmapService 规划服务：草稿 → 生成合并 → 确认生效
func NewPMRoadmapService(deps PMRoadmapDeps) interfaces.PMRoadmapService {
	return &pmRoadmapService{deps: deps}
}

func (s *pmRoadmapService) ListBatches(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMPlanBatch, error) {
	return s.deps.BatchRepo.ListByProject(ctx, tenantID, projectID)
}

func (s *pmRoadmapService) GetBatch(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMPlanBatch, error) {
	return s.deps.BatchRepo.GetByID(ctx, tenantID, id)
}

// CreateBatch 新建批次，初始落 draft，等待引导草稿推进
func (s *pmRoadmapService) CreateBatch(
	ctx context.Context, tenantID uint64, projectID string, batchName string, planType string,
) (*types.PMPlanBatch, error) {
	if strings.TrimSpace(batchName) == "" {
		return nil, fmt.Errorf("%w: 批次名称不能为空", ErrRoadmapDraftInvalid)
	}
	switch planType {
	case types.PlanTypeInitial, types.PlanTypeIteration, types.PlanTypeReplan:
	default:
		return nil, fmt.Errorf("%w: %s", ErrRoadmapPlanTypeUnknown, planType)
	}

	b := &types.PMPlanBatch{
		TenantID:    tenantID,
		ProjectID:   projectID,
		BatchName:   strings.TrimSpace(batchName),
		PlanType:    planType,
		Status:      types.PlanBatchStatusDraft,
		CurrentStep: 1,
	}
	if err := s.deps.BatchRepo.Create(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// SaveDraft 整体覆盖草稿，current_step 单调不减（用户回看前步时进度不倒退）
func (s *pmRoadmapService) SaveDraft(
	ctx context.Context, tenantID uint64, batchID string, draft types.JSON, step int,
) error {
	if len(draft) == 0 || !json.Valid(draft) {
		return ErrRoadmapDraftInvalid
	}
	// 结构校验前移到保存阶段：脏草稿当场暴露，而不是等到「完成规划」才整批失败
	if _, err := parseRoadmapDraft(draft); err != nil {
		return err
	}
	b, err := s.deps.BatchRepo.GetByID(ctx, tenantID, batchID)
	if err != nil {
		return err
	}
	if b.Status != types.PlanBatchStatusDraft {
		return fmt.Errorf("%w: 状态 %s", ErrRoadmapBatchNotEditable, b.Status)
	}
	if step > b.CurrentStep {
		b.CurrentStep = step
	}
	b.Draft = draft
	return s.deps.BatchRepo.Update(ctx, b)
}

// GenerateRoadmap 同步生成路线图：
// 读取草稿 → 校验 → 与生效批次合并 → 渲染 Markdown → 落知识库与快照 → 可选同步功能清单。
// 返回的 PMTask 记录本次生成结果，供统一轮询接口查询。
func (s *pmRoadmapService) GenerateRoadmap(
	ctx context.Context, tenantID uint64, batchID string, syncFeatures bool,
) (*types.PMTask, error) {
	b, err := s.deps.BatchRepo.GetByID(ctx, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	if b.Status == types.PlanBatchStatusArchived {
		return nil, fmt.Errorf("%w: 状态 %s", ErrRoadmapBatchNotEditable, b.Status)
	}
	if s.deps.Knowledge == nil {
		return nil, errors.New("pm roadmap: knowledge service is not configured")
	}

	draft, err := parseRoadmapDraft(b.Draft)
	if err != nil {
		return nil, err
	}
	if err := ValidateRoadmapDraft(draft.Features); err != nil {
		return nil, err
	}

	existing, err := s.existingFeatures(ctx, tenantID, b)
	if err != nil {
		return nil, err
	}
	merged, err := MergeRoadmap(existing, draft.Features, b.PlanType)
	if err != nil {
		return nil, err
	}
	final := append(append([]types.PMFeature{}, merged.Kept...), merged.Added...)

	project, err := s.deps.ProjectRepo.GetByID(ctx, tenantID, b.ProjectID)
	if err != nil {
		return nil, err
	}

	content := renderRoadmapMarkdown(project, draft, final)
	// 主文档写入 需求/，批次快照写入 需求/规划历史/，两者内容一致、生命周期不同
	roadmapKnowledge, err := s.deps.Knowledge.CreateKnowledgeFromPassageSync(
		ctx, project.KBID, []string{content}, "pm-roadmap")
	if err != nil {
		return nil, err
	}
	snapshotKnowledge, err := s.deps.Knowledge.CreateKnowledgeFromPassageSync(
		ctx, project.KBID, []string{content}, "pm-roadmap-snapshot")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	b.RoadmapKnowledgeID = roadmapKnowledge.ID
	b.SnapshotKnowledgeID = snapshotKnowledge.ID
	b.GeneratedAt = &now
	b.SnapshotAt = &now
	if err := s.deps.BatchRepo.Update(ctx, b); err != nil {
		return nil, err
	}

	// 功能清单是可选投影：失败不回滚已生成的路线图，只记录告警
	synced := false
	if syncFeatures && s.deps.FeatureSvc != nil {
		if err := s.deps.FeatureSvc.SyncFromRoadmap(
			ctx, tenantID, b.ProjectID, final); err != nil {
			logger.Warnf(ctx, "PM 路线图同步功能清单失败: batch=%s err=%v", b.ID, err)
		} else {
			synced = true
		}
	}

	finished := time.Now()
	result, err := json.Marshal(map[string]any{
		"roadmap_knowledge_id":  roadmapKnowledge.ID,
		"snapshot_knowledge_id": snapshotKnowledge.ID,
		"feature_sync":          synced,
	})
	if err != nil {
		return nil, err
	}
	task := &types.PMTask{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		ProjectID:  b.ProjectID,
		TaskType:   types.TypePMRoadmapGenerate,
		Status:     types.PMTaskStatusSucceeded,
		Progress:   100,
		Result:     types.JSON(result),
		CreatedBy:  b.OperatorID,
		FinishedAt: &finished,
	}
	if s.deps.TaskRepo != nil {
		if err := s.deps.TaskRepo.Create(ctx, task); err != nil {
			return nil, err
		}
	}
	return task, nil
}

// ActivateBatch 确认生效：归档旧 active → 置本人 active → 写快照指针（幂等）
func (s *pmRoadmapService) ActivateBatch(
	ctx context.Context, tenantID uint64, batchID string, operatorID string,
) error {
	b, err := s.deps.BatchRepo.GetByID(ctx, tenantID, batchID)
	if err != nil {
		return err
	}
	if b.Status == types.PlanBatchStatusActive {
		return nil
	}
	if b.Status == types.PlanBatchStatusArchived {
		return fmt.Errorf("%w: 状态 %s", ErrRoadmapBatchNotEditable, b.Status)
	}

	// 先落快照指针再切状态：中途失败时批次仍是 draft，不会出现无快照的生效批次
	snapshotID := b.SnapshotKnowledgeID
	if snapshotID == "" {
		snapshotID = b.RoadmapKnowledgeID
	}
	if snapshotID == "" {
		return ErrRoadmapNotGenerated
	}
	now := time.Now()
	b.SnapshotKnowledgeID = snapshotID
	b.SnapshotAt = &now
	b.OperatorID = operatorID
	if err := s.deps.BatchRepo.Update(ctx, b); err != nil {
		return err
	}
	return s.deps.BatchRepo.ActivateBatch(ctx, tenantID, b.ProjectID, batchID)
}

// StartGuideSession 创建 AI 引导会话：会话描述带保留前缀，从而不出现在首页对话列表，
// 同时返回首轮指令，前端把它与用户输入一起发给所选智能体。
func (s *pmRoadmapService) StartGuideSession(
	ctx context.Context, tenantID uint64, batchID string,
) (string, string, error) {
	if s.deps.SessionSvc == nil {
		return "", "", ErrRoadmapSessionUnready
	}
	b, err := s.deps.BatchRepo.GetByID(ctx, tenantID, batchID)
	if err != nil {
		return "", "", err
	}
	if b.Status != types.PlanBatchStatusDraft {
		return "", "", fmt.Errorf("%w: 状态 %s", ErrRoadmapBatchNotEditable, b.Status)
	}
	project, err := s.deps.ProjectRepo.GetByID(ctx, tenantID, b.ProjectID)
	if err != nil {
		return "", "", err
	}

	prompt := roadmapGuideInstructions + "\n\n" + buildRoadmapGuideContext(project, b)
	session, err := s.deps.SessionSvc.CreateSession(ctx, &types.Session{
		Title:       project.Name + " - 功能规划引导",
		Description: types.PMRoadmapSessionMarker + prompt,
		TenantID:    tenantID,
	})
	if err != nil {
		return "", "", err
	}
	return session.ID, prompt, nil
}

// roadmapGuideInstructions 功能规划引导的技能约束。cli/skills/pm-roadmap-planner 属于
// 独立 Go module，主模块无法直接引用其 SKILL.md，故在此保留核心约束副本。
const roadmapGuideInstructions = `你是产品经理的「功能规划」协作助手，全程对话引导用户完成产品路线图。

## 产出物
你会随对话逐步整理四类产出物，用户可在右侧预览页签中实时查看：
1. 产品认知：目标用户、核心价值、关键场景、规划背景
2. 功能清单：按「子系统 / 一级模块 / 二级模块 / 功能说明」四列拆解，遵循 feature-list 技能规范
3. 核心流程图：为关键一级模块给出 Mermaid flowchart
4. 首期范围：从功能清单中勾选本次上线范围
最后在「整体规划」中汇总确认，生成路线图文档。

## 交互要求
- 没有固定步骤，由你判断当前最缺哪类产出物，直接推进；用户随时可以补充任意部分
- 开始拆解功能清单前，先确认本次规划覆盖哪些子系统（如 PC端、移动端、运营端、管理后台、小程序端），必须由用户当次指定，不要自行假定
- 先给结论，再给理由；需要补充信息时一次最多问 3 个问题，并附上你的推测答案
- 使用简体中文纯文本回答，可用短标题与列表，不要输出整段长篇

## 结构化输出（必须遵守，前端依赖以下格式自动归档）
- 产品认知用字段行输出，每行一个：
  目标用户：…
  核心价值：…
  关键场景：…
  规划背景：…
- 子系统用单独一行输出（多个用顿号分隔）：
  子系统：PC端
- 功能清单用 Markdown 表格输出，表头固定为四列：
  | 子系统 | 一级模块 | 二级模块 | 功能说明 |
  | --- | --- | --- | --- |
  填写规则：
  - 每行四列都要填写完整，不使用空白单元格或合并单元格
  - 同一「子系统 + 一级模块」的每一行都重复填写这两列的值
  - 不同一级模块之间插入一行分隔：| --- | --- | --- | --- |
  - 二级模块是该一级模块下的具体功能项；功能说明写清功能内容，并原样保留（核心）（复用）（xx可见）等关键标记
  - 一级模块对应草稿的「模块」、二级模块对应「功能点」、功能说明对应「操作项」
- 核心流程用 Mermaid 代码块输出，并在代码块上方一行写一级模块名作为标题：
  ### 一级模块名
  随后紧跟一个以 mermaid 为语言标记的代码块，首行必须是 flowchart TD 或 LR
- 每次回复只输出本轮新增或修正的内容，不要重复已确认的产出物

## 输出前自校验（功能清单必须通过后再输出）
- 模块归属：每个二级模块确实属于其一级模块
- 拆解粒度：不过粗（一条覆盖整个模块），不过细（拆到字段级）
- 关键标记：需求沟通中确认的（核心）（复用）（xx可见）等标记无遗漏
- 独立可理解：不出现「同上」「参考上一条」等依赖上下文的描述
- 同级去重：同一一级模块下无重复或高度重叠的功能项
- 覆盖度：讨论过的功能点与异常/边界功能均已入表`

// buildRoadmapGuideContext 生成引导上下文：项目信息 + 批次信息 + 已填草稿快照
func buildRoadmapGuideContext(project *types.PMProject, b *types.PMPlanBatch) string {
	var sb strings.Builder
	sb.WriteString("## 当前项目\n")
	sb.WriteString("- 名称：" + strings.TrimSpace(project.Name) + "\n")
	if d := strings.TrimSpace(project.Description); d != "" {
		sb.WriteString("- 描述：" + d + "\n")
	}

	sb.WriteString("\n## 本次规划\n")
	sb.WriteString("- 批次：" + b.BatchName + "\n")
	sb.WriteString("- 类型：" + roadmapPlanTypeLabel(b.PlanType) + "\n")

	sb.WriteString("\n## 已填草稿\n")
	draft, err := parseRoadmapDraft(b.Draft)
	if err != nil || draft == nil {
		sb.WriteString("- 暂无\n")
		return sb.String()
	}
	if c := draft.Cognition; c != nil {
		writeKV(&sb, "目标用户", c.TargetUsers)
		writeKV(&sb, "核心价值", c.CoreValue)
		writeKV(&sb, "主要场景", c.Scenarios)
		writeKV(&sb, "规划背景", c.Background)
	}
	if len(draft.Features) == 0 {
		sb.WriteString("- 功能清单：暂无\n")
	} else {
		if subsystem := strings.TrimSpace(draft.Subsystem); subsystem != "" {
			sb.WriteString("- 子系统：" + subsystem + "\n")
		}
		sb.WriteString(fmt.Sprintf("- 功能清单（共 %d 项，下列为前 40 项）：\n", len(draft.Features)))
		for i, f := range draft.Features {
			if i >= 40 {
				break
			}
			subsystem := strings.TrimSpace(f.Subsystem)
			line := fmt.Sprintf("%s / %s / %s",
				strings.TrimSpace(f.ModuleName),
				strings.TrimSpace(f.FeatureName),
				strings.TrimSpace(f.OperationName))
			if subsystem != "" {
				line = subsystem + " / " + line
			}
			sb.WriteString("  - " + line + "\n")
		}
	}
	if len(draft.Flows) > 0 {
		names := make([]string, 0, len(draft.Flows))
		for _, flow := range draft.Flows {
			if name := strings.TrimSpace(flow.Module); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			sb.WriteString("- 已画流程模块：" + strings.Join(names, "、") + "\n")
		}
	}
	if len(draft.Scope) > 0 {
		sb.WriteString(fmt.Sprintf("- 首期范围：已选 %d 项\n", len(draft.Scope)))
	}
	return sb.String()
}

// roadmapPlanTypeLabel 规划类型的可读名称（代码用 iteration，README/SKILL 里叫 incremental）
func roadmapPlanTypeLabel(planType string) string {
	switch planType {
	case types.PlanTypeInitial:
		return "首期规划"
	case types.PlanTypeIteration:
		return "增量规划"
	case types.PlanTypeReplan:
		return "重新规划"
	default:
		return planType
	}
}

// DeleteBatch 仅草稿批次可删：已生效/已归档批次保留用于回溯
func (s *pmRoadmapService) DeleteBatch(ctx context.Context, tenantID uint64, batchID string) error {
	b, err := s.deps.BatchRepo.GetByID(ctx, tenantID, batchID)
	if err != nil {
		return err
	}
	if b.Status != types.PlanBatchStatusDraft {
		return fmt.Errorf("%w: 状态 %s", ErrRoadmapBatchNotEditable, b.Status)
	}
	return s.deps.BatchRepo.Delete(ctx, tenantID, batchID)
}

// existingFeatures 取当前生效批次的草稿内容作为合并基线；同批次重生成时不与自身比较
func (s *pmRoadmapService) existingFeatures(
	ctx context.Context, tenantID uint64, b *types.PMPlanBatch,
) ([]types.PMFeature, error) {
	active, err := s.deps.BatchRepo.GetActiveByProject(ctx, tenantID, b.ProjectID)
	if errors.Is(err, repository.ErrPMPlanBatchNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if active.ID == b.ID || len(active.Draft) == 0 {
		return nil, nil
	}
	draft, err := parseRoadmapDraft(active.Draft)
	if err != nil {
		return nil, err
	}
	return draft.Features, nil
}

// pmRoadmapDraftFeature 草稿功能项：只声明业务字段。
// 前端草稿尚未落库，会把 created_at/updated_at 写成空字符串；若直接反序列化到
// types.PMFeature（time.Time）会报「parsing time "" as ...」而整批失败。
// 生成阶段的时间由仓储重新赋值，因此草稿侧丢弃时间字段即可。
type pmRoadmapDraftFeature struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	RequirementID string `json:"requirement_id"`
	ParentID      string `json:"parent_id"`
	Subsystem     string `json:"subsystem"`
	ModuleName    string `json:"module_name"`
	FeatureName   string `json:"feature_name"`
	OperationName string `json:"operation_name"`
	Source        string `json:"source"`
	Status        string `json:"status"`
}

func (f pmRoadmapDraftFeature) toFeature() types.PMFeature {
	return types.PMFeature{
		ID:            f.ID,
		ProjectID:     f.ProjectID,
		RequirementID: f.RequirementID,
		ParentID:      f.ParentID,
		Subsystem:     f.Subsystem,
		ModuleName:    f.ModuleName,
		FeatureName:   f.FeatureName,
		OperationName: f.OperationName,
		Source:        f.Source,
		Status:        f.Status,
	}
}

// pmRoadmapDraftDTO 草稿的宽容形态：未知字段（含前端的时间字符串）直接忽略
type pmRoadmapDraftDTO struct {
	Cognition *types.PMRoadmapCognition `json:"cognition"`
	// Subsystem 批次级子系统：行级未填写时统一回填，兼容前端只填一处的情况
	Subsystem string                  `json:"subsystem"`
	Features  []pmRoadmapDraftFeature `json:"features"`
	Flows     []types.PMRoadmapFlow   `json:"flows"`
	Scope     []string                `json:"scope"`
}

func parseRoadmapDraft(raw types.JSON) (*types.PMRoadmapDraft, error) {
	if len(raw) == 0 {
		return nil, ErrRoadmapDraftEmpty
	}
	var dto pmRoadmapDraftDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRoadmapDraftInvalid, err)
	}
	subsystem := strings.TrimSpace(dto.Subsystem)
	draft := &types.PMRoadmapDraft{
		Cognition: dto.Cognition,
		Subsystem: subsystem,
		Flows:     dto.Flows,
		Scope:     dto.Scope,
		Features:  make([]types.PMFeature, 0, len(dto.Features)),
	}
	for _, f := range dto.Features {
		feat := f.toFeature()
		// 行级子系统为空时用批次默认值兜底，形成统一的四列视图
		if strings.TrimSpace(feat.Subsystem) == "" {
			feat.Subsystem = subsystem
		}
		draft.Features = append(draft.Features, feat)
	}
	return draft, nil
}

// renderRoadmapMarkdown 渲染路线图文档：项目认知、首期范围、功能清单（feature-list 四列）、业务流程图
func renderRoadmapMarkdown(
	project *types.PMProject, draft *types.PMRoadmapDraft, features []types.PMFeature,
) string {
	var sb strings.Builder
	sb.WriteString("# 产品路线图-" + project.Name + "\n\n")

	sb.WriteString("## 项目认知\n\n")
	if c := draft.Cognition; c != nil {
		writeKV(&sb, "目标用户", c.TargetUsers)
		writeKV(&sb, "核心价值", c.CoreValue)
		writeKV(&sb, "主要场景", c.Scenarios)
		writeKV(&sb, "本次规划背景", c.Background)
	}
	sb.WriteString("\n")

	sb.WriteString("## 首期范围\n\n")
	if len(draft.Scope) == 0 {
		sb.WriteString("- 未指定，默认全部功能\n")
	}
	for _, path := range draft.Scope {
		if path = strings.TrimSpace(path); path != "" {
			sb.WriteString("- " + path + "\n")
		}
	}
	sb.WriteString("\n")

	sb.WriteString("## 功能清单\n\n")
	writeFeatureListTable(&sb, draft.Subsystem, features)
	sb.WriteString("\n")

	sb.WriteString("## 核心业务流程\n\n")
	rendered := 0
	for _, flow := range draft.Flows {
		code := sanitizeMermaid(flow.Mermaid)
		if code == "" {
			continue // 图示非法则丢弃，只保留上方功能清单表格
		}
		rendered++
		name := strings.TrimSpace(flow.Module)
		if name == "" {
			name = "业务流程"
		}
		sb.WriteString("### " + name + "\n\n```mermaid\n" + code + "\n```\n\n")
	}
	if rendered == 0 {
		sb.WriteString("暂未提供合法流程图。\n")
	}
	return sb.String()
}

// writeFeatureListTable 按 feature-list 技能规范输出四列功能清单：
// 同一「子系统 + 一级模块」的行重复填写两列值（不合并单元格），不同一级模块之间插入分隔行。
func writeFeatureListTable(sb *strings.Builder, fallbackSubsystem string, features []types.PMFeature) {
	fallbackSubsystem = strings.TrimSpace(fallbackSubsystem)
	sb.WriteString("| 子系统 | 一级模块 | 二级模块 | 功能说明 |\n")
	sb.WriteString("| --- | --- | --- | --- |\n")
	lastGroup := ""
	for i, f := range features {
		subsystem := strings.TrimSpace(f.Subsystem)
		if subsystem == "" {
			// 兼容未填写子系统的旧草稿：用批次级子系统兜底
			subsystem = fallbackSubsystem
		}
		module := strings.TrimSpace(f.ModuleName)
		group := subsystem + "\x1f" + module
		if i > 0 && group != lastGroup {
			sb.WriteString("| --- | --- | --- | --- |\n")
		}
		lastGroup = group
		fmt.Fprintf(sb, "| %s | %s | %s | %s |\n",
			tableCell(subsystem), tableCell(module),
			tableCell(f.FeatureName), tableCell(f.OperationName))
	}
}

func writeKV(sb *strings.Builder, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		sb.WriteString("- " + key + "：" + value + "\n")
	}
}

// tableCell 转义 Markdown 表格分隔符与换行
func tableCell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// sanitizeMermaid 仅接受 flowchart TD/LR 子集；首行不合法、括号不配对或含反引号时整段丢弃
func sanitizeMermaid(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || strings.Contains(code, "`") {
		return ""
	}
	first := strings.TrimSpace(strings.SplitN(code, "\n", 2)[0])
	if first != "flowchart TD" && first != "flowchart LR" {
		return ""
	}
	if !balancedBrackets(code) {
		return ""
	}
	return code
}

// balancedBrackets 校验 () [] {} 配对，忽略引号内的括号
func balancedBrackets(s string) bool {
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	var stack []rune
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '(' || r == '[' || r == '{':
			stack = append(stack, r)
		case r == ')' || r == ']' || r == '}':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[r] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0 && quote == 0
}
