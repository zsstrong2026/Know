package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils/pmdocs"
)

// 资料业务错误哨兵：handler 统一映射 400，避免把业务校验当系统错误
var (
	ErrMaterialTitleRequired   = errors.New("pm material: title is required")
	ErrMaterialTypeInvalid     = errors.New("pm material: invalid material type")
	ErrMaterialVersionRequired = errors.New("pm material: version is required")
	ErrMaterialVersionInvalid  = errors.New("pm material: version is not finalized")
	ErrMaterialNotDraft        = errors.New("pm material: only draft can be generated or edited")
	ErrMaterialArchived        = errors.New("pm material: archived material is read-only")
	ErrMaterialAlreadyLatest   = errors.New("pm material: already linked to the latest version")
	ErrMaterialNoLatestVersion = errors.New("pm material: project has no finalized version")
	ErrMaterialTaskUnready     = errors.New("pm material: task runtime is not configured")
	// 以下为文档能力相关：正文缺失、知识库未装配、非草稿不可删
	ErrMaterialContentEmpty     = errors.New("pm material: content is empty, generate it first")
	ErrMaterialKnowledgeUnready = errors.New("pm material: knowledge service is not configured")
	ErrMaterialReadOnly         = errors.New("pm material: only draft material can be deleted")
)

// 资料落库知识库的通道标识，与知识库来源列表保持一致
const pmMaterialChannel = "pm-material"

// pmMaterialKnowledge 发布落库只需要「同步写入知识库」一个能力
type pmMaterialKnowledge interface {
	CreateKnowledgeFromPassageSync(
		ctx context.Context, kbID string, passage []string, channel string,
	) (*types.Knowledge, error)
}

// PMMaterialDeps 资料业务依赖：生成走 asynq，版本仓储用于「有新版本」判定，
// 项目仓储 + 知识库 + 目录约定仅发布落库时使用（可空，缺失时报错而非静默降级）。
type PMMaterialDeps struct {
	Repo        interfaces.PMMaterialRepository
	VersionRepo interfaces.PMVersionRepository
	TaskRepo    interfaces.PMTaskRepository
	Enqueuer    interfaces.TaskEnqueuer
	ProjectRepo interfaces.PMProjectRepository
	Knowledge   pmMaterialKnowledge
	KBDir       interfaces.PMKBDirService
}

type pmMaterialService struct {
	deps PMMaterialDeps
}

func NewPMMaterialService(deps PMMaterialDeps) interfaces.PMMaterialService {
	return &pmMaterialService{deps: deps}
}

// CreateMaterial 创建资料（类型 + 目标版本），初始 draft
func (s *pmMaterialService) CreateMaterial(
	ctx context.Context, tenantID uint64, m *types.PMMaterial,
) (*types.PMMaterial, error) {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return nil, ErrMaterialTitleRequired
	}
	if !isMaterialType(m.MaterialType) {
		return nil, fmt.Errorf("%w: %q", ErrMaterialTypeInvalid, m.MaterialType)
	}
	if strings.TrimSpace(m.VersionID) == "" {
		return nil, ErrMaterialVersionRequired
	}
	// 对应版本必须是已定稿及以上，避免基于仍会变动的版本产出资料
	if err := s.ensureUsableVersion(ctx, tenantID, m.VersionID); err != nil {
		return nil, err
	}

	m.ID = uuid.New().String()
	m.TenantID = tenantID
	m.Status = types.MaterialStatusDraft
	m.Archived = false
	if err := s.deps.Repo.Create(ctx, m); err != nil {
		return nil, err
	}
	s.decorate(ctx, tenantID, m)
	return m, nil
}

func (s *pmMaterialService) GetMaterial(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMMaterial, error) {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, tenantID, m)
	return m, nil
}

// ListMaterials 列表按类型过滤，并回填关联版本号与「有新版本」提示
func (s *pmMaterialService) ListMaterials(
	ctx context.Context, tenantID uint64, projectID string, materialType string,
) ([]*types.PMMaterial, error) {
	list, err := s.deps.Repo.List(ctx, tenantID, projectID, materialType)
	if err != nil {
		return nil, err
	}
	latest := s.latestVersionNo(ctx, tenantID, projectID)
	// 同一版本只查一次，避免逐条访问仓储
	versionNos := make(map[string]string, len(list))
	for _, m := range list {
		no, ok := versionNos[m.VersionID]
		if !ok {
			no = s.versionNo(ctx, tenantID, m.VersionID)
			versionNos[m.VersionID] = no
		}
		m.VersionNo = no
		m.LatestVersionNo = latest
		m.NeedsUpdate = materialNeedsUpdate(m, no, latest)
	}
	return list, nil
}

