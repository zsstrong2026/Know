/**
 * 工作台 AI 的类型（§11.2、§11.3）。
 *
 * 单独成文件是为了让 page-actions / use-inline-ai 等纯逻辑模块能只依赖类型，
 * 不把 axios 与 Pinia 拖进 `tsx --test`。
 */

/** 内联车道允许的 action；与后端注册表一一对应。 */
export type InlineAIAction =
  | 'polish'
  | 'continue'
  | 'translate'
  | 'summarize'
  | 'outline'
  | 'custom'
  | 'proofread'

export const INLINE_AI_ACTIONS: readonly InlineAIAction[] = [
  'polish',
  'continue',
  'translate',
  'summarize',
  'outline',
  'custom',
  'proofread',
]

/** 纠错动作返回的一条问题；字段名与后端 JSON 契约一致。 */
export interface ProofreadIssue {
  original: string
  suggestion: string
  reason: string
}

/** 请求体（§11.3）。不含 model_id / system_prompt / tools：那些由服务端解析。 */
export interface InlineAIRequest {
  action: InlineAIAction
  text: string
  instruction?: string
  target_lang?: string
  context_text?: string
  item_id?: string
  agent_id?: string
  stream?: boolean
  /**
   * 前端专用：本次动作的写入方式。**不发给服务端**（后端只认 action 与 target_lang），
   * 由 use-page-ai 在构造请求体时剔除。
   */
  writeMode?: 'replace' | 'below' | 'aside'
}

export interface InlineAIUsage {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

export interface InlineAIResult {
  text: string
  modelId: string
  agentId?: string
  usage: InlineAIUsage | null
}

/** 长度上限，与后端 types/workbench_ai.go 保持一致；前端先拦一次给即时提示。 */
export const INLINE_AI_TEXT_LIMIT = 8000
export const INLINE_AI_INSTRUCTION_LIMIT = 500
export const INLINE_AI_CONTEXT_LIMIT = 4000
