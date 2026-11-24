// SOP 工作台的类型镜像与 REST 客户端。
//
// 类型区逐字对齐后端 DTO（internal/types/sop_*.go、handler 内联结构、
// service/sop/approval_aggregate.go）；字段名即前后端契约，不得改名。
// 函数区返回**解包后的 data**（后端包络为 { success, data }）。
import { del, get, patch, post, postUpload, put } from '@/utils/request';

export const SOP_BASE = '/api/v1/sop';

// ---- 枚举 ----

export type ItemStatus =
  | 'backlog'
  | 'todo'
  | 'in_progress'
  | 'in_review'
  | 'done'
  | 'closed'
  | 'blocked'
  | 'cancelled';

export type RunStatus =
  | 'queued'
  | 'running'
  | 'waiting'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'paused';

export type NodeStatus =
  | 'pending'
  | 'running'
  | 'waiting_approval'
  | 'pending_input'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'skipped';

export type ExecState = 'none' | 'running' | 'intervene' | 'pr' | 'closed';

export type CliKind =
  | 'opencode'
  | 'cursor-agent'
  | 'agy'
  | 'kimi'
  | 'pi'
  | 'dsh'
  | 'zcode'
  | 'claude'
  | 'codebuddy'
  | 'codex';

export const CLI_KINDS: CliKind[] = [
  'opencode', 'cursor-agent', 'agy', 'kimi', 'pi',
  'dsh', 'zcode', 'claude', 'codebuddy', 'codex',
];

export type TemplateStatus = 'draft' | 'published' | 'archived';
export type NodeType = 'agent' | 'script' | 'approval' | 'sop';
export type ExecutorKind = 'platform_agent' | 'cli';
export type OnFailure = 'loop' | 'reject' | 'fail' | 'human';
export type ArtifactCheckType = 'exists' | 'contains' | 'command';

// ---- 项目 ----

export interface SopRepo {
  id: string;
  name: string;
  url: string;
  branch: string;
  default: boolean;
}

