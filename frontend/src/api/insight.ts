/**
 * 洞察 API 客户端。
 *
 * 约束与工作台一致：组件不拼 URL；owner 由服务端从上下文取；
 * 响应统一为 { success, data }，这里直接解包成业务对象。
 */
import { del, get, post, postUpload, put } from '@/utils/request'

const BASE = '/api/v1/workbench/insights'

export interface InsightDimension {
  name: string
  criteria: string
  scale: string
}

export interface InsightTheme {
  id: string
  name: string
  description: string
  builtin: boolean
  sensitive: boolean
  glyph: string
  instructions: string
  dimensions: InsightDimension[]
  report_count: number
  last_run_at: string | null
  running: boolean
  /** 上次报告之后有更新的文档数；仅用于提醒，不代表一定未覆盖。 */
  updated_docs: number
  created_at: string
  updated_at: string
}

export interface InsightThemeStats {
  report_count: number
  doc_count: number
}

export interface InsightThemeList {
  themes: InsightTheme[]
  stats: InsightThemeStats
}

export interface InsightCheckup {
  archived: number
  analyzable: number
  too_short: number
  empty: number
  attachment: number
  duplicate: number
  sample_size: number
  folders: string[]
}

export type InsightRunStatus =
  | 'pending'
  | 'stage1'
  | 'waiting_confirm'
  | 'stage2'
  | 'done'
  | 'failed'
  | 'cancelled'

export interface InsightRun {
  id: string
  theme_id: string
  status: InsightRunStatus
  stage: string
  progress: number
  docs_total: number
  docs_processed: number
  evidence_count: number
  message: string
  error: string
  created_at: string
  finished_at: string | null
}

export interface InsightEvidence {
  id: string
  run_id: string
  knowledge_id: string
  knowledge_title: string
  chunk_index: number
  dimension: string
  quote: string
  evidence_type: 'evidence' | 'inference'
  confidence: string
  excluded: boolean
  created_at: string
}

export interface InsightReportSummary {
  id: string
  theme_id: string
  run_id: string
  title: string
  created_at: string
  docs_covered: number
  evidence_count: number
  model_id: string
}

export interface InsightFinding {
  title: string
  detail: string
  kind: 'evidence' | 'inference'
  confidence: string
  refs: string[]
}

export interface InsightScore {
  name: string
  value: number
  label: string
}

/** 主题报告的结构化正文（由模型按固定骨架输出后解析而来）。 */
export interface InsightReportContent {
  coverage_note: string
  findings: InsightFinding[]
  evidence_groups: { quote: string; source: string }[]
  counterpoints: string[]
  scores: InsightScore[]
  conclusion: string
  conclusion_basis: string
  actions: string[]
}

export interface InsightReportDetail extends InsightReportSummary {
  theme_name: string
  content: InsightReportContent
}

/** 报告追问的一条会话消息。 */
export interface InsightReportMessage {
  id: string
  report_id: string
  role: 'user' | 'assistant'
  content: string
  refs: string[] | null
  created_at: string
}

/** 自我洞悉追问的一条会话消息。 */
export interface InsightSelfMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

export interface InsightSelfGate {
  required: number
  completed: number
  missing: string[]
}

export interface InsightSelfDim {
  name: string
  status: 'evidence' | 'partial' | 'inference' | 'blank'
  text: string
  source: string
}

export interface InsightSelfLayer {
  no: number | string
  name: string
  question: string
  coverage: string
  lead: string
  dims: InsightSelfDim[]
}

export interface InsightSelfOverview {
  key: string
  title: string
  text: string
  link: string
}

export interface InsightSelfContent {
  portrait: string
  overview: InsightSelfOverview[]
  layers: InsightSelfLayer[]
  cross: { title: string; text: string }[]
  trios: { trait: string; situation: string; cost: string }[]
  next: { title: string; text: string }[]
}

export interface InsightSelf {
  gate: InsightSelfGate
  status: 'idle' | 'generating' | 'ready' | 'failed'
  updated_at: string | null
  error: string
  model_id: string
  content: InsightSelfContent | null
}

/** 导出层脱敏设置；只作用于复制 / 导出 / 存为文档。 */
export interface InsightPrivacy {
  persons: string[]
  organizations: string[]
  projects: string[]
  hide_evidence_quotes: boolean
}

export interface CreateThemePayload {
  name: string
  description: string
  sensitive: boolean
  instructions: string
  dimensions: InsightDimension[]
}

export interface StartRunPayload {
  sample: boolean
  max_docs: number
  folder_path?: string
  recent_days?: number
  /** 为 true 时证据抽取完成后直接生成报告，不再等待手动确认。 */
  skip_evidence_confirm?: boolean
}

/** 一次分析的范围筛选；零值表示全部归档。 */
export interface InsightScope {
  folder_path: string
  recent_days: number
}

export function listInsightThemes(): Promise<InsightThemeList> {
  return get<{ success: boolean; data: InsightThemeList }>(`${BASE}/themes`).then((r) => r.data)
}

export function createInsightTheme(payload: CreateThemePayload): Promise<InsightTheme> {
  return post<{ success: boolean; data: InsightTheme }>(`${BASE}/themes`, payload).then((r) => r.data)
}

