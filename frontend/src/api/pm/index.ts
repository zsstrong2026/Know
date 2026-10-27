// ===== 类型定义（与后端 internal/types/pm.go 对齐）=====

// 需求状态（7 态）
export type PmRequirementStatus =
  | 'pending_analysis' | 'confirmed' | 'scheduled'
  | 'developing' | 'launched' | 'rejected' | 'closed';
// 版本状态（6 态）
export type PmVersionStatus =
  | 'planning' | 'reviewing' | 'finalized' | 'developing' | 'launched' | 'shelved';
export type PmPriority = 'P0' | 'P1' | 'P2' | 'P3';
export type PmRequirementSource = 'customer' | 'internal' | 'data' | 'competitor' | 'strategy' | 'other';
export type PmValueAchieved = 'achieved' | 'partial' | 'not_achieved';
export type PmReviewConclusion = 'pass' | 'conditional' | 'reject';
export type PmProjectStatus = 'active' | 'archived';
export type PmPlanType = 'initial' | 'iteration' | 'replan';
export type PmPlanBatchStatus = 'draft' | 'active' | 'archived';
export type PmFeatureSource = 'roadmap' | 'prd' | 'manual';
export type PmFeatureStatus = 'planned' | 'launched';
export type PmFeatureChangeType = 'add' | 'change' | 'remove';
export type PmTaskStatus = 'pending' | 'running' | 'succeeded' | 'failed';
// 资料类型：操作手册 / 宣传册 / PPT
export type PmMaterialType = 'manual' | 'brochure' | 'ppt';
export type PmMaterialStatus = 'draft' | 'published';

