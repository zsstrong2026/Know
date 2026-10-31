/**
 * Page AI 主控（§9.15）：选区采样 → 请求 → 预览 → 校验指纹 → 写入。
 *
 * 设计要点：
 *   - 采样与写入都通过注入的 editor 适配器完成，本模块不直接依赖 BlockSuite；
 *   - 同一页只允许一个生成：新请求先 abort 上一个（§9.15.1）；
 *   - 写入前必须重新校验指纹，绝不覆盖用户新输入的文字（§9.15.3）。
 */
import { computed, ref, shallowRef } from 'vue'
import { INLINE_AI_TEXT_LIMIT, type InlineAIRequest } from './inline-ai-types'
import { streamInlineAI, WorkbenchAIError } from './use-inline-ai'
import {
  buildWritePlan,
  fingerprintOf,
  verifyFingerprint,
  type SelectionFingerprint,
  type SelectionSnapshot,
  type FingerprintVerdict,
  type InlineWriteMode,
} from './selection-fingerprint'
import { WORKBENCH_ERROR } from '@/workbench/error-code'
import type { TranslateLang } from './page-actions'

/** 编辑器适配器：由视图层提供，只有两个职责——读选区、写内容。 */
export interface PageAIEditorAdapter {
  /** 取当前选区；无选区或选区不可写时返回 null */
  getSelection(): SelectionSnapshot | null
  /** 选区是否落在代码块内（代码块只允许「解释选中」） */
  isCodeBlock?(): boolean
  /** 取页面纯文本（侧栏页面级命令用）；超长由调用方判 */
  getPageText(): string
  /** 应用写入；失败返回 false，表示目标块已不存在 */
  applyWrite(plan: ReturnType<typeof buildWritePlan> & object): Promise<boolean>
}

export type PageAIErrorKind =
  | 'too-long'
  | 'no-model'
  | 'stream-broken'
  | 'not-found'
  | 'unknown'
  | 'aborted'

export interface PageAIError {
  kind: PageAIErrorKind
  code: number
  message: string
}

/** 预览状态。`applied` 表示已写入并结束；`failed` 保留已生成文本供重试。 */
export type PageAIPhase = 'idle' | 'streaming' | 'ready' | 'applied' | 'failed'

export interface PageAIStateOptions {
  editor: () => PageAIEditorAdapter | null
  /** 当前条目 id；切换条目时作废预览 */
  itemId: () => string
  /** 默认智能体 id；空表示用租户默认模型 */
  agentId?: () => string
  /** 是否先预览再写入（§9.14.5 confirmInsert） */
  confirmInsert?: () => boolean
  /** 回调：仅用于 toast，不含正文 */
  onError?: (error: PageAIError) => void
  onApplied?: () => void
}