export function updateInsightTheme(id: string, payload: CreateThemePayload): Promise<InsightTheme> {
  return put<{ success: boolean; data: InsightTheme }>(`${BASE}/themes/${id}`, payload).then((r) => r.data)
}

export function deleteInsightTheme(id: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/themes/${id}`).then(() => undefined)
}

export function getInsightCheckup(
  themeId: string,
  scope?: Partial<InsightScope>,
): Promise<InsightCheckup> {
  const query = new URLSearchParams()
  if (scope?.folder_path) query.set('folder_path', scope.folder_path)
  if (scope?.recent_days) query.set('recent_days', String(scope.recent_days))
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return get<{ success: boolean; data: InsightCheckup }>(
    `${BASE}/themes/${themeId}/checkup${suffix}`,
  ).then((r) => r.data)
}

export function listInsightReports(themeId: string): Promise<InsightReportSummary[]> {
  return get<{ success: boolean; data: InsightReportSummary[] }>(
    `${BASE}/themes/${themeId}/reports`,
  ).then((r) => r.data)
}

export function listInsightRuns(themeId: string): Promise<InsightRun[]> {
  return get<{ success: boolean; data: InsightRun[] }>(`${BASE}/themes/${themeId}/runs`).then(
    (r) => r.data,
  )
}

/** 上传 .md / .zip 方法包，返回解析后的主题草稿（不落库）。 */
export function importInsightTheme(file: File): Promise<CreateThemePayload> {
  const form = new FormData()
  form.append('file', file)
  return postUpload(`${BASE}/theme-import`, form).then((r) => r.data as CreateThemePayload)
}

export function startInsightRun(themeId: string, payload: StartRunPayload): Promise<InsightRun> {
  return post<{ success: boolean; data: InsightRun }>(`${BASE}/themes/${themeId}/runs`, payload).then(
    (r) => r.data,
  )
}

export function getInsightRun(runId: string): Promise<InsightRun> {
  return get<{ success: boolean; data: InsightRun }>(`${BASE}/runs/${runId}`).then((r) => r.data)
}

export function cancelInsightRun(runId: string): Promise<void> {
  return post<{ success: boolean }>(`${BASE}/runs/${runId}/cancel`).then(() => undefined)
}

/** 删除一次运行及其证据；报告保留可读。 */
export function deleteInsightRun(runId: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/runs/${runId}`).then(() => undefined)
}

export function listInsightEvidence(
  runId: string,
): Promise<{ items: InsightEvidence[]; total: number; excluded: number }> {
  return get<{ success: boolean; data: { items: InsightEvidence[]; total: number; excluded: number } }>(
    `${BASE}/runs/${runId}/evidence`,
  ).then((r) => r.data)
}

export function excludeInsightEvidence(runId: string, ids: string[], excluded: boolean): Promise<void> {
  return post<{ success: boolean }>(`${BASE}/runs/${runId}/evidence/exclude`, { ids, excluded }).then(
    () => undefined,
  )
}

export function confirmInsightRun(runId: string): Promise<void> {
  return post<{ success: boolean }>(`${BASE}/runs/${runId}/confirm`).then(() => undefined)
}

export function getInsightReport(reportId: string): Promise<InsightReportDetail> {
  return get<{ success: boolean; data: InsightReportDetail }>(`${BASE}/reports/${reportId}`).then(
    (r) => r.data,
  )
}

export function deleteInsightReport(reportId: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/reports/${reportId}`).then(() => undefined)
}

export function listInsightReportMessages(reportId: string): Promise<InsightReportMessage[]> {
  return get<{ success: boolean; data: { items: InsightReportMessage[] } }>(
    `${BASE}/reports/${reportId}/messages`,
  ).then((r) => r.data.items ?? [])
}

/** 报告追问：服务端只使用这份报告与它的证据集作答。 */
export function askInsightReport(reportId: string, question: string): Promise<string> {
  return post<{ success: boolean; data: { answer: string } }>(`${BASE}/reports/${reportId}/ask`, {
    question,
  }).then((r) => r.data.answer)
}

export function getInsightSelf(): Promise<InsightSelf> {
  return get<{ success: boolean; data: InsightSelf }>(`${BASE}/self`).then((r) => r.data)
}

export function generateInsightSelf(): Promise<void> {
  return post<{ success: boolean }>(`${BASE}/self/generate`).then(() => undefined)
}

export function getInsightPrivacy(): Promise<InsightPrivacy> {
  return get<{ success: boolean; data: InsightPrivacy }>(`${BASE}/privacy`).then((r) => r.data)
}

export function updateInsightPrivacy(payload: InsightPrivacy): Promise<InsightPrivacy> {
  return put<{ success: boolean; data: InsightPrivacy }>(`${BASE}/privacy`, payload).then(
    (r) => r.data,
  )
}

export function listInsightSelfMessages(): Promise<InsightSelfMessage[]> {
  return get<{ success: boolean; data: { items: InsightSelfMessage[] } }>(
    `${BASE}/self/messages`,
  ).then((r) => r.data.items ?? [])
}

/** 自我洞悉追问：服务端只使用当前说明书与主题报告摘要作答。 */
export function askInsightSelf(question: string): Promise<string> {
  return post<{ success: boolean; data: { answer: string } }>(`${BASE}/self/ask`, {
    question,
  }).then((r) => r.data.answer)
}
