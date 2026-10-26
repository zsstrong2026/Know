package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 版本服务错误哨兵：handler 依赖 errors.Is 区分业务失败与系统错误
var (
	ErrVersionNotApproved       = errors.New("pm version: review conclusion must be pass or conditional")
	ErrVersionReviewClosed      = errors.New("pm version: review is closed after finalize")
	ErrVersionConclusionInvalid = errors.New("pm version: invalid review conclusion")
	ErrVersionReviewInvalid     = errors.New("pm version: review opinions is not valid json")
	ErrIllegalVersionTransition = errors.New("pm version: illegal status transition")
	ErrVersionPlanDateInvalid   = errors.New("pm version: plan launch date must be after plan date")
	// 以下为 Task 17 新增：产物生成、人工定稿、评审意见与需求集合操作
	ErrVersionNotEditable          = errors.New("pm version: only planning or reviewing is editable")
	ErrVersionNoInvalid            = errors.New("pm version: version no must be X.Y.Z")
	ErrVersionNoDuplicate          = errors.New("pm version: version no already exists in project")
	ErrVersionOutlineTooLong       = errors.New("pm version: update outline exceeds 2000 characters")
	ErrVersionRequirementNotFound  = errors.New("pm version: requirement is not in this version")
	ErrVersionRequirementInvalid   = errors.New("pm version: requirement change is invalid")
	ErrVersionOpinionNotFound      = errors.New("pm version: review opinion not found")
	ErrVersionOpinionInvalid       = errors.New("pm version: review opinion status must be pending or resolved")
	ErrVersionShelveReasonRequired = errors.New("pm version: shelve reason is required")
	ErrVersionNoRequirements       = errors.New("pm version: no requirements to generate")
	ErrVersionKnowledgeUnready     = errors.New("pm version: knowledge service is not configured")
	ErrVersionTaskUnready          = errors.New("pm version: task service is not configured")
)

// 版本人工定稿的输入上限，与原型弹窗的校验口径保持一致
const (
	pmVersionOutlineMaxRunes = 2000
	pmVersionShelveMaxRunes  = 200
)

// PMVersionDeps 版本服务依赖：需求仓储可空（仅跳过联动），
// 生成产物需要项目仓储、任务仓储、知识库与功能清单服务。
type PMVersionDeps struct {
	Repo            interfaces.PMVersionRepository
	RequirementRepo interfaces.PMRequirementRepository
	ProjectRepo     interfaces.PMProjectRepository
	TaskRepo        interfaces.PMTaskRepository
	Knowledge       pmVersionKnowledge
	FeatureSvc      interfaces.PMFeatureService
	// Enqueuer 异步任务入队；Models 逐章生成 PRD/原型所需的对话模型；Attachments 需求附件正文
	Enqueuer    interfaces.TaskEnqueuer
	Models      interfaces.ModelService
	Attachments interfaces.PMRequirementAttachmentRepository
}

// pmVersionKnowledge 产物落库只需要「同步写入知识库」一个能力
type pmVersionKnowledge interface {
	CreateKnowledgeFromPassageSync(
		ctx context.Context, kbID string, passage []string, channel string,
	) (*types.Knowledge, error)
}

// pmVersionService 版本业务：生成 → 评审 → 定稿 → 状态级联。
// requirementRepo 为空时仅跳过联动，不阻塞版本自身流转。
type pmVersionService struct {
	deps PMVersionDeps
}

func NewPMVersionService(
	repo interfaces.PMVersionRepository,
	requirementRepo interfaces.PMRequirementRepository,
) interfaces.PMVersionService {
	return NewPMVersionServiceWithDeps(PMVersionDeps{Repo: repo, RequirementRepo: requirementRepo})
}

// NewPMVersionServiceWithDeps 组合根入口：装配产物生成所需依赖
func NewPMVersionServiceWithDeps(deps PMVersionDeps) interfaces.PMVersionService {
	return &pmVersionService{deps: deps}
}

