export const MODEL_IN_USE_ERROR_CODE = 2300

export const KNOWN_MODEL_USAGE_BINDINGS = [
  'embedding_model',
  'summary_model',
  'question_model',
  'image_processing_model',
  'vlm_model',
  'asr_model',
  'wiki_synthesis_model',
  'auto_tag_model',
  'chat_model',
  'rerank_model',
  'query_understand_model',
  'follow_up_model',
  'extract_model',
] as const

export type KnownModelUsageBinding = typeof KNOWN_MODEL_USAGE_BINDINGS[number]

export interface ModelUsageResource {
  id: string
  name: string
  bindings: string[]
}

export interface ModelUsageDetails {
  knowledge_bases: ModelUsageResource[]
  agents: ModelUsageResource[]
  long_term_memory: {
    bindings: string[]
  }
  knowledge_base_total: number
  agent_total: number
  // 文档级向量引用数（knowledges.embedding_model_id）。
  document_total: number
  // 聊天历史索引是否绑定该 Embedding 模型。
  chat_history_embedding: boolean
}

export class ModelInUseError extends Error {
  readonly details: ModelUsageDetails

  constructor(details: ModelUsageDetails) {
    super('model is in use')
    this.name = 'ModelInUseError'
    this.details = details
  }
}

export interface ModelReassignmentItem {
  binding: string
  model_type: string
  target_model_id: string
  target_model_name: string
  target_source: string
  knowledge_base_count: number
  agent_count: number
  long_term_memory: boolean
}

export interface ModelReassignmentBlocker {
  code: string
  binding?: string
  model_type?: string
  knowledge_base_count?: number
  document_count?: number
}

export interface ModelReassignmentPlan {
  model_id: string
  model_name: string
  can_delete: boolean
  items: ModelReassignmentItem[]
  blockers: ModelReassignmentBlocker[]
  knowledge_base_total: number
  agent_total: number
  long_term_memory: {
    bindings: string[]
  }
  // 会话输入栏记忆中仍选中该模型的会话数。
  session_model_count: number
  // 会话记忆改写目标；为空表示删除后清空选择。
  session_target_model_id?: string
  session_target_model_name?: string
}

export class ModelReassignmentBlockedError extends Error {
  readonly plan: ModelReassignmentPlan

  constructor(plan: ModelReassignmentPlan) {
    super('model reassignment blocked')
    this.name = 'ModelReassignmentBlockedError'
    this.plan = plan
  }
}

type UnknownRecord = Record<string, unknown>

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseBindings(value: unknown): string[] | null {
  if (!Array.isArray(value) || value.some(binding => typeof binding !== 'string' || binding.trim() === '')) {
    return null
  }
  return [...value]
}

function parseResources(value: unknown): ModelUsageResource[] | null {
  if (!Array.isArray(value)) return null

  const resources: ModelUsageResource[] = []
  for (const item of value) {
    if (!isRecord(item) || typeof item.id !== 'string' || item.id.trim() === '' || typeof item.name !== 'string') {
      return null
    }
    const bindings = parseBindings(item.bindings)
    if (!bindings || bindings.length === 0) return null
    resources.push({ id: item.id, name: item.name, bindings })
  }
  return resources
}

function parseTotal(value: unknown, listed: number): number {
  if (typeof value === 'number' && Number.isFinite(value) && value >= listed) {
    return Math.floor(value)
  }
  return listed
}

export function parseModelUsageDetails(value: unknown): ModelUsageDetails | null {
  if (!isRecord(value) || !isRecord(value.long_term_memory)) return null

  const knowledgeBases = parseResources(value.knowledge_bases)
  const agents = parseResources(value.agents)
  const memoryBindings = parseBindings(value.long_term_memory.bindings)
  if (!knowledgeBases || !agents || !memoryBindings) return null

  const details: ModelUsageDetails = {
    knowledge_bases: knowledgeBases,
    agents,
    long_term_memory: { bindings: memoryBindings },
    knowledge_base_total: parseTotal(value.knowledge_base_total, knowledgeBases.length),
    agent_total: parseTotal(value.agent_total, agents.length),
    document_total: parseCount(value.document_total),
    chat_history_embedding: value.chat_history_embedding === true,
  }
  if (
    knowledgeBases.length === 0 &&
    agents.length === 0 &&
    memoryBindings.length === 0 &&
    details.document_total === 0 &&
    !details.chat_history_embedding
  ) {
    return null
  }
  return details
}

export function modelInUseErrorFromRequest(error: unknown): ModelInUseError | null {
  if (!isRecord(error) || !isRecord(error.error) || error.error.code !== MODEL_IN_USE_ERROR_CODE) {
    return null
  }
  const details = parseModelUsageDetails(error.error.details)
  return details ? new ModelInUseError(details) : null
}

function parseCount(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : 0
}

function parseReassignmentItem(value: unknown): ModelReassignmentItem | null {
  if (!isRecord(value)) return null
  if (typeof value.binding !== 'string' || value.binding.trim() === '') return null
  if (typeof value.target_model_id !== 'string' || value.target_model_id.trim() === '') return null
  return {
    binding: value.binding,
    model_type: typeof value.model_type === 'string' ? value.model_type : '',
    target_model_id: value.target_model_id,
    target_model_name: typeof value.target_model_name === 'string' ? value.target_model_name : '',
    target_source: typeof value.target_source === 'string' ? value.target_source : '',
    knowledge_base_count: parseCount(value.knowledge_base_count),
    agent_count: parseCount(value.agent_count),
    long_term_memory: value.long_term_memory === true,
  }
}