export interface PmProject {
  id: string;
  name: string;
  description: string;
  kb_id: string;
  session_id: string;
  status: PmProjectStatus;
  owner_id: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface PmRequirement {
  id: string;
  project_id: string;
  title: string;
  description: string;
  priority: PmPriority;
  status: PmRequirementStatus;
  source: PmRequirementSource;
  proposer: string;
  // AI 引导分析结论（价值/关联影响/风险/建议优先级/建议状态），空对象表示未分析
  ai_analysis: Record<string, unknown> | null;
  design_plan: string;
  session_id: string;
  knowledge_id: string;
  actual_launch_date: string | null;
  effect_notes: string;
  value_achieved: PmValueAchieved | '';
  retro_notes: string;
  value_achieved_at: string | null;
  // 搁置（评审不通过）时记录的拒绝原因
  reject_reason: string;
  owner_id: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

// 需求附件：文件字节存于后端，列表接口不返回正文
export interface PmRequirementAttachment {
  id: string;
  requirement_id: string;
  file_name: string;
  content_type: string;
  size: number;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface PmVersion {
  id: string;
  project_id: string;
  version_no: string;
  name: string;
  status: PmVersionStatus;
  review_round: number;
  review_opinions: unknown;
  review_conclusion: PmReviewConclusion | '';
  reviewers: string;
  release_note: string;
  plan_date: string | null;
  plan_launch_date: string | null;
  finalize_date: string | null;
  launch_date: string | null;
  // 定稿时人工填写的更新内容大纲
  update_outline: string;
  // 产物（PRD / 原型）落库后的知识文档 ID，空串表示尚未生成
  prd_knowledge_id: string;
  prototype_knowledge_id: string;
  // 产物生成轮次：0 表示从未生成，用于「重新生成」按钮判定
  artifact_round: number;
  artifact_feature_count: number;
  generated_at: string | null;
  // 以下为后端派生的只读字段
  requirement_count: number;
  requirements_changed: boolean;
  owner_id: string;
  created_at: string;
  updated_at: string;
}

export interface PmFeature {
  id: string;
  project_id: string;
  requirement_id: string;
  parent_id: string;
  // 子系统（feature-list 第一列），仅随规划草稿流转，不落功能清单表
  subsystem?: string;
  module_name: string;
  feature_name: string;
  operation_name: string;
  source: PmFeatureSource;
  status: PmFeatureStatus;
  // 草稿阶段尚未落库，时间字段由后端在同步功能清单时补齐
  created_at?: string;
  updated_at?: string;
}

// 功能清单三级树节点，携带最新迭代版本号与状态
export interface PmFeatureNode extends PmFeature {
  children?: PmFeatureNode[];
  latest_version_no?: string;
  latest_version_status?: PmVersionStatus;
}

export interface PmFeatureIteration {
  id: string;
  feature_id: string;
  version_id: string;
  change_type: PmFeatureChangeType;
  change_note: string;
  changed_at: string;
}

// 5 步引导草稿
export interface PmRoadmapCognition {
  target_users: string;
  core_value: string;
  scenarios: string;
  background: string;
}
export interface PmRoadmapFlow {
  module: string;
  mermaid: string;
}
export interface PmRoadmapDraft {
  cognition?: PmRoadmapCognition;
  // 本次规划的子系统（feature-list 第一列），由用户指定；行级为空时以它为准
  subsystem?: string;
  features: PmFeature[];
  flows?: PmRoadmapFlow[];
  // 首期范围，元素为「模块/功能点/操作项」三级路径
  scope?: string[];
}

export interface PmPlanBatch {
  id: string;
  project_id: string;
  batch_name: string;
  plan_type: PmPlanType;
  version_id: string;
  change_summary: string;
  status: PmPlanBatchStatus;
  draft: PmRoadmapDraft | null;
  current_step: number;
  roadmap_knowledge_id: string;
  snapshot_knowledge_id: string;
  generated_at: string | null;
  snapshot_at: string | null;
  operator_id: string;
  activated_at: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface PmTask {
  id: string;
  project_id: string;
  task_type: string;
  status: PmTaskStatus;
  progress: number;
  payload: unknown;
  result: unknown;
  error_message: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
}

export interface PmMaterial {
  id: string;
  project_id: string;
  material_type: PmMaterialType;
  // 资料对应的项目版本
  version_id: string;
  // 发布后落库的知识文档 ID，空串表示尚未生成/落库
  knowledge_id: string;
  title: string;
  status: PmMaterialStatus;
  // 旧发布版在更新后归档保留
  archived: boolean;
  // 生成任务回写的 Markdown 正文；空串表示尚未生成
  content?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  // 以下为后端派生的只读字段
  version_no?: string;
  latest_version_no?: string;
  needs_update?: boolean;
}

export interface PmRoadmapGuide {
  project_id: string;
  session_id: string;
  batch_id?: string;
  batch_name?: string;
  plan_type?: PmPlanType;
  current_step?: number;
  draft?: PmRoadmapDraft | null;
}

export interface PmPlanSnapshot {
  batch_id: string;
  snapshot_knowledge_id: string;
  roadmap_knowledge_id: string;
  snapshot_at: string | null;
  generated_at: string | null;
}

// ===== 请求体 =====

export interface CreatePmProjectInput {
  name: string;
  description?: string;
  is_new?: boolean;
}
export interface UpdatePmProjectInput {
  name?: string;
  description?: string;
}
export interface CreatePlanBatchInput {
  batch_name: string;
  plan_type: PmPlanType;
  version_id?: string;
}
export interface CreatePmRequirementInput {
  title: string;
  description?: string;
  priority?: PmPriority;
  source?: PmRequirementSource;
  proposer?: string;
  owner_id?: string;
  ai_analysis?: Record<string, unknown>;
}
export interface UpdatePmRequirementInput {
  title?: string;
  description?: string;
  priority?: PmPriority;
  source?: PmRequirementSource;
  proposer?: string;
  owner_id?: string;
  status?: PmRequirementStatus;
  // AI 分析结果与设计方案，用于编辑态或非分析中状态的回写
  ai_analysis?: Record<string, unknown>;
  design_plan?: string;
}
export interface CreatePmVersionInput {
  name: string;
  release_note?: string;
  reviewers?: string;
  owner_id?: string;
  // 日期统一 yyyy-MM-dd，空串表示清空
  plan_date?: string;
  plan_launch_date?: string;
  requirement_ids?: string[];
}
export interface UpdatePmVersionInput {
  name?: string;
  release_note?: string;
  reviewers?: string;
  owner_id?: string;
  plan_date?: string;
  plan_launch_date?: string;
  status?: PmVersionStatus;
}
export interface ReviewPmVersionInput {
  conclusion: PmReviewConclusion;
  opinions?: Record<string, unknown>;
  reviewers?: string;
}
export interface FinalizePmVersionInput {
  // 人工指定版本号，空串则按 minor 自动递增
  version_no?: string;
  update_outline?: string;
}
export interface UpdateReviewOpinionInput {
  round: number;
  index: number;
  // 意见处理状态：pending 待处理 / resolved 已解决
  status: 'pending' | 'resolved';
  note?: string;
}
export interface ChangeVersionRequirementInput {
  priority?: PmPriority;
  // 空串表示保持原描述
  description?: string;
}
export interface CreatePmFeatureInput {
  project_id: string;
  module_name: string;
  feature_name?: string;
  operation_name?: string;
  parent_id?: string;
  requirement_id?: string;
  version_id?: string;
}
export interface CreatePmMaterialInput {
  project_id: string;
  title: string;
  material_type: PmMaterialType;
  // 仅允许已定稿及以上版本
  version_id: string;
}

// ===== 纯函数（置于网络层之前，便于 Node 下单测）=====

export interface PmRequirementFilter {
  status?: string;
  priority?: string;
  source?: string;
  value_achieved?: string;
}

// 需求列表查询串：只拼接非空筛选项，全部为空时返回空串
export function buildRequirementQuery(filter: PmRequirementFilter = {}): string {
  const qs = new URLSearchParams();
  for (const key of ['status', 'priority', 'source', 'value_achieved'] as const) {
    const value = (filter[key] ?? '').trim();
    if (value) qs.set(key, value);
  }
  const query = qs.toString();
  return query ? `?${query}` : '';
}

// ===== 网络层（动态加载 request，避免浏览器依赖污染 Node 单测）=====

const BASE = '/api/v1/pm';

async function loadRequest() {
  return import('../../utils/request');
}

// ---- 项目 ----
export async function listPmProjects(status?: PmProjectStatus) {
  const { get } = await loadRequest();
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  return get<{ data: PmProject[] }>(`${BASE}/projects${qs}`).then((r) => r.data);
}
export async function createPmProject(input: CreatePmProjectInput) {
  const { post } = await loadRequest();
  return post<{ data: PmProject }>(`${BASE}/projects`, input).then((r) => r.data);
}
export async function getPmProject(id: string) {
  const { get } = await loadRequest();
  return get<{ data: PmProject }>(`${BASE}/projects/${id}`).then((r) => r.data);
}
export async function updatePmProject(id: string, input: UpdatePmProjectInput) {
  const { patch } = await loadRequest();
  return patch<{ data: PmProject }>(`${BASE}/projects/${id}`, input).then((r) => r.data);
}
// 删除项目：后端会级联清理项目下的规划、需求、版本、功能与资料
export async function deletePmProject(id: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/projects/${id}`);
}

// ---- 功能规划批次 ----
export async function listPlanBatches(projectId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmPlanBatch[] }>(`${BASE}/projects/${projectId}/plan-batches`).then((r) => r.data);
}
export async function createPlanBatch(projectId: string, input: CreatePlanBatchInput) {
  const { post } = await loadRequest();
  return post<{ data: PmPlanBatch }>(`${BASE}/projects/${projectId}/plan-batches`, input).then((r) => r.data);
}
export async function getPlanBatch(batchId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmPlanBatch }>(`${BASE}/plan-batches/${batchId}`).then((r) => r.data);
}
// 保存引导草稿（5 步过程中逐步落库）
export async function savePlanDraft(batchId: string, draft: PmRoadmapDraft, currentStep = 1): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/plan-batches/${batchId}/draft`, { draft, current_step: currentStep });
}
// 生成功能清单：后端当前同步完成，仍返回任务对象供后续异步化复用
export async function generatePlanBatch(batchId: string, syncFeatures = true) {
  const { post } = await loadRequest();
  return post<{ data: PmTask }>(`${BASE}/plan-batches/${batchId}/generate`, { sync_features: syncFeatures })
    .then((r) => r.data);
}
export async function activatePlanBatch(batchId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/plan-batches/${batchId}/activate`, {});
}
// 删除规划批次：仅草稿批次可删，后端会二次校验状态
export async function deletePlanBatch(batchId: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/plan-batches/${batchId}`);
}
export async function getPlanSnapshot(batchId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmPlanSnapshot }>(`${BASE}/plan-batches/${batchId}/snapshot`).then((r) => r.data);
}
// 引导页续接：返回项目会话与当前批次草稿
export async function getRoadmapGuide(projectId: string) {
  const { post } = await loadRequest();
  return post<{ data: PmRoadmapGuide }>(`${BASE}/projects/${projectId}/roadmap/guide`, {}).then((r) => r.data);
}
// 创建功能规划 AI 引导会话：返回隐藏会话 ID 与首轮指令
// 隐藏标记只能由后端写入（客户端描述会被净化），故不走前端自建会话
export async function startPlanGuideSession(batchId: string) {
  const { post } = await loadRequest();
  return post<{ data: { session_id: string; prompt: string } }>(
    `${BASE}/plan-batches/${batchId}/guide-session`, {},
  ).then((r) => r.data);
}

// ---- 需求 ----
export async function listPmRequirements(projectId: string, filter: PmRequirementFilter = {}) {
  const { get } = await loadRequest();
  return get<{ data: PmRequirement[] }>(
    `${BASE}/projects/${projectId}/requirements${buildRequirementQuery(filter)}`,
  ).then((r) => r.data);
}
export async function createPmRequirement(projectId: string, input: CreatePmRequirementInput) {
  const { post } = await loadRequest();
  return post<{ data: PmRequirement }>(`${BASE}/projects/${projectId}/requirements`, input).then((r) => r.data);
}
export async function getPmRequirement(id: string) {
  const { get } = await loadRequest();
  return get<{ data: PmRequirement }>(`${BASE}/requirements/${id}`).then((r) => r.data);
}
// 局部更新（含看板拖拽改状态）
export async function updatePmRequirement(id: string, input: UpdatePmRequirementInput): Promise<void> {
  const { patch } = await loadRequest();
  await patch(`${BASE}/requirements/${id}`, input);
}
// 发起 AI 分析：返回分析会话 ID，前端复用现有 SSE 对话通道
export async function analyzePmRequirement(id: string) {
  const { post } = await loadRequest();
  return post<{ data: { session_id: string } }>(`${BASE}/requirements/${id}/analyze`, {}).then((r) => r.data);
}
// 回写 AI 分析结果：分析仅作辅助，不限制需求状态；设计方案的 Markdown 单独存放
export async function savePmRequirementAnalysis(
  id: string, aiAnalysis: Record<string, unknown>, designPlan = '',
): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/requirements/${id}/analysis`, { ai_analysis: aiAnalysis, design_plan: designPlan });
}
// 重新激活：仅 rejected / closed 可调用，重置为待分析
export async function reactivatePmRequirement(id: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/requirements/${id}/reactivate`, {});
}
// 删除需求：后端会同时移除版本关联并软删附件
export async function deletePmRequirement(id: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/requirements/${id}`);
}
// 上线验证回填：仅 launched 可调用，重复提交视为修正并刷新回填时间
export async function verifyPmRequirementLaunch(
  id: string, valueAchieved: PmValueAchieved, effectNotes = '', retroNotes = '',
): Promise<PmRequirement> {
  const { post } = await loadRequest();
  return post<{ data: PmRequirement }>(`${BASE}/requirements/${id}/launch-verification`, {
    value_achieved: valueAchieved,
    effect_notes: effectNotes,
    retro_notes: retroNotes,
  }).then((r) => r.data);
}

// ---- 需求附件 ----
// 上传单个附件：multipart/form-data，字段名 file；后端校验大小与类型
export async function uploadPmRequirementAttachment(
  requirementId: string, file: File,
): Promise<PmRequirementAttachment> {
  const { postUpload } = await loadRequest();
  const form = new FormData();
  form.append('file', file);
  return postUpload(`${BASE}/requirements/${requirementId}/attachments`, form)
    .then((r) => r.data as PmRequirementAttachment);
}
export async function listPmRequirementAttachments(requirementId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmRequirementAttachment[] }>(`${BASE}/requirements/${requirementId}/attachments`)
    .then((r) => r.data);
}
export async function deletePmRequirementAttachment(requirementId: string, attachmentId: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/requirements/${requirementId}/attachments/${attachmentId}`);
}
// 下载/预览附件：图片与文档均以 Blob 返回，由调用方决定 objectURL 预览或另存
export async function downloadPmRequirementAttachment(
  requirementId: string, attachmentId: string,
): Promise<Blob> {
  const { getDown } = await loadRequest();
  return getDown(`${BASE}/requirements/${requirementId}/attachments/${attachmentId}/download`);
}