// UpdateMaterial 仅草稿可改标题/类型/对应版本；已发布与归档记录保持只读
func (s *pmMaterialService) UpdateMaterial(
	ctx context.Context, tenantID uint64, m *types.PMMaterial,
) error {
	cur, err := s.deps.Repo.GetByID(ctx, tenantID, m.ID)
	if err != nil {
		return err
	}
	if cur.Status != types.MaterialStatusDraft || cur.Archived {
		return ErrMaterialNotDraft
	}
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return ErrMaterialTitleRequired
	}
	if m.MaterialType != "" && !isMaterialType(m.MaterialType) {
		return fmt.Errorf("%w: %q", ErrMaterialTypeInvalid, m.MaterialType)
	}
	m.TenantID = tenantID
	m.ProjectID = cur.ProjectID
	m.Status = types.MaterialStatusDraft
	m.Archived = false
	m.CreatedBy = cur.CreatedBy
	return s.deps.Repo.Update(ctx, m)
}

// GenerateMaterial 投递 asynq 任务，立即返回 task_id；资料状态保持 draft
func (s *pmMaterialService) GenerateMaterial(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMTask, error) {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if m.Status != types.MaterialStatusDraft || m.Archived {
		return nil, ErrMaterialNotDraft
	}
	return s.enqueueGenerate(ctx, tenantID, m)
}

// Publish 发布：正文落库知识库「资料」目录，status=published，
// 同类型旧 published 置 archived=true（不删除）。
func (s *pmMaterialService) Publish(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMMaterial, error) {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if m.Archived {
		return nil, ErrMaterialArchived
	}
	if m.Status == types.MaterialStatusPublished {
		return m, nil // 幂等：重复发布不改动归档关系
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil, ErrMaterialContentEmpty
	}
	if s.deps.Knowledge == nil || s.deps.ProjectRepo == nil {
		return nil, ErrMaterialKnowledgeUnready
	}

	project, err := s.deps.ProjectRepo.GetByID(ctx, tenantID, m.ProjectID)
	if err != nil {
		return nil, err
	}
	// 正式格式（docx/pptx）由下载端点按需导出，知识库只存 Markdown 正文便于检索
	knowledge, err := s.deps.Knowledge.CreateKnowledgeFromPassageSync(
		ctx, project.KBID,
		[]string{renderMaterialDocument(s.knowledgeTitle(m), m.Content)},
		pmMaterialChannel,
	)
	if err != nil {
		return nil, err
	}

	// 同项目同类型只保留一个「当前发布版」，其余转归档便于回溯
	siblings, err := s.deps.Repo.List(ctx, tenantID, m.ProjectID, m.MaterialType)
	if err != nil {
		return nil, err
	}
	for _, other := range siblings {
		if other.ID == m.ID || other.Status != types.MaterialStatusPublished || other.Archived {
			continue
		}
		other.Archived = true
		if err := s.deps.Repo.Update(ctx, other); err != nil {
			return nil, err
		}
	}

	m.Status = types.MaterialStatusPublished
	m.Archived = false
	m.KnowledgeID = knowledge.ID
	if err := s.deps.Repo.Update(ctx, m); err != nil {
		return nil, err
	}
	s.decorate(ctx, tenantID, m)
	return m, nil
}

// SaveGeneratedContent 生成任务完成后回写正文；已发布/已归档记录不再覆盖，避免冲掉线上版本
func (s *pmMaterialService) SaveGeneratedContent(
	ctx context.Context, tenantID uint64, id string, content string,
) error {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if m.Archived || m.Status != types.MaterialStatusDraft {
		return nil
	}
	m.Content = content
	return s.deps.Repo.Update(ctx, m)
}

// DeleteMaterial 仅草稿可删；已发布/已归档记录保留可追溯
func (s *pmMaterialService) DeleteMaterial(
	ctx context.Context, tenantID uint64, id string,
) error {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if m.Archived || m.Status != types.MaterialStatusDraft {
		return ErrMaterialReadOnly
	}
	return s.deps.Repo.Delete(ctx, tenantID, id)
}