// CreateVersion 新建版本，默认从 planning 起步并写入关联需求
func (s *pmVersionService) CreateVersion(
	ctx context.Context, tenantID uint64, v *types.PMVersion, requirementIDs []string,
) (*types.PMVersion, error) {
	// 计划上线日必须在规划日之后（排期基线）
	if v.PlanDate != nil && v.PlanLaunchDate != nil && !v.PlanLaunchDate.After(*v.PlanDate) {
		return nil, ErrVersionPlanDateInvalid
	}
	v.TenantID = tenantID
	v.Status = types.VersionStatusPlanning
	v.VersionNo = ""
	v.ReviewRound = 0
	v.ReviewOpinions = nil
	v.ReviewConclusion = ""
	v.FinalizeDate = nil
	v.LaunchDate = nil
	if err := s.deps.Repo.Create(ctx, v); err != nil {
		return nil, err
	}
	if len(requirementIDs) > 0 {
		if err := s.deps.Repo.ReplaceRequirements(ctx, v.ID, requirementIDs); err != nil {
			return nil, err
		}
		if err := s.scheduleRequirements(ctx, tenantID, requirementIDs); err != nil {
			return nil, err
		}
	}
	s.decorate(ctx, v)
	return v, nil
}

// scheduleRequirements 需求进入版本即视为已排期：仅「已确认」自动推进，其余状态保持不变。
func (s *pmVersionService) scheduleRequirements(
	ctx context.Context, tenantID uint64, ids []string,
) error {
	if s.deps.RequirementRepo == nil {
		return nil // 未装配需求仓储时跳过联动
	}
	reqs, err := s.deps.RequirementRepo.ListByIDs(ctx, tenantID, ids)
	if err != nil {
		return err
	}
	for _, r := range reqs {
		if r.Status != types.RequirementStatusConfirmed {
			continue
		}
		r.Status = types.RequirementStatusScheduled
		if err := s.deps.RequirementRepo.Update(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (s *pmVersionService) GetVersion(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMVersion, error) {
	v, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, v)
	return v, nil
}

func (s *pmVersionService) ListVersions(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMVersion, error) {
	list, err := s.deps.Repo.List(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	for _, v := range list {
		s.decorate(ctx, v)
	}
	return list, nil
}

// UpdateVersion 仅更新描述性字段；状态、评审、定稿与上线信息由专用方法写入
func (s *pmVersionService) UpdateVersion(
	ctx context.Context, tenantID uint64, v *types.PMVersion,
) error {
	cur, err := s.deps.Repo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		return err
	}
	if v.PlanDate != nil && v.PlanLaunchDate != nil && !v.PlanLaunchDate.After(*v.PlanDate) {
		return ErrVersionPlanDateInvalid
	}
	v.TenantID = tenantID
	v.Status = cur.Status
	v.VersionNo = cur.VersionNo
	v.ReviewRound = cur.ReviewRound
	v.ReviewOpinions = cur.ReviewOpinions
	v.ReviewConclusion = cur.ReviewConclusion
	v.FinalizeDate = cur.FinalizeDate
	v.LaunchDate = cur.LaunchDate
	// 产物由生成/定稿流程独占写入，普通编辑不得覆盖
	v.PRDKnowledgeID = cur.PRDKnowledgeID
	v.PrototypeKnowledgeID = cur.PrototypeKnowledgeID
	v.ArtifactRound = cur.ArtifactRound
	v.ArtifactFeatureCount = cur.ArtifactFeatureCount
	v.ArtifactRequirementIDs = cur.ArtifactRequirementIDs
	v.GeneratedAt = cur.GeneratedAt
	// 更新内容大纲只在定稿弹窗维护；已有值不被普通编辑清空
	if strings.TrimSpace(v.UpdateOutline) == "" {
		v.UpdateOutline = cur.UpdateOutline
	}
	return s.deps.Repo.Update(ctx, v)
}

// Review 追加一轮评审：轮次 +1，历史轮次原样保留；定稿后评审关闭
func (s *pmVersionService) Review(
	ctx context.Context, tenantID uint64, id string, conclusion string, opinions types.JSON,
) (*types.PMVersion, error) {
	if !types.IsValidVersionConclusion(conclusion) {
		return nil, fmt.Errorf("%w: %s", ErrVersionConclusionInvalid, conclusion)
	}
	v, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if v.Status == types.VersionStatusFinalized || v.Status == types.VersionStatusDeveloping ||
		v.Status == types.VersionStatusLaunched {
		return nil, fmt.Errorf("%w: %s", ErrVersionReviewClosed, v.Status)
	}
	rounds := make([]types.PMVersionReviewRecord, 0, v.ReviewRound+1)
	if len(v.ReviewOpinions) > 0 {
		if err := json.Unmarshal(v.ReviewOpinions, &rounds); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrVersionReviewInvalid, err)
		}
	}
	v.ReviewRound++
	rounds = append(rounds, types.PMVersionReviewRecord{
		Round:      v.ReviewRound,
		Conclusion: conclusion,
		Opinions:   opinions,
		Reviewers:  v.Reviewers,
		ReviewedAt: time.Now(),
	})
	raw, err := json.Marshal(rounds)
	if err != nil {
		return nil, err
	}
	v.ReviewOpinions = types.JSON(raw)
	v.ReviewConclusion = conclusion
	v.Status = types.VersionStatusReviewing
	if err := s.deps.Repo.Update(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

// Finalize 定稿：校验最近一轮结论 → 生成 version_no → 状态置 finalized → 联动需求。
// 必须幂等：已定稿及之后的版本直接返回已有结果。
func (s *pmVersionService) Finalize(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMVersion, error) {
	return s.FinalizeWithInput(ctx, tenantID, id, "", "")
}

// FinalizeWithInput 人工定稿：允许指定版本号与更新内容大纲，空串表示沿用自动规则
func (s *pmVersionService) FinalizeWithInput(
	ctx context.Context, tenantID uint64, id string, versionNo string, updateOutline string,
) (*types.PMVersion, error) {
	v, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if v.Status == types.VersionStatusFinalized || v.Status == types.VersionStatusDeveloping ||
		v.Status == types.VersionStatusLaunched {
		return v, nil
	}
	if v.ReviewRound < 1 || !isPassConclusion(v.ReviewConclusion) {
		return nil, fmt.Errorf("%w: round=%d conclusion=%q",
			ErrVersionNotApproved, v.ReviewRound, v.ReviewConclusion)
	}
	if !types.CanTransitVersion(v.Status, types.VersionStatusFinalized) {
		return nil, fmt.Errorf("%w: %s -> %s",
			ErrIllegalVersionTransition, v.Status, types.VersionStatusFinalized)
	}

	// 版本号：人工优先（格式 X.Y.Z，可带 v 前缀），否则按历史最大版本自动递增
	auto := strings.TrimSpace(versionNo) == ""
	if auto {
		latest, err := s.deps.Repo.GetLatestVersionNo(ctx, tenantID, v.ProjectID)
		if err != nil {
			return nil, err
		}
		versionNo = types.NextVersionNo(latest, 1)
	} else {
		normalized, err := normalizeVersionNoInput(versionNo)
		if err != nil {
			return nil, err
		}
		versionNo = normalized
		if err := s.ensureVersionNoUnique(ctx, tenantID, v, versionNo); err != nil {
			return nil, err
		}
	}

	outline := strings.TrimSpace(updateOutline)
	if utf8.RuneCountInString(outline) > pmVersionOutlineMaxRunes {
		return nil, ErrVersionOutlineTooLong
	}

	now := time.Now()
	v.VersionNo = versionNo
	v.UpdateOutline = outline
	v.Status = types.VersionStatusFinalized
	v.FinalizeDate = &now
	if err := s.deps.Repo.Update(ctx, v); err != nil {
		return nil, err
	}
	// 定稿后把已确认需求纳入排期
	if err := s.cascadeRequirements(ctx, tenantID, v, now); err != nil {
		return nil, err
	}
	s.decorate(ctx, v)
	return v, nil
}

// ensureVersionNoUnique 人工指定的版本号在项目内必须唯一
func (s *pmVersionService) ensureVersionNoUnique(
	ctx context.Context, tenantID uint64, v *types.PMVersion, versionNo string,
) error {
	list, err := s.deps.Repo.List(ctx, tenantID, v.ProjectID)
	if err != nil {
		return err
	}
	for _, other := range list {
		if other.ID != v.ID && other.VersionNo == versionNo {
			return fmt.Errorf("%w: %s", ErrVersionNoDuplicate, versionNo)
		}
	}
	return nil
}

// normalizeVersionNoInput 规范化人工输入：去 v 前缀，必须是三段非负整数
func normalizeVersionNoInput(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V")
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: %s", ErrVersionNoInvalid, raw)
	}
	nums := make([]string, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return "", fmt.Errorf("%w: %s", ErrVersionNoInvalid, raw)
		}
		nums[i] = strconv.Itoa(n)
	}
	return strings.Join(nums, "."), nil
}

