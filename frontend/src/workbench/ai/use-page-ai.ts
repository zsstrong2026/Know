/**
 * Page AI 主控（§9.15）：选区采样 → 请求 → 预览 → 校验指纹 → 写入。
 *
 * 设计要点：
 *   - 采样与写入都通过注入的 editor 适配器完成，本模块不直接依赖 BlockSuite；
 *   - 同一页只允许一个生成：新请求先 abort 上一个（§9.15.1）；
 *   - 写入前必须重新校验指纹，绝不覆盖用户新输入的文字（§9.15.3）；
 *   - 多候选：首轮只生成 1 个，用户点「换一种」时并行补 2 个（不浪费首字延迟）；
 *   - 纠错动作返回结构化问题列表，逐条接受后再一次性写入。
 */
import { computed, ref, shallowRef } from 'vue'
import { INLINE_AI_TEXT_LIMIT, type InlineAIRequest, type ProofreadIssue } from './inline-ai-types'
import { applyIssuesToText, parseProofreadIssues } from './proofread'
import { joinChunkResults, shouldChunk, splitLongText } from './long-doc'
import { streamInlineAI, WorkbenchAIError } from './use-inline-ai'
import {
  buildWritePlan,
  fingerprintOf,
  hashText,
  verifyFingerprint,
  type BlockSpan,
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
  /**
   * 无选区时的插入锚点块。
   *
   * 侧栏页面级命令（总结整页等）没有选区，但结果要插到文末或当前块之后；
   * 由编辑器给出锚点，写入计划就以它作为 below 的落点。
   */
  getAnchorBlockId?(): string | null
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

/** 一个候选结果；多候选预览按 id 切换。 */
export interface PageAICandidate {
  id: string
  text: string
  streaming: boolean
  failed: boolean
}

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

/** 「换一种」一次补几个候选；1 个是默认，用户主动要求时才多花钱。 */
const EXTRA_CANDIDATE_COUNT = 2

export function usePageAI(options: PageAIStateOptions) {
  const phase = ref<PageAIPhase>('idle')
  /** 全部候选；首轮只有 1 个，「换一种」后追加。 */
  const candidates = ref<PageAICandidate[]>([])
  const activeIndex = ref(0)
  const title = ref('')
  const mode = ref<InlineWriteMode>('replace')
  const error = ref<PageAIError | null>(null)
  /** 纠错结果的问题列表；非纠错动作为空。 */
  const issues = ref<ProofreadIssue[]>([])
  /** 被用户取消勾选的问题下标（默认全部接受）。 */
  const rejectedIssues = ref<Set<number>>(new Set())
  /** 预览对应的指纹与请求；重试直接复用它，不拼接上一次的半截结果。 */
  const activeRequest = shallowRef<InlineAIRequest | null>(null)
  /** 发起请求时的原文；纠错写入以它为底本。 */
  const originalText = ref('')
  /** 跨块选区的块快照；纠错写入时逐块应用，避免整段串到一块里。 */
  const originalSpans = shallowRef<BlockSpan[] | null>(null)
  const fingerprint = shallowRef<SelectionFingerprint | null>(null)
  /** 本次接管是否按纯文本写入（侧栏对话直接插入，不做 Markdown 解析）。 */
  const plainWrite = ref(false)
  let controller: AbortController | null = null

  /** 当前动作；视图据此决定渲染问题卡还是 diff。 */
  const activeAction = computed(() => activeRequest.value?.action ?? null)

  /** 当前候选的文本；所有视图只读这一个值。 */
  const text = computed(() => candidates.value[activeIndex.value]?.text ?? '')
  const streaming = computed(() => candidates.value.some((candidate) => candidate.streaming))
  const visible = computed(() => phase.value !== 'idle')
  const activeCandidate = computed(() => candidates.value[activeIndex.value] ?? null)
  /** 是否有多个候选可切换（决定是否渲染 Tab）。 */
  const hasAlternatives = computed(() => candidates.value.length > 1)
  /** 能否重试：只有走过模型请求的预览才可以；侧栏问答接管的内容没有原请求。 */
  const retryable = computed(() => activeRequest.value !== null)

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
    candidates.value = []
    activeIndex.value = 0
    issues.value = []
    rejectedIssues.value = new Set()
    error.value = null
    activeRequest.value = null
    originalSpans.value = null
    fingerprint.value = null
    plainWrite.value = false
  }

  /** 收集一次请求的完整文本；与首轮共用同一套帧解析。 */
  async function collect(
    request: InlineAIRequest,
    signal: AbortSignal,
    onDelta?: (delta: string) => void,
  ): Promise<string> {
    let buffer = ''
    await streamInlineAI(
      { ...request, item_id: options.itemId() || undefined, agent_id: options.agentId?.() },
      (delta) => {
        buffer += delta
        onDelta?.(delta)
      },
      signal,
    )
    return buffer
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
    plainWrite.value = false
    candidates.value = []
    activeIndex.value = 0
    issues.value = []
    rejectedIssues.value = new Set()
    title.value = label
    activeRequest.value = request
    originalText.value = request.text
    mode.value = request.writeMode ?? 'replace'
    // 超长但动作支持分块时（总结/大纲），走 map-reduce，不直接报 413。
    const chunked = shouldChunk(request.action, request.text)
    if (!chunked && request.text.length > INLINE_AI_TEXT_LIMIT) {
      phase.value = 'failed'
      fail({ kind: 'too-long', code: WORKBENCH_ERROR.payloadTooLarge, message: 'text too long' })
      return
    }

    const adapter = options.editor()
    const selection = adapter?.getSelection() ?? null
    // 跨块选区的块快照：纠错写入时逐块应用，避免整段串到一块里。
    originalSpans.value = selection?.spans ?? null
    // 有选区用真实选区；无选区但结果是「插入下方」时，用编辑器给出的锚点块。
    fingerprint.value = selection
      ? fingerprintOf(selection)
      : anchorFingerprint(adapter, options.itemId())
    phase.value = 'streaming'

    const primary: PageAICandidate = { id: 'c1', text: '', streaming: true, failed: false }
    candidates.value = [primary]

    controller = new AbortController()
    const signal = controller.signal
    /** 本轮是否已生成成功；成功后再判断是否直接写入。 */
    let succeeded = false
    try {
      const full = chunked
        ? await collectChunked(request, signal, (delta) => {
            primary.text += delta
            candidates.value = [...candidates.value]
          })
        : await collect(request, signal, (delta) => {
            primary.text += delta
            // 数组元素是普通对象，Vue 会追踪属性变更，这里显式替换以触发重算。
            candidates.value = [...candidates.value]
          })
      primary.streaming = false
      candidates.value = [...candidates.value]
      finalize(full)
      succeeded = true
    } catch (err) {
      if (isAbort(err)) {
        phase.value = 'idle'
        return
      }
      primary.streaming = false
      primary.failed = true
      candidates.value = [...candidates.value]
      phase.value = 'failed'
      fail(toPageAIError(err))
    } finally {
      controller = null
      // confirmInsert=false 时直接写入，仍保留一次撤销（§9.15.3）。
      if (succeeded && mode.value !== 'aside' && options.confirmInsert?.() === false) {
        await apply(mode.value === 'below' ? 'below' : 'replace')
      }
    }
  }

  /**
   * 长文分块生成（map-reduce）。
   *
   * 逐块串行请求：一是避免并发打满模型配额，二是让用户能看到进度式增长。
   * 结果是各块输出的线性拼接——总结与大纲都可以这样合并，不需要二次归纳。
   */
  async function collectChunked(
    request: InlineAIRequest,
    signal: AbortSignal,
    onDelta: (delta: string) => void,
  ): Promise<string> {
    const chunks = splitLongText(request.text)
    const results: string[] = []
    for (let index = 0; index < chunks.length; index += 1) {
      const chunk = chunks[index]
      // 块间用空行分隔再回调，让流式预览看得出分段。
      if (index > 0) onDelta('\n\n')
      const part = await collect({ ...request, text: chunk }, signal, onDelta)
      results.push(part)
    }
    return joinChunkResults(results)
  }

  /** 收尾：纠错动作解析问题列表；其余动作直接把阶段推到 ready。 */
  function finalize(fullText: string) {
    if (activeRequest.value?.action === 'proofread') {
      issues.value = parseProofreadIssues(fullText)
      rejectedIssues.value = new Set()
    }
    phase.value = 'ready'
  }

  /**
   * 「换一种」：并行再生成 2 个候选并追加到 Tab 里。
   *
   * 只在当前没有流式请求时可用；已存在的候选全部保留，用户可以来回对比。
   */
  async function alternatives(): Promise<void> {
    const request = activeRequest.value
    if (!request || streaming.value) return
    const baseIndex = candidates.value.length
    const pending: PageAICandidate[] = Array.from({ length: EXTRA_CANDIDATE_COUNT }, (_, i) => ({
      id: `c${baseIndex + i + 1}`,
      text: '',
      streaming: true,
      failed: false,
    }))
    candidates.value = [...candidates.value, ...pending]
    phase.value = 'streaming'

    controller = new AbortController()
    const signal = controller.signal
    try {
      await Promise.all(
        pending.map(async (candidate, offset) => {
          try {
            const full = await collect(request, signal, (delta) => {
              candidate.text += delta
              candidates.value = [...candidates.value]
            })
            candidate.streaming = false
            if (offset === 0) finalize(full)
          } catch (err) {
            candidate.streaming = false
            candidate.failed = true
            if (!isAbort(err)) fail(toPageAIError(err))
          } finally {
            candidates.value = [...candidates.value]
          }
        }),
      )
      if (phase.value === 'streaming') phase.value = 'ready'
    } finally {
      controller = null
    }
  }

  /** 切换到某个候选；纠错的问题列表随候选重新解析。 */
  function selectCandidate(index: number) {
    if (index < 0 || index >= candidates.value.length) return
    activeIndex.value = index
    if (activeRequest.value?.action === 'proofread') {
      issues.value = parseProofreadIssues(candidates.value[index].text)
      rejectedIssues.value = new Set()
    }
  }

  /** 勾选 / 取消一条纠错问题。 */
  function toggleIssue(index: number) {
    const next = new Set(rejectedIssues.value)
    if (next.has(index)) next.delete(index)
    else next.add(index)
    rejectedIssues.value = next
  }

  /** 是否全部问题都被取消（此时没有可写入内容）。 */
  const allIssuesRejected = computed(
    () => issues.value.length > 0 && rejectedIssues.value.size >= issues.value.length,
  )

  /**
   * 实际要写入的文本。
   *
   * 纠错走「应用选中的问题」：以发起请求时的原文为底本，按已勾选的问题替换；
   * 其余动作直接用当前候选的生成结果。
   */
  function textToApply(): string {
    if (activeRequest.value?.action !== 'proofread') return text.value
    const spans = originalSpans.value
    if (spans?.length) {
      // 跨块：逐块应用，再用换行拼回去（写入时同样按块拆开）。
      return spans
        .map((span) => applyIssuesToText(span.text, issues.value, rejectedIssues.value))
        .join('\n')
    }
    return applyIssuesToText(originalText.value, issues.value, rejectedIssues.value)
  }

  /**
   * 跨块纠错的分块结果。
   *
   * 只在「跨块 + 纠错」时返回：逐块应用问题，保证每块写回自己那一段，
   * 不会被 buildWritePlan 的按空行切分打乱。
   */
  function segmentsToApply(): string[] | null {
    const spans = originalSpans.value
    if (activeRequest.value?.action !== 'proofread' || !spans?.length) return null
    return spans.map((span) => applyIssuesToText(span.text, issues.value, rejectedIssues.value))
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

    // 锚点写入没有真实选区：只要求锚点块仍然存在，不做文本比对。
    if (!fp.anchorOnly) {
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
    } else if (!adapter.getAnchorBlockId?.()) {
      fail({ kind: 'not-found', code: WORKBENCH_ERROR.notFound, message: 'anchor missing' })
      cancel()
      return false
    }

    const payload = textToApply()
    if (!payload.trim()) return false

    const plan = buildWritePlan(fp, payload, writeMode, plainWrite.value)
    if (!plan) return false
    // 跨块纠错：用逐块应用后的结果覆盖按空行切分的对齐结果。
    const proofreadSegments = segmentsToApply()
    if (proofreadSegments && plan.spans?.length === proofreadSegments.length) {
      plan.segments = proofreadSegments
    }
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

  /**
   * 直接接管一段现成文本进入预览（侧栏问答的「替换选中 / 插入下方」）。
   *
   * 不再请求模型：问答结果已经是最终内容，这里只补上指纹与写入方式。
   * `plain=true` 表示内容已在视图层整理成纯文本，写入时不再按 Markdown 解析。
   */
  function adopt(
    content: string,
    label: string,
    writeMode: InlineWriteMode = 'below',
    plain = false,
  ): boolean {
    const adapter = options.editor()
    const selection = adapter?.getSelection() ?? null
    const fp = selection ? fingerprintOf(selection) : anchorFingerprint(adapter, options.itemId())
    if (!fp) return false
    abort()
    fingerprint.value = fp
    plainWrite.value = plain
    activeRequest.value = null
    originalText.value = selection?.text ?? ''
    originalSpans.value = selection?.spans ?? null
    candidates.value = [{ id: 'c1', text: content, streaming: false, failed: false }]
    activeIndex.value = 0
    issues.value = []
    rejectedIssues.value = new Set()
    mode.value = writeMode
    title.value = label
    error.value = null
    phase.value = 'ready'
    return true
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
    issues,
    rejectedIssues,
    candidates,
    activeIndex,
    activeCandidate,
    hasAlternatives,
    retryable,
    allIssuesRejected,
    streaming,
    visible,
    lastLang,
    /** 当前动作；视图据此决定渲染问题卡还是 diff。 */
    activeAction,
    /** 发起请求时的原文；diff 以此为对照。 */
    originalText,
    run,
    apply,
    retry,
    cancel,
    abort,
    alternatives,
    selectCandidate,
    toggleIssue,
    adopt,
  }
}

/**
 * 无选区时的插入锚点指纹。
 *
 * 只用于「插入下方」：锚点块存在即可插入，块的正文变化不影响插入位置，
 * 因此这里不记录文本哈希（记录反而会误判成"内容被改"而拒绝写入）。
 */
function anchorFingerprint(
  adapter: PageAIEditorAdapter | null,
  itemId: string,
): SelectionFingerprint | null {
  const blockId = adapter?.getAnchorBlockId?.() ?? null
  if (!blockId) return null
  return {
    blockId,
    start: 0,
    end: 0,
    textHash: hashText(''),
    itemId,
    anchorOnly: true,
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