// DownloadMaterial 按类型导出正式格式：手册/宣传册 → docx，PPT → pptx
func (s *pmMaterialService) DownloadMaterial(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMMaterialFile, error) {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil, ErrMaterialContentEmpty
	}
	base := materialFileBase(m.Title)
	if m.MaterialType == types.MaterialTypePPT {
		data, err := pmdocs.RenderPptx(m.Title, m.Content)
		if err != nil {
			return nil, err
		}
		return &types.PMMaterialFile{
			FileName:    base + ".pptx",
			ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			Data:        data,
		}, nil
	}
	data, err := pmdocs.RenderDocx(m.Title, m.Content)
	if err != nil {
		return nil, err
	}
	return &types.PMMaterialFile{
		FileName:    base + ".docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Data:        data,
	}, nil
}

// UpdateToLatest 以最新已定稿版本复制出新草稿并异步生成，不覆盖已发布版本
func (s *pmMaterialService) UpdateToLatest(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMTask, error) {
	m, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if m.Archived {
		return nil, ErrMaterialArchived
	}
	if s.deps.VersionRepo == nil {
		return nil, ErrMaterialTaskUnready
	}
	latest, err := s.latestVersion(ctx, tenantID, m.ProjectID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, ErrMaterialNoLatestVersion
	}
	if latest.ID == m.VersionID {
		return nil, ErrMaterialAlreadyLatest
	}

	draft := &types.PMMaterial{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		ProjectID:    m.ProjectID,
		MaterialType: m.MaterialType,
		VersionID:    latest.ID,
		Title:        m.Title,
		Status:       types.MaterialStatusDraft,
		CreatedBy:    m.CreatedBy,
	}
	if err := s.deps.Repo.Create(ctx, draft); err != nil {
		return nil, err
	}
	return s.enqueueGenerate(ctx, tenantID, draft)
}

// enqueueGenerate 落 pending 任务并投递；投递失败即刻置 failed，避免前端空轮询
func (s *pmMaterialService) enqueueGenerate(
	ctx context.Context, tenantID uint64, m *types.PMMaterial,
) (*types.PMTask, error) {
	if s.deps.TaskRepo == nil || s.deps.Enqueuer == nil {
		return nil, ErrMaterialTaskUnready
	}
	taskID := uuid.New().String()
	raw, err := json.Marshal(types.PMPayload{
		TenantID: tenantID,
		TaskID:   taskID,
		TargetID: m.ID,
		Title:    m.Title,
		Sections: materialSections(m),
	})
	if err != nil {
		return nil, fmt.Errorf("pm material: marshal payload: %w", err)
	}

	task := &types.PMTask{
		ID:        taskID,
		TenantID:  tenantID,
		ProjectID: m.ProjectID,
		TaskType:  types.TypePMMaterialGenerate,
		Status:    types.PMTaskStatusPending,
		Payload:   types.JSON(raw),
		CreatedBy: m.CreatedBy,
	}
	if err := s.deps.TaskRepo.Create(ctx, task); err != nil {
		return nil, err
	}
	if _, err := s.deps.Enqueuer.Enqueue(
		asynq.NewTask(types.TypePMMaterialGenerate, raw),
	); err != nil {
		_ = s.deps.TaskRepo.Fail(ctx, task.ID, err.Error())
		return nil, err
	}
	return task, nil
}

// materialSections 按资料类型给出草稿骨架，后续由 LLM 润色补全
func materialSections(m *types.PMMaterial) []types.PMPayloadSection {
	var headings []string
	switch m.MaterialType {
	case types.MaterialTypeManual:
		headings = []string{"产品概述", "功能说明", "操作步骤", "常见问题"}
	case types.MaterialTypeBrochure:
		headings = []string{"产品定位", "核心价值", "应用场景", "客户案例"}
	default: // ppt
		headings = []string{"封面", "产品亮点", "功能架构", "落地路径"}
	}
	sections := make([]types.PMPayloadSection, 0, len(headings))
	for _, h := range headings {
		sections = append(sections, types.PMPayloadSection{
			Heading: h,
			Body:    fmt.Sprintf("围绕《%s》整理%s内容，对应版本 %s。", m.Title, h, m.VersionID),
		})
	}
	return sections
}

