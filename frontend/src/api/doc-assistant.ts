import { get, post, put, del, getDown } from '@/utils/request'

// ---------------------------------------------------------------------------
// 文档助手（Doc Assistant）API 封装。全部接口位于 /api/v1/doc-assistant，
// 鉴权/租户头由 utils/request 统一处理。资源为个人私有（tenant + owner 隔离）。
// ---------------------------------------------------------------------------

export interface DocTemplateGlobalConfig {
  default_tone?: string
  target_audience?: string
  default_extra_requirement?: string
  top_k?: number
}

export interface DocTemplateChapter {
  title: string
  instruction?: string
  max_words?: number
  must_reference?: boolean
  skippable?: boolean
}

export interface DocTemplate {
  id: string
  tenant_id: number
  owner_id: string
  name: string
  description?: string
  global_config?: DocTemplateGlobalConfig
  chapters: DocTemplateChapter[]
  created_at: string
  updated_at: string
}

export interface DocTemplatePayload {
  name: string
  description?: string
  global_config?: DocTemplateGlobalConfig
  chapters: DocTemplateChapter[]
}

export type DocTaskStatus =
  | 'pending'
  | 'running'
  | 'success'
  | 'partial_success'
  | 'failed'
  | 'cancelled'

export type DocChapterStatus =
  | 'pending'
  | 'retrieving'
  | 'generating'
  | 'done'
  | 'failed'
  | 'skipped'

export interface DocChapterProgress {
  no: number
  title: string
  status: DocChapterStatus
}

export interface DocGenerationTask {
  id: string
  template_id: string
  template_snapshot?: DocTemplate | null
  kb_ids: string[]
  model_id?: string
  theme: string
  extra_requirement?: string
  status: DocTaskStatus
  progress?: { chapters?: DocChapterProgress[] }
  error?: string
  created_at: string
  finished_at?: string | null
}

export interface DocGenerationPayload {
  template_id?: string
  kb_ids: string[]
  model_id?: string
  theme: string
  extra_requirement?: string
}

export interface DocReference {
  knowledge_id: string
  title: string
  filename?: string
  chunk_ids?: string[]
}

export interface DocChapter {
  no: number
  title: string
  content: string
  references?: DocReference[]
  status: DocChapterStatus
  message?: string
}

export type DocDocumentStatus = 'generated' | 'edited'

export interface DocDocument {
  id: string
  task_id: string
  version: number
  status: DocDocumentStatus
  chapters: DocChapter[]
  created_at: string
}

// ---- 模板 ----

export function createDocTemplate(payload: DocTemplatePayload) {
  return post<{ success: boolean; data: DocTemplate }>('/api/v1/doc-assistant/templates', payload)
}

export function listDocTemplates() {
  return get<{ success: boolean; data: DocTemplate[] }>('/api/v1/doc-assistant/templates')
}

export function getDocTemplate(id: string) {
  return get<{ success: boolean; data: DocTemplate }>(
    `/api/v1/doc-assistant/templates/${encodeURIComponent(id)}`,
  )
}

export function updateDocTemplate(id: string, payload: DocTemplatePayload) {
  return put<{ success: boolean; data: DocTemplate }>(
    `/api/v1/doc-assistant/templates/${encodeURIComponent(id)}`,
    payload,
  )
}

export function deleteDocTemplate(id: string) {
  return del<{ success: boolean }>(`/api/v1/doc-assistant/templates/${encodeURIComponent(id)}`)
}

// ---- 生成任务 ----

export function createDocGenerationTask(payload: DocGenerationPayload) {
  return post<{ success: boolean; data: DocGenerationTask }>('/api/v1/doc-assistant/generate', payload)
}

export function listDocTasks() {
  return get<{ success: boolean; data: DocGenerationTask[] }>('/api/v1/doc-assistant/tasks')
}

export function getDocTask(id: string) {
  return get<{ success: boolean; data: DocGenerationTask }>(
    `/api/v1/doc-assistant/tasks/${encodeURIComponent(id)}`,
  )
}

export function deleteDocTask(id: string) {
  return del<{ success: boolean }>(`/api/v1/doc-assistant/tasks/${encodeURIComponent(id)}`)
}

// ---- 文档 ----

/** 某任务下的全部版本，按版本号倒序（新版本在前）。 */
export function listDocDocuments(taskId: string) {
  return get<{ success: boolean; data: DocDocument[] }>(
    `/api/v1/doc-assistant/tasks/${encodeURIComponent(taskId)}/documents`,
  )
}

export function getDocDocument(id: string) {
  return get<{ success: boolean; data: DocDocument }>(
    `/api/v1/doc-assistant/documents/${encodeURIComponent(id)}`,
  )
}

export function deleteDocDocument(id: string) {
  return del<{ success: boolean }>(`/api/v1/doc-assistant/documents/${encodeURIComponent(id)}`)
}

/** 重新生成单章，返回新版本文档。 */
export function regenerateDocChapter(docId: string, chapterNo: number) {
  return post<{ success: boolean; data: DocDocument }>(
    `/api/v1/doc-assistant/documents/${encodeURIComponent(docId)}/chapters/${chapterNo}/regenerate`,
    {},
  )
}

/** 手动编辑单章内容（续写），返回新版本文档。 */
export function updateDocChapter(docId: string, chapterNo: number, content: string) {
  return put<{ success: boolean; data: DocDocument }>(
    `/api/v1/doc-assistant/documents/${encodeURIComponent(docId)}/chapters/${chapterNo}`,
    { content },
  )
}

// ---- 导出 ----

export function exportDocMarkdown(docId: string) {
  return getDown(`/api/v1/doc-assistant/documents/${encodeURIComponent(docId)}/export/markdown`)
}

export function exportDocWord(docId: string) {
  return getDown(`/api/v1/doc-assistant/documents/${encodeURIComponent(docId)}/export/word`)
}
