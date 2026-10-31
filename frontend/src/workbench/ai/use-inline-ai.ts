/**
 * 内联 AI 的 SSE 客户端（§11.3）。
 *
 * 为什么不用 axios：需要读 ReadableStream 做逐帧解析，axios 会缓冲整个响应。
 * 帧格式是 `event: delta|done|error` + 单行 JSON data，与后端 handler 一致。
 */
import { getApiBaseUrl } from '@/utils/api-base'
import { buildAuthHeaders } from '@/utils/request'
import { WORKBENCH_ERROR } from '@/workbench/error-code'
import type { InlineAIRequest, InlineAIResult, InlineAIUsage } from './inline-ai-types'

const INLINE_URL = '/api/v1/workbench/ai/inline'

/** 内联 AI 的对外错误：带后端错误码，供视图区分 2408 / 2410 / 2402。 */
export class WorkbenchAIError extends Error {
  readonly code: number

  constructor(code: number, message: string) {
    super(message)
    this.name = 'WorkbenchAIError'
    this.code = code
  }
}

/**
 * 发起一次内联生成，逐帧回调增量文本。
 *
 * 约定：
 *   - 正常结束 resolve，返回聚合结果（含 model_id 与 usage）；
 *   - 收到 error 帧 reject WorkbenchAIError(2410)，调用方保留已生成文本；
 *   - 开流前的普通 HTTP 错误（2400/2402/2408）也 reject，带后端 code；
 *   - signal 中止时 reject AbortError，调用方不提示错误。
 */
export async function streamInlineAI(
  request: InlineAIRequest,
  onDelta: (text: string) => void,
  signal?: AbortSignal,
): Promise<InlineAIResult> {
  const response = await fetch(`${getApiBaseUrl()}${INLINE_URL}`, {
    method: 'POST',
    headers: buildAuthHeaders({
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
    }),
    // writeMode 是前端内部字段，后端只认 action 与 target_lang，必须剔除。
    body: JSON.stringify({ ...request, writeMode: undefined, stream: true }),
    signal,
  })

  if (!response.ok || !response.body) {
    throw await toWorkbenchError(response)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let text = ''
  let result: InlineAIResult = { text: '', modelId: '', usage: null }

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      // SSE 帧以空行分隔；最后一段可能不完整，留在 buffer 里等下一个 chunk。
      const frames = buffer.split('\n\n')
      buffer = frames.pop() ?? ''
      for (const frame of frames) {
        const parsed = parseFrame(frame)
        if (!parsed) continue
        if (parsed.event === 'delta') {
          const chunk = String(parsed.data.text ?? '')
          if (chunk) {
            text += chunk
            onDelta(chunk)
          }
        } else if (parsed.event === 'done') {
          result = {
            text,
            modelId: String(parsed.data.model_id ?? ''),
            agentId: parsed.data.agent_id ? String(parsed.data.agent_id) : undefined,
            usage: (parsed.data.usage as InlineAIUsage | null) ?? null,
          }
        } else if (parsed.event === 'error') {
          throw new WorkbenchAIError(
            Number(parsed.data.code ?? WORKBENCH_ERROR.streamBroken),
            String(parsed.data.message ?? 'model stream interrupted'),
          )
        }
      }
    }
  } finally {
    // 无论正常结束还是抛错都要释放 reader，否则 keep-alive 连接会被占住。
    reader.releaseLock()
  }

  return { ...result, text }
}

/** 解析单个 SSE 帧；无 data 行（心跳注释）返回 null。 */
function parseFrame(frame: string): { event: string; data: Record<string, unknown> } | null {
  if (!frame.trim()) return null
  let event = 'message'
  const dataLines: string[] = []
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) {
      event = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trim())
    }
  }
  if (dataLines.length === 0) return null
  try {
    return { event, data: JSON.parse(dataLines.join('\n')) as Record<string, unknown> }
  } catch {
    return null
  }
}

/** 把 HTTP 错误响应还原成带 code 的 WorkbenchAIError。 */
async function toWorkbenchError(response: Response): Promise<WorkbenchAIError> {
  let code = response.status
  let message = response.statusText || 'request failed'
  try {
    const body = (await response.json()) as {
      error?: { code?: number; message?: string }
      code?: number
      message?: string
    }
    code = Number(body?.error?.code ?? body?.code ?? code)
    message = body?.error?.message ?? body?.message ?? message
  } catch {
    // 非 JSON 错误体（网关 502 页面等）保持 HTTP 状态码即可。
  }
  return new WorkbenchAIError(code, message)
}

// ---------------------------------------------------------------------------
// 问答车道（§11.8）：与内联共用帧格式，额外多一个 references 帧。
// 检索范围由服务端强制为工作 KB，这里不传任何 KB 参数。
// ---------------------------------------------------------------------------

const ASK_URL = '/api/v1/workbench/ai/ask'

export interface AskAIRequest {
  question: string
  item_id?: string
  include_context?: boolean
  agent_id?: string
}

/** 问答命中来源；只带定位字段，不含正文。 */
export interface AskAIReference {
  knowledge_id: string
  knowledge_title: string
  chunk_index: number
  score: number
}

/**
 * 发起一次工作台问答，逐帧回调增量并先行回调引用。
 *
 * 与 streamInlineAI 的差异：
 *   - 有 references 帧，必须在正文之前回调，前端才能边渲染来源边等回答；
 *   - 没有 done 之外的写入语义——问答结果只进对话，不写入文档。
 */
export async function streamAskAI(
  request: AskAIRequest,
  handlers: {
    onDelta: (text: string) => void
    onReferences?: (refs: AskAIReference[]) => void
  },
  signal?: AbortSignal,
): Promise<InlineAIResult> {
  const response = await fetch(`${getApiBaseUrl()}${ASK_URL}`, {
    method: 'POST',
    headers: buildAuthHeaders({
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
    }),
    body: JSON.stringify(request),
    signal,
  })
  if (!response.ok || !response.body) throw await toWorkbenchError(response)

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let text = ''
  let result: InlineAIResult = { text: '', modelId: '', usage: null }

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const frames = buffer.split('\n\n')
      buffer = frames.pop() ?? ''
      for (const frame of frames) {
        const parsed = parseFrame(frame)
        if (!parsed) continue
        if (parsed.event === 'references') {
          handlers.onReferences?.((parsed.data.references as AskAIReference[]) ?? [])
        } else if (parsed.event === 'delta') {
          const chunk = String(parsed.data.text ?? '')
          if (chunk) {
            text += chunk
            handlers.onDelta(chunk)
          }
        } else if (parsed.event === 'done') {
          result = {
            text,
            modelId: String(parsed.data.model_id ?? ''),
            agentId: parsed.data.agent_id ? String(parsed.data.agent_id) : undefined,
            usage: (parsed.data.usage as InlineAIUsage | null) ?? null,
          }
        } else if (parsed.event === 'error') {
          throw new WorkbenchAIError(
            Number(parsed.data.code ?? WORKBENCH_ERROR.streamBroken),
            String(parsed.data.message ?? 'model stream interrupted'),
          )
        }
      }
    }
  } finally {
    reader.releaseLock()
  }

  return { ...result, text }
}