// UpdateStatus 状态变更统一入口，变更后按级联表同步关联需求
func (s *pmVersionService) UpdateStatus(
	ctx context.Context, tenantID uint64, id string, to string,
) error {
	// 定稿包含版本号生成与结论校验，必须走 Finalize
	if to == types.VersionStatusFinalized {
		_, err := s.Finalize(ctx, tenantID, id)
		return err
	}
	v, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if v.Status == to {
		return nil
	}
	if !types.CanTransitVersion(v.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalVersionTransition, v.Status, to)
	}
	now := time.Now()
	v.Status = to
	if to == types.VersionStatusLaunched {
		v.LaunchDate = &now
	}
	if err := s.deps.Repo.Update(ctx, v); err != nil {
		return err
	}
	return s.cascadeRequirements(ctx, tenantID, v, now)
}

// cascadeRequirements 按方案 §4.2 的级联表同步关联需求状态
func (s *pmVersionService) cascadeRequirements(
	ctx context.Context, tenantID uint64, v *types.PMVersion, at time.Time,
) error {
	targets := requirementCascadeTargets(v.Status)
	if len(targets) == 0 {
		return nil
	}
	if s.deps.RequirementRepo == nil {
		return nil // 未装配需求仓储时仅跳过联动（如仅做版本自身流转）
	}
	ids, err := s.deps.Repo.ListRequirementIDs(ctx, v.ID)
	if err != nil || len(ids) == 0 {
		return err
	}
	reqs, err := s.deps.RequirementRepo.ListByIDs(ctx, tenantID, ids)
	if err != nil {
		return err
	}
	for _, r := range reqs {
		to, ok := targets[r.Status]
		if !ok {
			continue // 不在级联范围内的需求保持原状态
		}
		r.Status = to
		if to == types.RequirementStatusLaunched {
			r.ActualLaunchDate = &at
		}
		if err := s.deps.RequirementRepo.Update(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// requirementCascadeTargets 版本状态 → 需求状态迁移映射（目标为空表示不联动）
func requirementCascadeTargets(versionStatus string) map[string]string {
	switch versionStatus {
	case types.VersionStatusFinalized:
		return map[string]string{types.RequirementStatusConfirmed: types.RequirementStatusScheduled}
	case types.VersionStatusDeveloping:
		return map[string]string{types.RequirementStatusScheduled: types.RequirementStatusDeveloping}
	case types.VersionStatusLaunched:
		return map[string]string{types.RequirementStatusDeveloping: types.RequirementStatusLaunched}
	case types.VersionStatusShelved:
		return map[string]string{
			types.RequirementStatusScheduled:  types.RequirementStatusConfirmed,
			types.RequirementStatusDeveloping: types.RequirementStatusConfirmed,
		}
	}
	return nil
}

// isPassConclusion 定稿允许的评审结论：通过或有条件通过
func isPassConclusion(conclusion string) bool {
	return conclusion == types.VersionConclusionPass ||
		conclusion == types.VersionConclusionConditional
}