// knowledgeTitle 落库标题走目录约定（资料/ MAT- 前缀），未装配目录服务时退化为前缀
func (s *pmMaterialService) knowledgeTitle(m *types.PMMaterial) string {
	if s.deps.KBDir != nil {
		return s.deps.KBDir.Resolve(types.PMDirMaterial, m.Title)
	}
	return "MAT-" + strings.TrimSpace(m.Title)
}

// renderMaterialDocument 组装知识库正文：标题作 H1；正文已含同名 H1 时不重复
func renderMaterialDocument(title, content string) string {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" {
		title = "项目资料"
	}
	first, _, _ := strings.Cut(content, "\n")
	if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), "#")) == title {
		return content
	}
	return "# " + title + "\n\n" + content
}

// materialFileBase 去掉文件名非法字符，避免 Content-Disposition 注入与路径穿越
func materialFileBase(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "项目资料"
	}
	return strings.NewReplacer(
		`\`, "_", "/", "_", ":", "_", "*", "_", "?", "_", `"`, "_",
		"<", "_", ">", "_", "|", "_", "\r", "", "\n", "",
	).Replace(title)
}

// decorate 单条回填派生字段
func (s *pmMaterialService) decorate(ctx context.Context, tenantID uint64, m *types.PMMaterial) {
	m.VersionNo = s.versionNo(ctx, tenantID, m.VersionID)
	m.LatestVersionNo = s.latestVersionNo(ctx, tenantID, m.ProjectID)
	m.NeedsUpdate = materialNeedsUpdate(m, m.VersionNo, m.LatestVersionNo)
}

func (s *pmMaterialService) versionNo(ctx context.Context, tenantID uint64, versionID string) string {
	if s.deps.VersionRepo == nil || versionID == "" {
		return ""
	}
	v, err := s.deps.VersionRepo.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return ""
	}
	return v.VersionNo
}

func (s *pmMaterialService) latestVersionNo(ctx context.Context, tenantID uint64, projectID string) string {
	if s.deps.VersionRepo == nil {
		return ""
	}
	no, err := s.deps.VersionRepo.GetLatestVersionNo(ctx, tenantID, projectID)
	if err != nil {
		return ""
	}
	return no
}

// latestVersion 取项目内版本号最大的已定稿版本记录
func (s *pmMaterialService) latestVersion(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.PMVersion, error) {
	list, err := s.deps.VersionRepo.List(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	var latest *types.PMVersion
	for _, v := range list {
		if strings.TrimSpace(v.VersionNo) == "" {
			continue
		}
		if latest == nil || compareVersionNo(v.VersionNo, latest.VersionNo) > 0 {
			latest = v
		}
	}
	return latest, nil
}

func (s *pmMaterialService) ensureUsableVersion(
	ctx context.Context, tenantID uint64, versionID string,
) error {
	if s.deps.VersionRepo == nil {
		return nil // 未装配版本仓储时仅校验非空，便于最小化部署
	}
	v, err := s.deps.VersionRepo.GetByID(ctx, tenantID, versionID)
	if err != nil {
		// 版本不存在对调用方等价于入参非法，统一映射 400
		return fmt.Errorf("%w: %s", ErrMaterialVersionInvalid, versionID)
	}
	switch v.Status {
	case types.VersionStatusFinalized, types.VersionStatusDeveloping, types.VersionStatusLaunched:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrMaterialVersionInvalid, v.Status)
	}
}

// materialNeedsUpdate 归档记录不再提示；版本号变化即视为有新版本
func materialNeedsUpdate(m *types.PMMaterial, versionNo string, latestNo string) bool {
	if m.Archived || latestNo == "" || versionNo == "" {
		return false
	}
	return versionNo != latestNo
}

func isMaterialType(t string) bool {
	switch t {
	case types.MaterialTypeManual, types.MaterialTypeBrochure, types.MaterialTypePPT:
		return true
	}
	return false
}

// compareVersionNo 仅比较 Vx.y.z 的数字段，缺失段按 0 处理
func compareVersionNo(a, b string) int {
	x, y := parseVersionNo(a), parseVersionNo(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			if x[i] > y[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVersionNo(v string) [3]int {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "V"), ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out
		}
		out[i] = n
	}
	return out
}