export interface SopProject {
  id: string;
  owner_user_id: string;
  name: string;
  description: string;
  code: string;
  next_item_no: number;
  kb_id: string;
  kb_owner: string;
  brief_knowledge_id: string;
  brief_rendered_seq: number;
  default_template_id: string;
  default_executor: string;
  on_run_success_item_status: string;
  notify_on_gate_or_fail: boolean;
  workspace_root: string;
  repos: SopRepo[];
  status: string;
  domain_data: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface SopProjectInput {
  name?: string;
  description?: string;
  code?: string;
  repos?: SopRepo[];
  kb_id?: string;
  default_template_id?: string;
  default_executor?: string;
  on_run_success_item_status?: string;
  notify_on_gate_or_fail?: boolean;
  workspace_root?: string;
}

// ---- 事项 ----

export interface SopItem {
  id: string;
  project_id: string;
  no: string;
  title: string;
  description: string;
  kind: string;
  priority: string;
  status: ItemStatus;
  source: string;
  proposer: string;
  owner: string;
  tags: string[];
  template_id: string;
  kb_id: string;
  knowledge_id: string;
  outcome: Record<string, unknown>;
  domain_data: Record<string, unknown>;
  last_run_id: string;
  is_big: boolean;
  parent_id: string;
  split_source: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface SopItemWithDerived extends SopItem {
  current_node: string;
  progress_done: number;
  progress_total: number;
  exec_state: ExecState;
}

export interface SopItemAttachment {
  id: string;
  item_id: string;
  name: string;
  media_type: string;
  size: number;
  text_content: string;
  created_at: string;
}

export interface SopItemClarification {
  id: string;
  item_id: string;
  question: string;
  answer: string;
  status: 'open' | 'answered';
  source: string;
  created_at: string;
  answered_at: string | null;
}

export interface SopItemActivity {
  id: string;
  item_id: string;
  kind: 'business' | 'audit';
  actor: string;
  action: string;
  detail: string;
  created_at: string;
}

export interface SopItemFilter {
  keyword?: string;
  status?: string;
  owner?: string;
  source?: string;
  kind?: string;
  exec_state?: string;
  parent_id?: string;
  page?: number;
  page_size?: number;
}

export interface SopItemInput {
  title?: string;
  description?: string;
  kind?: string;
  priority?: string;
  source?: string;
  proposer?: string;
  owner?: string;
  tags?: string[];
  template_id?: string;
  kb_id?: string;
  outcome?: Record<string, unknown>;
  domain_data?: Record<string, unknown>;
  is_big?: boolean;
  parent_id?: string;
}

export interface SopBulkResult {
  updated: string[];
  skipped: string[];
}

// ---- 模板与定义 ----

export interface SopPosition {
  x: number;
  y: number;
}

export interface SopNodeExecutor {
  kind: ExecutorKind | string;
  agent_id?: string;
  cli_kind?: string;
  model?: string;
  credential_id?: string;
  role_id?: string | null;
  kb_scope?: string;
  kb_ids?: string[];
}

export interface SopArtifactCheck {
  type: ArtifactCheckType | string;
  keyword?: string;
  command?: string;
}

export interface SopArtifactOutput {
  name: string;
  path: string;
  media_type: string;
  required: boolean;
  check: SopArtifactCheck;
}

export interface SopNode {
  id: string;
  name: string;
  type: NodeType | string;
  executor: SopNodeExecutor;
  task_prompt: string;
  task_refs: string[];
  extra_rules: string;
  skills: string[] | null;
  mcp: string[] | null;
  artifact_outputs: SopArtifactOutput[];
  gate_commands: string[];
  gate_extra: string;
  on_failure: OnFailure | string;
  reject_to: string;
  max_attempts: number;
  max_reworks: number;
  loop_max_iterations: number;
  carry_over: boolean;
  serial_group: string;
  timeout_minutes: number;
  position: SopPosition;
  script?: string;
  credential_env?: Record<string, string>;
  approver_hint?: string;
  notification?: string;
  approval_timeout_hours?: number;
  approval_timeout_action?: string;
  auto_ingest?: boolean;
  child_template_id?: string;
}

export interface SopEdge {
  id: string;
  source: string;
  target: string;
  when: string;
}

export interface SopDefinition {
  nodes: SopNode[];
  edges: SopEdge[];
  input_schema: string[];
  on_success_item_status: string;
}

/** 编排/画布操作（后端 NodeOp 的客户端形状；边同时接受 source/target 与 from/to）。 */
export interface SopNodeOp {
  op: 'set' | 'add_node' | 'remove_node' | 'add_edge' | 'remove_edge' | string;
  node_id?: string;
  node?: Partial<SopNode> & { id: string };
  fields?: Record<string, unknown>;
  edge?: { source?: string; target?: string; from?: string; to?: string; id?: string };
}

export interface SopTemplate {
  id: string;
  tenant_id: number;
  project_id: string;
  name: string;
  description: string;
  status: TemplateStatus | string;
  version: number;
  published_revision_id: string;
  definition: SopDefinition;
  is_builtin: boolean;
  metric_category: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface SopTemplateRevision {
  id: string;
  tenant_id: number;
  template_id: string;
  revision: number;
  definition: SopDefinition;
  published_by: string;
  created_at: string;
}

export interface SopTemplateInput {
  project_id?: string;
  name?: string;
  description?: string;
  metric_category?: string;
  definition?: SopDefinition;
}

// ---- 运行 ----

export interface SopUsage {
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
}

export interface SopHumanContextEntry {
  at: string;
  source: string;
  node_id: string;
  text: string;
}

export interface SopRun {
  id: string;
  tenant_id: number;
  template_id: string;
  revision_id: string;
  workflow_snapshot: SopDefinition;
  project_id: string;
  item_id: string;
  parent_run_id: string;
  trigger_kind: string;
  trigger_ref_id: string;
  status: RunStatus;
  input: Record<string, unknown>;
  human_context: SopHumanContextEntry[];
  project_context: string;
  workspace_dir: string;
  lease_until: string | null;
  heartbeat_at: string | null;
  cancel_requested: boolean;
  pause_requested: boolean;
  usage: SopUsage;
  execution_duration_ms: number;
  error: string;
  started_at: string | null;
  finished_at: string | null;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface SopNodeRun {
  id: string;
  run_id: string;
  node_id: string;
  status: NodeStatus;
  attempt: number;
  rework_count: number;
  output: string;
  error: string;
  feedback: string;
  gate_result: string;
  gate_reason: string;
  session_id: string;
  resume_session_id: string;
  child_run_id: string;
  usage: SopUsage;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface SopArtifact {
  id: string;
  run_id: string;
  node_id: string;
  attempt: number;
  name: string;
  path: string;
  media_type: string;
  size: number;
  sha256: string;
  kb_id: string;
  knowledge_id: string;
  ingested_at: string | null;
  created_at: string;
}

export interface SopRunDetail {
  run: SopRun;
  node_runs: SopNodeRun[];
  artifacts: SopArtifact[];
  project_context: string;
}

export interface SopRunStats {
  success_rate: number;
  finished_count: number;
  total_count: number;
  total_tokens: number;
  status_counts: Record<string, number>;
}

export interface SopRunFilter {
  project_id?: string;
  item_id?: string;
  status?: string;
  keyword?: string;
  page?: number;
  page_size?: number;
}

export interface SopCreateRunInput {
  template_id: string;
  item_id?: string;
  input?: Record<string, unknown>;
  trigger_kind?: string;
  trigger_ref_id?: string;
}

// ---- 审批中心 ----

export interface ApprovalEntry {
  kind: 'sop' | 'agent_prompt' | 'automation';
  ref_id: string;
  run_id?: string;
  node_id?: string;
  session_id?: string;
  prompt_id?: string;
  title: string;
  project_name?: string;
  item_title?: string;
  reason: string;
  waiting_since: string;
  actions: string[];
}

// ---- 触发配置 ----

export type TriggerMode = 'interval' | 'cron';

export interface SopTriggerConfig {
  id?: string;
  project_id: string;
  mode: TriggerMode | string;
  interval_key: string;
  cron: string;
  timezone: string;
  default_template_id: string;
  prefer_item_template: boolean;
  sources: string;
  statuses: string[];
  per_run_limit: number;
  enabled: boolean;
  last_run_at?: string | null;
  next_run_at?: string | null;
  created_at?: string;
  updated_at?: string;
}

export interface SopTriggerLog {
  id: string;
  config_id: string;
  kind: 'auto' | 'manual' | string;
  scanned: number;
  fired: number;
  skipped: number;
  note: string;
  created_at: string;
}

// ---- CLI 凭据与安装状态 ----

export interface CLICredentialMasked {
  cli_kind: CliKind | string;
  name: string;
  base_url?: string;
  default_model?: string;
  api_key_tail: string;
  updated_at?: string;
}

export interface CLICredentialInput {
  name?: string;
  api_key?: string;
  base_url?: string;
  model?: string;
}

export interface CLIStatus {
  kind: CliKind | string;
  binary: string;
  installed: boolean;
  version?: string;
  guidance?: string;
}

// ---- PM 归档 ----

export interface PMArchiveSummary {
  projects: number;
  items: number;
  runs: number;
  attachments: number;
}

// ---- AI 助手 ----

export interface SopSubItem {
  title: string;
  description: string;
  priority?: string;
}

export interface SopBreakdownInput {
  project_id: string;
  title: string;
  description?: string;
}

export interface SopFlowDraft {
  definition: SopDefinition;
  warnings: string[];
  status: string;
}

export interface SopInventoryItem {
  id: string;
  name: string;
}

export interface SopInventory {
  agents?: SopInventoryItem[];
  clis?: string[];
  models?: SopInventoryItem[];
  skills?: string[];
  mcps?: string[];
}

export interface SopOrchestrateInput {
  template_id?: string;
  mode: 'flow' | 'node';
  instruction: string;
  node_id?: string;
  definition?: SopDefinition;
  inventory?: SopInventory;
}

export interface SopApplyOpsResult {
  definition: SopDefinition;
  applied: string[];
  skipped: { op: string; reason: string }[];
}

export interface SopOpSkip {
  op: string;
  reason: string;
}

/**
 * 编排操作（T25 NodeOp 的客户端视图，命名沿用计划锁定的 OrchestrateOp）。
 * 后端 NodeOp 的增删边用 from_id/to_id；这里统一收敛成 edge 对象，避免两套形状。
 */
export type OrchestrateOp = SopNodeOp & { reason?: string };

/** AI 差异卡（T47 formatDiffCards 产出，命名沿用计划锁定的 DiffCard）。 */
export interface DiffCard {
  nodeId: string;
  nodeName: string;
  field: string;
  from: string;
  to: string;
  reason: string;
}

// ---- 项目 ----

export async function listProjects(): Promise<SopProject[]> {
  return get<{ data: SopProject[] }>(`${SOP_BASE}/projects`).then((r) => r.data);
}

export async function createProject(input: SopProjectInput): Promise<SopProject> {
  return post<{ data: SopProject }>(`${SOP_BASE}/projects`, input).then((r) => r.data);
}

export async function getProject(id: string): Promise<SopProject> {
  return get<{ data: SopProject }>(`${SOP_BASE}/projects/${id}`).then((r) => r.data);
}

export async function updateProject(id: string, input: SopProjectInput): Promise<SopProject> {
  return put<{ data: SopProject }>(`${SOP_BASE}/projects/${id}`, input).then((r) => r.data);
}

export async function deleteProject(id: string): Promise<void> {
  await del(`${SOP_BASE}/projects/${id}`);
}

export async function addRepo(projectId: string, repo: Partial<SopRepo>): Promise<SopRepo> {
  return post<{ data: SopRepo }>(`${SOP_BASE}/projects/${projectId}/repos`, repo).then((r) => r.data);
}

export async function removeRepo(projectId: string, nameOrId: string): Promise<void> {
  await del(`${SOP_BASE}/projects/${projectId}/repos/${encodeURIComponent(nameOrId)}`);
}

export async function setDefaultRepo(projectId: string, nameOrId: string): Promise<void> {
  await put(`${SOP_BASE}/projects/${projectId}/repos/default`, { id: nameOrId });
}

// ---- 事项 ----

export async function listItems(projectId: string, params: SopItemFilter = {}): Promise<SopItemWithDerived[]> {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    qs.set(key, String(value));
  }
  const suffix = qs.toString() ? `?${qs}` : '';
  return get<{ data: SopItemWithDerived[] }>(`${SOP_BASE}/projects/${projectId}/items${suffix}`).then((r) => r.data);
}

export async function createItem(projectId: string, input: SopItemInput): Promise<SopItem> {
  return post<{ data: SopItem }>(`${SOP_BASE}/projects/${projectId}/items`, input).then((r) => r.data);
}

export async function getItem(id: string): Promise<SopItem> {
  return get<{ data: SopItem }>(`${SOP_BASE}/items/${id}`).then((r) => r.data);
}

export async function updateItem(id: string, input: SopItemInput): Promise<SopItem> {
  return put<{ data: SopItem }>(`${SOP_BASE}/items/${id}`, input).then((r) => r.data);
}

export async function deleteItem(id: string): Promise<void> {
  await del(`${SOP_BASE}/items/${id}`);
}

export async function setItemStatus(id: string, status: ItemStatus | string): Promise<SopItem> {
  return patch<{ data: SopItem }>(`${SOP_BASE}/items/${id}/status`, { status }).then((r) => r.data);
}

/** 批量迁移状态（后端唯一批量入口；用于批量关闭与批量推进）。 */
export async function bulkUpdateItemStatus(ids: string[], status: ItemStatus | string): Promise<SopBulkResult> {
  return post<{ data: SopBulkResult }>(`${SOP_BASE}/items/bulk-status`, { ids, status }).then((r) => r.data);
}

export async function addAttachment(itemId: string, file: File): Promise<SopItemAttachment> {
  const form = new FormData();
  form.append('file', file);
  return postUpload(`${SOP_BASE}/items/${itemId}/attachments`, form).then((r) => r.data);
}

export async function listAttachments(itemId: string): Promise<SopItemAttachment[]> {
  return get<{ data: SopItemAttachment[] }>(`${SOP_BASE}/items/${itemId}/attachments`).then((r) => r.data);
}

/** 回填产物结论：后端没有独立 outcome 端点，走事项更新（outcome 是其中一列）。 */
export async function setOutcome(id: string, outcome: Record<string, unknown>): Promise<SopItem> {
  return updateItem(id, { outcome });
}

export async function listClarifications(itemId: string): Promise<SopItemClarification[]> {
  return get<{ data: SopItemClarification[] }>(`${SOP_BASE}/items/${itemId}/clarifications`).then((r) => r.data);
}

export async function addClarification(itemId: string, question: string, source = 'human'): Promise<SopItemClarification> {
  return post<{ data: SopItemClarification }>(`${SOP_BASE}/items/${itemId}/clarifications`, { question, source }).then((r) => r.data);
}

export async function answerClarification(itemId: string, clarificationId: string, answer: string): Promise<void> {
  await patch(`${SOP_BASE}/items/${itemId}/clarifications/${clarificationId}/answer`, { answer });
}

export async function listActivities(itemId: string, kind?: 'business' | 'audit'): Promise<SopItemActivity[]> {
  const suffix = kind ? `?kind=${kind}` : '';
  return get<{ data: SopItemActivity[] }>(`${SOP_BASE}/items/${itemId}/activities${suffix}`).then((r) => r.data);
}

// ---- 模板 ----

export async function listTemplates(projectId?: string): Promise<SopTemplate[]> {
  const suffix = projectId ? `?project_id=${encodeURIComponent(projectId)}` : '';
  return get<{ data: SopTemplate[] }>(`${SOP_BASE}/templates${suffix}`).then((r) => r.data);
}

export async function createTemplate(input: SopTemplateInput): Promise<SopTemplate> {
  return post<{ data: SopTemplate }>(`${SOP_BASE}/templates`, input).then((r) => r.data);
}

export async function getTemplate(id: string): Promise<SopTemplate> {
  return get<{ data: SopTemplate }>(`${SOP_BASE}/templates/${id}`).then((r) => r.data);
}

export async function updateTemplate(id: string, input: SopTemplateInput): Promise<SopTemplate> {
  return put<{ data: SopTemplate }>(`${SOP_BASE}/templates/${id}`, input).then((r) => r.data);
}

export async function deleteTemplate(id: string): Promise<void> {
  await del(`${SOP_BASE}/templates/${id}`);
}

export async function publishTemplate(id: string): Promise<SopTemplateRevision> {
  return post<{ data: SopTemplateRevision }>(`${SOP_BASE}/templates/${id}/publish`, {}).then((r) => r.data);
}

export async function copyTemplate(id: string, name: string): Promise<SopTemplate> {
  return post<{ data: SopTemplate }>(`${SOP_BASE}/templates/${id}/copy`, { name }).then((r) => r.data);
}

export async function listRevisions(templateId: string): Promise<SopTemplateRevision[]> {
  return get<{ data: SopTemplateRevision[] }>(`${SOP_BASE}/templates/${templateId}/revisions`).then((r) => r.data);
}

export async function getRevision(revisionId: string): Promise<SopTemplateRevision> {
  return get<{ data: SopTemplateRevision }>(`${SOP_BASE}/revisions/${revisionId}`).then((r) => r.data);
}

// ---- 运行 ----

export async function listRuns(params: SopRunFilter = {}): Promise<SopRun[]> {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    qs.set(key, String(value));
  }
  const suffix = qs.toString() ? `?${qs}` : '';
  return get<{ data: SopRun[] }>(`${SOP_BASE}/runs${suffix}`).then((r) => r.data);
}

export async function runStats(projectId?: string): Promise<SopRunStats> {
  const suffix = projectId ? `?project_id=${encodeURIComponent(projectId)}` : '';
  return get<{ data: SopRunStats }>(`${SOP_BASE}/runs/stats/summary${suffix}`).then((r) => r.data);
}

export async function getRun(id: string): Promise<SopRunDetail> {
  return get<{ data: SopRunDetail }>(`${SOP_BASE}/runs/${id}`).then((r) => r.data);
}

export async function createRun(input: SopCreateRunInput): Promise<SopRun> {
  return post<{ data: SopRun }>(`${SOP_BASE}/runs`, input).then((r) => r.data);
}

export async function retryRun(id: string): Promise<void> {
  await post(`${SOP_BASE}/runs/${id}/retry`, {});
}

export async function pauseRun(id: string): Promise<void> {
  await post(`${SOP_BASE}/runs/${id}/pause`, {});
}

export async function resumeRun(id: string): Promise<void> {
  await post(`${SOP_BASE}/runs/${id}/resume`, {});
}

export async function cancelRun(id: string): Promise<void> {
  await post(`${SOP_BASE}/runs/${id}/cancel`, {});
}

export async function addHumanContext(runId: string, text: string, nodeId = ''): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/human-context`, { text, node_id: nodeId });
}

export async function approveNode(runId: string, nodeId: string, comment = ''): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/approve`, { comment });
}

export async function rejectNode(runId: string, nodeId: string, comment = '', rejectTo = ''): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/reject`, { comment, reject_to: rejectTo });
}

export async function reworkNode(runId: string, nodeId: string, comment = ''): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/rework`, { comment });
}