function parseReassignmentBlocker(value: unknown): ModelReassignmentBlocker | null {
  if (!isRecord(value) || typeof value.code !== 'string' || value.code.trim() === '') {
    return null
  }
  return {
    code: value.code,
    binding: typeof value.binding === 'string' ? value.binding : undefined,
    model_type: typeof value.model_type === 'string' ? value.model_type : undefined,
    knowledge_base_count: parseCount(value.knowledge_base_count),
    document_count: parseCount(value.document_count),
  }
}

export function parseModelReassignmentPlan(value: unknown): ModelReassignmentPlan | null {
  if (!isRecord(value)) return null
  if (typeof value.model_id !== 'string' || value.model_id.trim() === '') return null
  if (typeof value.can_delete !== 'boolean') return null
  if (!Array.isArray(value.items) || !Array.isArray(value.blockers)) return null
  if (!isRecord(value.long_term_memory)) return null

  const items: ModelReassignmentItem[] = []
  for (const raw of value.items) {
    const item = parseReassignmentItem(raw)
    if (!item) return null
    items.push(item)
  }
  const blockers: ModelReassignmentBlocker[] = []
  for (const raw of value.blockers) {
    const blocker = parseReassignmentBlocker(raw)
    if (!blocker) return null
    blockers.push(blocker)
  }
  const memoryBindings = parseBindings(value.long_term_memory.bindings)
  if (!memoryBindings) return null

  return {
    model_id: value.model_id,
    model_name: typeof value.model_name === 'string' ? value.model_name : '',
    can_delete: value.can_delete,
    items,
    blockers,
    knowledge_base_total: parseCount(value.knowledge_base_total),
    agent_total: parseCount(value.agent_total),
    long_term_memory: { bindings: memoryBindings },
    session_model_count: parseCount(value.session_model_count),
    session_target_model_id:
      typeof value.session_target_model_id === 'string' && value.session_target_model_id.trim() !== ''
        ? value.session_target_model_id
        : undefined,
    session_target_model_name:
      typeof value.session_target_model_name === 'string' && value.session_target_model_name.trim() !== ''
        ? value.session_target_model_name
        : undefined,
  }
}

// 从错误响应中提取改写计划（后端在执行失败时把 plan 放在 details 中）。
export function modelReassignmentPlanFromRequest(error: unknown): ModelReassignmentPlan | null {
  if (!isRecord(error)) return null
  if (isRecord(error.error)) {
    return parseModelReassignmentPlan(error.error.details)
  }
  return parseModelReassignmentPlan(error.details)
}

const knownBindings = new Set<string>(KNOWN_MODEL_USAGE_BINDINGS)

export function modelUsageBindingI18nKey(binding: string): string {
  const key = knownBindings.has(binding) ? binding : 'unknown'
  return `modelSettings.usage.bindings.${key}`
}

export function modelUsageResourceCount(resources: readonly ModelUsageResource[], total?: number): number {
  return typeof total === 'number' && Number.isFinite(total) && total >= resources.length
    ? Math.floor(total)
    : resources.length
}

export function modelUsageListTruncated(resources: readonly ModelUsageResource[], total?: number): boolean {
  return modelUsageResourceCount(resources, total) > resources.length
}

export type ModelUsageResourceKind = 'knowledge_base' | 'agent'

export type KnowledgeBaseModelUsageSection = 'models' | 'multimodal' | 'asr' | 'advanced'

export type AgentModelUsageSection = 'model' | 'multimodal' | 'suggestions'

const knowledgeBaseUsageSections: Partial<Record<KnownModelUsageBinding, KnowledgeBaseModelUsageSection>> = {
  embedding_model: 'models',
  summary_model: 'models',
  question_model: 'advanced',
  wiki_synthesis_model: 'models',
  auto_tag_model: 'models',
  image_processing_model: 'multimodal',
  vlm_model: 'multimodal',
  asr_model: 'asr',
}

const agentUsageSections: Partial<Record<KnownModelUsageBinding, AgentModelUsageSection>> = {
  chat_model: 'model',
  rerank_model: 'model',
  query_understand_model: 'model',
  vlm_model: 'multimodal',
  asr_model: 'multimodal',
  follow_up_model: 'suggestions',
}

export function modelUsageKnowledgeBaseSection(
  bindings: readonly string[],
): KnowledgeBaseModelUsageSection {
  for (const binding of bindings) {
    const section = knowledgeBaseUsageSections[binding as KnownModelUsageBinding]
    if (section) return section
  }
  return 'models'
}

export function modelUsageAgentSection(
  bindings: readonly string[],
): AgentModelUsageSection {
  for (const binding of bindings) {
    const section = agentUsageSections[binding as KnownModelUsageBinding]
    if (section) return section
  }
  return 'model'
}

export function modelUsageResourceRoute(
  kind: ModelUsageResourceKind,
  id: string,
  bindings: readonly string[] = [],
) {
  if (kind === 'knowledge_base') {
    return { path: `/platform/knowledge-bases/${encodeURIComponent(id)}` }
  }
  return {
    path: '/platform/agents',
    query: { edit: id, section: modelUsageAgentSection(bindings) },
  }
}
