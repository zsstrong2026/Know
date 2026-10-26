package interfaces

import (
	"context"

	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 仓储接口：统一约定 ----
//  1. 所有方法首参为 ctx；
//  2. 读写一律带 tenant_id 条件，防跨租户越权；
//  3. GetByXxx 查不到时返回仓储包级 ErrXxxNotFound，由 handler 统一映射 404；
//  4. 列表按 updated_at DESC 返回，PM 数据量小，不做分页；
//  5. Update 影响行数为 0 视为不存在，返回 ErrXxxNotFound。

// PMProjectRepository 项目仓储
type PMProjectRepository interface {
	Create(ctx context.Context, p *types.PMProject) error
	// GetByID 查不到返回 ErrPMProjectNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMProject, error)
	// GetByName 同租户项目名唯一（软删不计），查不到返回 ErrPMProjectNotFound
	GetByName(ctx context.Context, tenantID uint64, name string) (*types.PMProject, error)
	// List status 为空表示不过滤
	List(ctx context.Context, tenantID uint64, status string) ([]*types.PMProject, error)
	Update(ctx context.Context, p *types.PMProject) error
	// Delete 事务内清理项目下全部数据（关联/任务硬删，业务表软删），
	// 项目不存在返回 ErrPMProjectNotFound
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// PMRequirementRepository 需求仓储
type PMRequirementRepository interface {
	Create(ctx context.Context, r *types.PMRequirement) error
	// GetByID 查不到返回 ErrPMRequirementNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMRequirement, error)
	// List status 为空表示不过滤
	List(ctx context.Context, tenantID uint64, projectID string, status string) ([]*types.PMRequirement, error)
	// ListByIDs 批量取需求（版本关联、级联状态变更用），自动过滤租户
	ListByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.PMRequirement, error)
	Update(ctx context.Context, r *types.PMRequirement) error
	// Delete 事务内硬删版本关联、软删附件与需求本体；
	// 需求不存在返回 ErrPMRequirementNotFound
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// PMRequirementAttachmentRepository 需求附件仓储
type PMRequirementAttachmentRepository interface {
	Create(ctx context.Context, a *types.PMRequirementAttachment) error
	// GetByID 查不到返回 ErrPMRequirementAttachmentNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMRequirementAttachment, error)
	// ListByRequirement 按创建时间升序返回，不包含文件正文
	ListByRequirement(ctx context.Context, tenantID uint64, requirementID string) ([]*types.PMRequirementAttachment, error)
	// Delete 软删：查不到返回 ErrPMRequirementAttachmentNotFound
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// PMVersionRepository 版本仓储（含版本-需求关联表的读写）
type PMVersionRepository interface {
	Create(ctx context.Context, v *types.PMVersion) error
	// GetByID 查不到返回 ErrPMVersionNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMVersion, error)
	List(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMVersion, error)
	Update(ctx context.Context, v *types.PMVersion) error
	// GetLatestVersionNo 取项目内最大的已定稿版本号；无历史版本返回 ""
	GetLatestVersionNo(ctx context.Context, tenantID uint64, projectID string) (string, error)
	// ReplaceRequirements 事务内全量替换版本的关联需求（先删后插）
	ReplaceRequirements(ctx context.Context, versionID string, requirementIDs []string) error
	// ListRequirementIDs 返回版本关联的需求 ID，按加入顺序稳定
	ListRequirementIDs(ctx context.Context, versionID string) ([]string, error)
	// CountVersionsByRequirementID 统计需求仍关联的版本数（取消/搁置联动判断）
	CountVersionsByRequirementID(ctx context.Context, requirementID string) (int64, error)
	// RemoveRequirementFromAllVersions 删除需求与全部版本的关联（搁置用）
	RemoveRequirementFromAllVersions(ctx context.Context, requirementID string) error
}

// PMFeatureRepository 功能清单仓储（含迭代记录）
type PMFeatureRepository interface {
	Create(ctx context.Context, f *types.PMFeature) error
	CreateBatch(ctx context.Context, fs []*types.PMFeature) error
	// GetByID 查不到返回 ErrPMFeatureNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMFeature, error)
	ListByProject(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMFeature, error)
	Update(ctx context.Context, f *types.PMFeature) error
	CreateIteration(ctx context.Context, it *types.PMFeatureIteration) error
	// ListIterations 按版本上线计划日升序返回，无版本记录排最后
	ListIterations(ctx context.Context, featureID string) ([]*types.PMFeatureIteration, error)
}

// PMMaterialRepository 项目资料仓储
type PMMaterialRepository interface {
	Create(ctx context.Context, m *types.PMMaterial) error
	// GetByID 查不到返回 ErrPMMaterialNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMMaterial, error)
	// List materialType 为空表示不过滤
	List(ctx context.Context, tenantID uint64, projectID string, materialType string) ([]*types.PMMaterial, error)
	Update(ctx context.Context, m *types.PMMaterial) error
	// Delete 软删：查不到返回 ErrPMMaterialNotFound
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// PMPlanBatchRepository 规划批次仓储
type PMPlanBatchRepository interface {
	Create(ctx context.Context, b *types.PMPlanBatch) error
	// GetByID 查不到返回 ErrPMPlanBatchNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMPlanBatch, error)
	ListByProject(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMPlanBatch, error)
	Update(ctx context.Context, b *types.PMPlanBatch) error
	// GetActiveByProject 无 active 批次返回 ErrPMPlanBatchNotFound
	GetActiveByProject(ctx context.Context, tenantID uint64, projectID string) (*types.PMPlanBatch, error)
	// ActivateBatch 事务内先把旧 active 置 archived，再把目标批次置 active
	ActivateBatch(ctx context.Context, tenantID uint64, projectID string, batchID string) error
	// Delete 软删：查不到返回 ErrPMPlanBatchNotFound
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// PMTaskRepository 异步任务仓储（无软删）
type PMTaskRepository interface {
	Create(ctx context.Context, t *types.PMTask) error
	// GetByID 查不到返回 ErrPMTaskNotFound
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMTask, error)
	// UpdateStatus 仅更新状态与进度，worker 执行中逐步调用
	UpdateStatus(ctx context.Context, id string, status string, progress int) error
	// Succeed 置 succeeded，写 result 与 finished_at
	Succeed(ctx context.Context, id string, result types.JSON) error
	// Fail 置 failed，写 error_message 与 finished_at，进度停在失败点
	Fail(ctx context.Context, id string, message string) error
}

// ---- 服务接口：按业务用例定义，不暴露仓储细节 ----

// PMProjectService 项目业务：创建时编排知识库与会话，归档不删内容
type PMProjectService interface {
	// CreateProject isNew=true 动态建知识库 + 常驻 PM 会话；false 表示关联同名已有项目
	CreateProject(ctx context.Context, tenantID uint64, userID string, name string, isNew bool) (*types.PMProject, error)
	// GetProject 供 RBAC 归属查询与详情页复用
	GetProject(ctx context.Context, tenantID uint64, id string) (*types.PMProject, error)
	ListProjects(ctx context.Context, tenantID uint64, status string) ([]*types.PMProject, error)
	UpdateProject(ctx context.Context, tenantID uint64, id string, name string, description string) (*types.PMProject, error)
	// ArchiveProject 归档/恢复幂等，不删除知识库内容
	ArchiveProject(ctx context.Context, tenantID uint64, id string, archived bool) error
	// DeleteProject 删除项目及其数据，并尽力清理关联知识库与会话
	DeleteProject(ctx context.Context, tenantID uint64, id string) error
}

// PMKBDirService 负责 PM 产物在知识库中的目录/前缀约定
// 知识库若不支持多级目录，自动退化为命名前缀；Resolve 必须是纯函数
type PMKBDirService interface {
	// EnsureProjectDirs 初始化项目目录，尽力而为，失败由调用方决定是否告警
	EnsureProjectDirs(ctx context.Context, tenantID uint64, kbID string) error
	// Resolve 逻辑目录 + 名称 → 实际落库标题/前缀
	Resolve(dir types.PMDir, name string) string
}

// PMRoadmapService 功能规划业务：草稿 → 生成合并 → 确认生效
type PMRoadmapService interface {
	ListBatches(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMPlanBatch, error)
	GetBatch(ctx context.Context, tenantID uint64, id string) (*types.PMPlanBatch, error)
	CreateBatch(ctx context.Context, tenantID uint64, projectID string, batchName string, planType string) (*types.PMPlanBatch, error)
	// SaveDraft 整体覆盖草稿，current_step 单调不减
	SaveDraft(ctx context.Context, tenantID uint64, batchID string, draft types.JSON, step int) error
	// GenerateRoadmap 同步生成路线图，返回异步任务载体（含进度与产物 ID）
	GenerateRoadmap(ctx context.Context, tenantID uint64, batchID string, syncFeatures bool) (*types.PMTask, error)
	// ActivateBatch 确认生效：归档旧批次 + 置新批次 active + 写快照指针
	ActivateBatch(ctx context.Context, tenantID uint64, batchID string, operatorID string) error
	// DeleteBatch 仅草稿批次可删，已生效/已归档批次保留用于回溯
	DeleteBatch(ctx context.Context, tenantID uint64, batchID string) error
	// StartGuideSession 创建隐藏的 AI 引导会话，返回会话 ID 与首轮指令
	StartGuideSession(ctx context.Context, tenantID uint64, batchID string) (string, string, error)
}

// PMRequirementService 需求业务：状态机唯一写入口 + AI 分析编排
type PMRequirementService interface {
	CreateRequirement(ctx context.Context, tenantID uint64, r *types.PMRequirement) (*types.PMRequirement, error)
	GetRequirement(ctx context.Context, tenantID uint64, id string) (*types.PMRequirement, error)
	ListRequirements(ctx context.Context, tenantID uint64, projectID string, status string) ([]*types.PMRequirement, error)
	UpdateRequirement(ctx context.Context, tenantID uint64, r *types.PMRequirement) error
	// UpdateStatus 唯一状态写入入口，内部统一过状态机
	UpdateStatus(ctx context.Context, tenantID uint64, id string, to string) error
	// Reactivate 仅 rejected/closed 可调用，重置为 pending_analysis 并清空 AI 结论
	Reactivate(ctx context.Context, tenantID uint64, id string) error
	// DeleteRequirement 删除需求：同步移除版本关联，软删附件与需求本体
	DeleteRequirement(ctx context.Context, tenantID uint64, id string) error
	// StartAnalysis 建会话并返回 session_id 供前端 SSE 订阅；仅作分析辅助，不改变需求状态
	StartAnalysis(ctx context.Context, tenantID uint64, id string) (string, error)
	// SaveAnalysis 对话结束后回写 AI 分析与设计方案
	SaveAnalysis(ctx context.Context, tenantID uint64, id string, aiAnalysis types.JSON, designPlan string) error
	// VerifyLaunch 上线验证回填，仅 launched 状态允许；重复回填覆盖旧值并刷新 value_achieved_at
	VerifyLaunch(
		ctx context.Context, tenantID uint64, id string, in types.LaunchVerificationInput,
	) (*types.PMRequirement, error)

	// AddAttachment 上传附件：校验大小/类型，文本类抽取正文供 AI 上下文使用
	AddAttachment(
		ctx context.Context, tenantID uint64, userID string, requirementID string,
		fileName string, contentType string, data []byte,
	) (*types.PMRequirementAttachment, error)
	// ListAttachments 需求附件列表（不含文件正文）
	ListAttachments(ctx context.Context, tenantID uint64, requirementID string) ([]*types.PMRequirementAttachment, error)
	// DeleteAttachment 删除需求附件
	DeleteAttachment(ctx context.Context, tenantID uint64, requirementID string, attachmentID string) error
	// GetAttachmentFile 读取附件正文，供下载/预览端点直接写响应
	GetAttachmentFile(
		ctx context.Context, tenantID uint64, requirementID string, attachmentID string,
	) (*types.PMRequirementAttachment, error)
}

// PMVersionArtifactRunner 版本产物生成能力：异步任务 worker 通过该接口回调逐章生成。
// 单独抽出便于任务服务只依赖「执行产物生成」这一职责，避免与任务服务形成循环依赖。
type PMVersionArtifactRunner interface {
	RunArtifactGeneration(
		ctx context.Context, tenantID uint64, taskID string, versionID string,
		regenerate bool, chapters []string,
	) (types.JSON, error)
}

// PMVersionService 版本业务：生成 → 评审 → 定稿 → 状态级联
type PMVersionService interface {
	PMVersionArtifactRunner
	CreateVersion(ctx context.Context, tenantID uint64, v *types.PMVersion, requirementIDs []string) (*types.PMVersion, error)
	GetVersion(ctx context.Context, tenantID uint64, id string) (*types.PMVersion, error)
	ListVersions(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMVersion, error)
	UpdateVersion(ctx context.Context, tenantID uint64, v *types.PMVersion) error
	// Review 追加评审轮次（round+1），历史轮次不可修改
	Review(ctx context.Context, tenantID uint64, id string, conclusion string, opinions types.JSON) (*types.PMVersion, error)
	// Finalize 定稿幂等：校验最近一轮结论 → 生成版本号 → 联动需求
	Finalize(ctx context.Context, tenantID uint64, id string) (*types.PMVersion, error)
	// FinalizeWithInput 定稿时允许人工指定版本号与更新内容大纲；空串表示沿用自动规则
	FinalizeWithInput(ctx context.Context, tenantID uint64, id string, versionNo string, updateOutline string) (*types.PMVersion, error)
	// UpdateStatus 状态变更统一入口，变更后级联关联需求
	UpdateStatus(ctx context.Context, tenantID uint64, id string, to string) error

	// ListVersionRequirements 按加入顺序返回版本关联需求
	ListVersionRequirements(ctx context.Context, tenantID uint64, versionID string) ([]*types.PMRequirement, error)
	// GenerateArtifacts 创建异步产物任务并立即返回 pending 载体，worker 逐章生成
	GenerateArtifacts(ctx context.Context, tenantID uint64, versionID string, regenerate bool, chapters []string) (*types.PMTask, error)
	// UpdateReviewOpinion 更新某轮某条评审意见的处理状态与说明
	UpdateReviewOpinion(ctx context.Context, tenantID uint64, id string, round int, index int, status string, note string) (*types.PMVersion, error)

	// RemoveRequirement 从当前版本移出需求；不再关联任何版本时回退为已确认
	RemoveRequirement(ctx context.Context, tenantID uint64, versionID string, requirementID string) error
	// ShelveRequirement 搁置需求：移出全部版本并置为已拒绝
	ShelveRequirement(ctx context.Context, tenantID uint64, versionID string, requirementID string, reason string) error
	// ChangeRequirement 修改版本内需求的优先级与描述
	ChangeRequirement(ctx context.Context, tenantID uint64, versionID string, requirementID string, priority string, description string) error
	// ReplaceRequirement 用项目内已确认需求替换版本内旧需求
	ReplaceRequirement(ctx context.Context, tenantID uint64, versionID string, oldRequirementID string, newRequirementID string) error
}

// PMFeatureService 功能清单业务：双来源同步 + 多对多迭代
type PMFeatureService interface {
	// SyncFromRoadmap 路线图 → 功能清单（source=roadmap），以三级路径为自然键去重
	SyncFromRoadmap(ctx context.Context, tenantID uint64, projectID string, items []types.PMFeature) error
	// SyncFromPRD 版本 PRD → 功能清单（source=prd），归属变化写 change 迭代
	SyncFromPRD(ctx context.Context, tenantID uint64, projectID string, versionID string, items []types.PMFeature) error
	// CreateManual 人工补充（source=manual），可同时关联版本并写 add 迭代
	CreateManual(ctx context.Context, tenantID uint64, in types.CreatePMFeatureInput) (*types.PMFeature, error)
	ListByProject(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMFeature, error)
	ListIterations(ctx context.Context, featureID string) ([]*types.PMFeatureIteration, error)
	// ListTree 返回三级树，节点上携带最新迭代版本号与状态
	ListTree(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMFeatureNode, error)
}

// PMMaterialService 项目资料业务
type PMMaterialService interface {
	CreateMaterial(ctx context.Context, tenantID uint64, m *types.PMMaterial) (*types.PMMaterial, error)
	GetMaterial(ctx context.Context, tenantID uint64, id string) (*types.PMMaterial, error)
	ListMaterials(ctx context.Context, tenantID uint64, projectID string, materialType string) ([]*types.PMMaterial, error)
	UpdateMaterial(ctx context.Context, tenantID uint64, m *types.PMMaterial) error
	// GenerateMaterial 异步生成资料草稿，返回任务载体供轮询
	GenerateMaterial(ctx context.Context, tenantID uint64, id string) (*types.PMTask, error)
	// Publish 发布：同类型旧发布版置 archived，保留可追溯
	Publish(ctx context.Context, tenantID uint64, id string) (*types.PMMaterial, error)
	// UpdateToLatest 按最新已定稿版本复制出新草稿并异步生成，不覆盖已发布版本
	UpdateToLatest(ctx context.Context, tenantID uint64, id string) (*types.PMTask, error)
	// SaveGeneratedContent 生成任务完成后回写正文；已发布/已归档记录不再覆盖
	SaveGeneratedContent(ctx context.Context, tenantID uint64, id string, content string) error
	// DeleteMaterial 删除草稿；已发布与已归档记录不可删除
	DeleteMaterial(ctx context.Context, tenantID uint64, id string) error
	// DownloadMaterial 按类型导出 docx（manual/brochure）或 pptx（ppt）
	DownloadMaterial(ctx context.Context, tenantID uint64, id string) (*types.PMMaterialFile, error)
}

// PMTaskService 异步任务业务：入队 + 进度查询 + asynq 处理入口
type PMTaskService interface {
	CreateTask(ctx context.Context, tenantID uint64, projectID string, taskType string, payload types.JSON) (*types.PMTask, error)
	// GetTask 按 task_id + tenant_id 查询，跨租户返回 ErrPMTaskNotFound
	GetTask(ctx context.Context, tenantID uint64, id string) (*types.PMTask, error)
	// ProcessTask 签名对齐 asynq 处理器；worker 无 HTTP 上下文，需从 payload 重建租户上下文
	ProcessTask(ctx context.Context, t *asynq.Task) error
}