export async function terminateNode(runId: string, nodeId: string, comment = ''): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/terminate`, { comment });
}

export async function answerNode(runId: string, nodeId: string, answer: string): Promise<void> {
  await post(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/answer`, { answer });
}

export async function listArtifacts(runId: string): Promise<SopArtifact[]> {
  return get<{ data: SopArtifact[] }>(`${SOP_BASE}/runs/${runId}/artifacts`).then((r) => r.data);
}

/**
 * 产物文件地址。
 *
 * iframe/下载链接无法携带 Authorization 头，所以调用方必须用 axios 以 blob 拉取
 * （带鉴权头）再 URL.createObjectURL；本函数只负责拼出**不含 token** 的地址。
 */
export function artifactFileURL(runId: string, artifactId: string, download = false): string {
  const suffix = download ? '?download=1' : '';
  return `${SOP_BASE}/runs/${runId}/artifacts/${artifactId}/file${suffix}`;
}

export async function getNodeLogs(runId: string, nodeId: string, attempt?: number): Promise<string> {
  const suffix = attempt ? `?attempt=${attempt}` : '';
  // 日志是 text/plain：axios 原样返回字符串，不经 { success, data } 包络。
  return get<string>(`${SOP_BASE}/runs/${runId}/nodes/${nodeId}/logs${suffix}`) as unknown as Promise<string>;
}