export function usePageAI(options: PageAIStateOptions) {
  const phase = ref<PageAIPhase>('idle')
  const text = ref('')
  const title = ref('')
  const mode = ref<InlineWriteMode>('replace')
  const error = ref<PageAIError | null>(null)
  /** 预览对应的指纹与请求；重试直接复用它，不拼接上一次的半截结果。 */
  const activeRequest = shallowRef<InlineAIRequest | null>(null)
  const fingerprint = shallowRef<SelectionFingerprint | null>(null)
  let controller: AbortController | null = null

  const streaming = computed(() => phase.value === 'streaming')
  const visible = computed(() => phase.value !== 'idle')

  /** 上一次使用的翻译语言；刷新页面回英语（不写偏好，§9.15.2）。 */
  const lastLang = ref<TranslateLang>('en')

  /** 中止进行中的请求；切换条目、关闭预览、取消都调它。 */
  function abort() {
    controller?.abort()
    controller = null
  }

  /** 关闭预览并丢弃未确认结果（不产生任何编辑）。 */
  function cancel() {
    abort()
    phase.value = 'idle'
    text.value = ''
    error.value = null
    activeRequest.value = null
    fingerprint.value = null
  }

  /**
   * 发起一次内联生成。
   *
   * 传入 text 时用于侧栏页面级命令（当前页纯文本）；不传则读取当前选区。
   */
  async function run(request: InlineAIRequest, label: string): Promise<void> {
    // 同一页只保留一个生成：先中止上一个，再替换预览。
    abort()
    error.value = null
    text.value = ''
    title.value = label
    activeRequest.value = request
    mode.value = request.writeMode ?? 'replace'
    if (request.text.length > INLINE_AI_TEXT_LIMIT) {
      phase.value = 'failed'
      fail({ kind: 'too-long', code: WORKBENCH_ERROR.payloadTooLarge, message: 'text too long' })
      return
    }

    const selection = options.editor()?.getSelection() ?? null
    fingerprint.value = selection ? fingerprintOf(selection) : null
    phase.value = 'streaming'

    controller = new AbortController()
    try {
      await streamInlineAI(
        { ...request, item_id: options.itemId() || undefined, agent_id: options.agentId?.() },
        (delta) => {
          text.value += delta
        },
        controller.signal,
      )
      phase.value = 'ready'
      // confirmInsert=false 时直接写入，仍保留一次撤销（§9.15.3）。
      if (mode.value !== 'aside' && options.confirmInsert?.() === false) {
        await apply(mode.value === 'below' ? 'below' : 'replace')
      }
    } catch (err) {
      if (isAbort(err)) {
        phase.value = 'idle'
        return
      }
      phase.value = 'failed'
      fail(toPageAIError(err))
    } finally {
      controller = null
    }
  }

  /**
   * 应用结果。
   *
   * 顺序：条目未变 → 指纹未变 → 目标块存在。任一失败都只提示、不写入。
   */
  async function apply(writeMode: 'replace' | 'below'): Promise<boolean> {
    const fp = fingerprint.value
    if (!fp) {
      fail({ kind: 'unknown', code: 0, message: 'no selection' })
      return false
    }
    const adapter = options.editor()
    if (!adapter) return false

    const current = adapter.getSelection()
    const verdict = verifyFingerprint(fp, current)
    if (!verdict.ok) {
      fail(verdictToError(verdict))
      // 选区/条目变化：丢弃预览，避免用户误以为还能应用。
      if (verdict.reason === 'item-changed' || verdict.reason === 'block-missing') {
        cancel()
      }
      return false
    }

    const plan = buildWritePlan(fp, text.value, writeMode)
    if (!plan) return false
    const applied = await adapter.applyWrite(plan)
    if (!applied) {
      fail({ kind: 'not-found', code: WORKBENCH_ERROR.notFound, message: 'block missing' })
      cancel()
      return false
    }
    phase.value = 'applied'
    options.onApplied?.()
    return true
  }

  /** 重试：重新发原始请求，不拼接上一次结果（§9.15.3）。 */
  async function retry(): Promise<void> {
    const request = activeRequest.value
    if (!request) return
    await run(request, title.value)
  }

  function fail(next: PageAIError) {
    error.value = next
    options.onError?.(next)
  }

  return {
    phase,
    text,
    title,
    mode,
    error,
    streaming,
    visible,
    lastLang,
    run,
    apply,
    retry,
    cancel,
    abort,
  }
}

/** 把编辑结果映射成用户可读的错误类型。 */
function verdictToError(verdict: Extract<FingerprintVerdict, { ok: false }>): PageAIError {
  switch (verdict.reason) {
    case 'item-changed':
      return { kind: 'not-found', code: WORKBENCH_ERROR.notFound, message: 'item changed' }
    case 'block-missing':
      return { kind: 'not-found', code: WORKBENCH_ERROR.notFound, message: 'block missing' }
    default:
      return { kind: 'unknown', code: 0, message: verdict.reason }
  }
}

function toPageAIError(err: unknown): PageAIError {
  if (err instanceof WorkbenchAIError) {
    if (err.code === WORKBENCH_ERROR.payloadTooLarge) {
      return { kind: 'too-long', code: err.code, message: err.message }
    }
    if (err.code === WORKBENCH_ERROR.noModel) {
      return { kind: 'no-model', code: err.code, message: err.message }
    }
    if (err.code === WORKBENCH_ERROR.streamBroken) {
      return { kind: 'stream-broken', code: err.code, message: err.message }
    }
    if (err.code === WORKBENCH_ERROR.notFound) {
      return { kind: 'not-found', code: err.code, message: err.message }
    }
    return { kind: 'unknown', code: err.code, message: err.message }
  }
  return { kind: 'unknown', code: 0, message: err instanceof Error ? err.message : 'unknown error' }
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}