// ---- 版本 ----
export async function listPmVersions(projectId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmVersion[] }>(`${BASE}/projects/${projectId}/versions`).then((r) => r.data);
}
export async function createPmVersion(projectId: string, input: CreatePmVersionInput) {
  const { post } = await loadRequest();
  return post<{ data: PmVersion }>(`${BASE}/projects/${projectId}/versions`, input).then((r) => r.data);
}
export async function getPmVersion(id: string) {
  const { get } = await loadRequest();
  return get<{ data: PmVersion }>(`${BASE}/versions/${id}`).then((r) => r.data);
}
// PATCH 返回变更后的最新版本（状态变更后后端回读）
export async function updatePmVersion(id: string, input: UpdatePmVersionInput) {
  const { patch } = await loadRequest();
  return patch<{ data: PmVersion }>(`${BASE}/versions/${id}`, input).then((r) => r.data);
}
export async function reviewPmVersion(id: string, input: ReviewPmVersionInput) {
  const { post } = await loadRequest();
  return post<{ data: PmVersion }>(`${BASE}/versions/${id}/review`, input).then((r) => r.data);
}
export async function finalizePmVersion(id: string, input: FinalizePmVersionInput = {}) {
  const { post } = await loadRequest();
  return post<{ data: PmVersion }>(`${BASE}/versions/${id}/finalize`, input).then((r) => r.data);
}
// 版本内需求集合（按关联顺序返回）
export async function listPmVersionRequirements(versionId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmRequirement[] }>(`${BASE}/versions/${versionId}/requirements`).then((r) => r.data);
}
// 生成 PRD/原型产物：后端异步入队后返回 pending 任务，前端按任务轮询章节进度
// chapters 为 PRD 章节 key，空数组表示生成全部章节
export async function generatePmVersionArtifacts(
  id: string, regenerate = false, chapters: string[] = [],
) {
  const { post } = await loadRequest();
  return post<{ data: PmTask }>(`${BASE}/versions/${id}/generate`, { regenerate, chapters })
    .then((r) => r.data);
}
// 更新某轮评审意见的处理状态与说明
export async function updatePmVersionReviewOpinion(
  id: string, input: UpdateReviewOpinionInput,
): Promise<PmVersion> {
  const { patch } = await loadRequest();
  return patch<{ data: PmVersion }>(`${BASE}/versions/${id}/review-opinions`, input).then((r) => r.data);
}
// 需求移出本版本
export async function removePmVersionRequirement(versionId: string, requirementId: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/versions/${versionId}/requirements/${requirementId}`);
}
// 搁置需求：移出全部版本并置为已拒绝
export async function shelvePmVersionRequirement(
  versionId: string, requirementId: string, reason: string,
): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/versions/${versionId}/requirements/${requirementId}/shelve`, { reason });
}
// 变更需求：字段为空表示保持原值
export async function changePmVersionRequirement(
  versionId: string, requirementId: string, input: ChangeVersionRequirementInput,
): Promise<void> {
  const { patch } = await loadRequest();
  await patch(`${BASE}/versions/${versionId}/requirements/${requirementId}`, input);
}
// 更换需求：用同项目其他「已确认」需求替换集合中的位置
export async function replacePmVersionRequirement(
  versionId: string, requirementId: string, newRequirementId: string,
): Promise<void> {
  const { post } = await loadRequest();
  await post(`${BASE}/versions/${versionId}/requirements/${requirementId}/replace`, {
    new_requirement_id: newRequirementId,
  });
}