export async function ingestArtifact(runId: string, artifactId: string, kbId = ''): Promise<SopArtifact> {
  return post<{ data: SopArtifact }>(`${SOP_BASE}/runs/${runId}/artifacts/${artifactId}/ingest`, { kb_id: kbId })
    .then((r) => r.data);
}

// ---- AI 助手 ----

export async function breakdownItems(input: SopBreakdownInput): Promise<SopSubItem[]> {
  return post<{ data: SopSubItem[] }>(`${SOP_BASE}/ai/breakdown`, input).then((r) => r.data);
}

export async function analyzeItem(itemId: string): Promise<string> {
  return post<{ data: { session_id: string } }>(`${SOP_BASE}/ai/analyze-item`, { item_id: itemId })
    .then((r) => r.data.session_id);
}

export async function generateClarifications(itemId: string): Promise<SopItemClarification[]> {
  return post<{ data: SopItemClarification[] }>(`${SOP_BASE}/ai/clarify-generate`, { item_id: itemId })
    .then((r) => r.data);
}

export async function orchestrate(input: SopOrchestrateInput): Promise<SopFlowDraft | SopApplyOpsResult> {
  return post<{ data: SopFlowDraft | SopApplyOpsResult }>(`${SOP_BASE}/ai/orchestrate`, input).then((r) => r.data);
}

// ---- 管理：审批 / 触发 / 凭据 / 归档 ----

export async function listApprovals(): Promise<ApprovalEntry[]> {
  return get<{ data: ApprovalEntry[] }>(`${SOP_BASE}/approvals`).then((r) => r.data);
}

export async function getTriggerConfig(projectId: string): Promise<SopTriggerConfig> {
  return get<{ data: SopTriggerConfig }>(`${SOP_BASE}/projects/${projectId}/trigger-config`).then((r) => r.data);
}

export async function saveTriggerConfig(projectId: string, config: Partial<SopTriggerConfig>): Promise<SopTriggerConfig> {
  return put<{ data: SopTriggerConfig }>(`${SOP_BASE}/projects/${projectId}/trigger-config`, config).then((r) => r.data);
}

export async function runNow(projectId: string): Promise<void> {
  await post(`${SOP_BASE}/projects/${projectId}/trigger-config/run-now`, {});
}

export async function listTriggerLogs(projectId: string): Promise<SopTriggerLog[]> {
  return get<{ data: SopTriggerLog[] }>(`${SOP_BASE}/projects/${projectId}/trigger-logs`).then((r) => r.data);
}

export async function listCredentials(): Promise<CLICredentialMasked[]> {
  return get<{ data: CLICredentialMasked[] }>(`${SOP_BASE}/cli-credentials`).then((r) => r.data);
}

export async function saveCredential(kind: string, input: CLICredentialInput): Promise<CLICredentialMasked> {
  return put<{ data: CLICredentialMasked }>(`${SOP_BASE}/cli-credentials/${kind}`, input).then((r) => r.data);
}

export async function deleteCredential(kind: string): Promise<void> {
  await del(`${SOP_BASE}/cli-credentials/${kind}`);
}

export async function cliStatus(): Promise<CLIStatus[]> {
  return get<{ data: CLIStatus[] }>(`${SOP_BASE}/cli-status`).then((r) => r.data);
}

export async function pmArchiveSummary(): Promise<PMArchiveSummary> {
  return get<{ data: PMArchiveSummary }>(`${SOP_BASE}/pm-archive/summary`).then((r) => r.data);
}