// ---- 功能清单 ----
export async function listPmFeatures(projectId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmFeatureNode[] }>(`${BASE}/projects/${projectId}/features`).then((r) => r.data);
}
export async function createPmFeature(input: CreatePmFeatureInput) {
  const { post } = await loadRequest();
  return post<{ data: PmFeature }>(`${BASE}/features`, input).then((r) => r.data);
}
export async function listPmFeatureIterations(featureId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmFeatureIteration[] }>(`${BASE}/features/${featureId}/iterations`).then((r) => r.data);
}

// ---- 项目资料 ----
// 列表包含已归档记录，页面按 archived 自行区分「当前资料」与「历史归档」
export async function listPmMaterials(projectId: string, type?: PmMaterialType) {
  const { get } = await loadRequest();
  const qs = type ? `?type=${encodeURIComponent(type)}` : '';
  return get<{ data: PmMaterial[] }>(`${BASE}/projects/${projectId}/materials${qs}`).then((r) => r.data);
}
export async function getPmMaterial(id: string) {
  const { get } = await loadRequest();
  return get<{ data: PmMaterial }>(`${BASE}/materials/${id}`).then((r) => r.data);
}
export async function createPmMaterial(input: CreatePmMaterialInput) {
  const { post } = await loadRequest();
  return post<{ data: PmMaterial }>(`${BASE}/materials`, input).then((r) => r.data);
}
// 生成草稿：异步任务，前端复用统一任务轮询
export async function generatePmMaterial(id: string) {
  const { post } = await loadRequest();
  return post<{ data: PmTask }>(`${BASE}/materials/${id}/generate`, {}).then((r) => r.data);
}
// 发布：同类型旧发布版由后端归档保留
export async function publishPmMaterial(id: string) {
  const { post } = await loadRequest();
  return post<{ data: PmMaterial }>(`${BASE}/materials/${id}/publish`, {}).then((r) => r.data);
}
// 更新到最新版本：按最新已定稿版本复制出新草稿并异步生成
export async function updatePmMaterialToLatest(id: string) {
  const { post } = await loadRequest();
  return post<{ data: PmTask }>(`${BASE}/materials/${id}/update`, {}).then((r) => r.data);
}
// 下载导出文件：手册/宣传册为 docx，PPT 为 pptx；空正文由后端返回错误
export async function downloadPmMaterial(id: string): Promise<Blob> {
  const { getDown } = await loadRequest();
  return getDown(`${BASE}/materials/${id}/download`);
}
// 删除草稿资料；已发布/归档记录后端拒绝，调用方需先确认
export async function deletePmMaterial(id: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`${BASE}/materials/${id}`);
}

// ---- 异步任务 ----
// 任务轮询：前端统一 3s 间隔，终态即停
export async function getPmTask(taskId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmTask }>(`${BASE}/tasks/${taskId}`).then((r) => r.data);
}
