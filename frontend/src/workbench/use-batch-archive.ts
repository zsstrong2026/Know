/**
 * 批量归档的请求编排（§9.8.1）。
 *
 * 关键约束（都是规格硬要求，不能为了“快”而改）：
 *   - 并发固定 2，不开放调大；单条超时 60 秒；
 *   - 每条只走单条归档端点，不新增 /archives/batch；
 *   - 部分失败不阻塞其他条目，也不回滚已成功的条目。
 */
import { createArchive, getWorkbenchContent, refreshArchive } from '@/api/workbench'
import {
  capBatchSelection,
  planBatchArchive,
  reasonFromErrorCode,
  summarizeBatchResults,
  type BatchArchiveCandidate,
  type BatchArchivePlan,
  type BatchArchiveResult,
  type BatchArchiveSummary,
} from './batch-archive'

/** 并发固定 2：既不打满后端，也不至于串行慢到用户放弃。 */
const CONCURRENCY = 2
/** 单条超时：与规格一致，超时记为 network 失败。 */
const PER_ITEM_TIMEOUT_MS = 60_000
/** 单次上限：超过只处理前 20 项。 */
const MAX_ITEMS = 20

export interface BatchArchiveRunOptions {
  signal?: AbortSignal
  /** 每条开始前的回调，供 UI 显示「已处理 N / M」 */
  onProgress?: (done: number, total: number) => void
  /** 逐条产出结果，供对话框实时更新行状态 */
  onResult?: (result: BatchArchiveResult) => void
}

export interface BatchArchivePlanPrep {
  plan: BatchArchivePlan
  /** 超过上限被丢掉的条数；>0 时必须提示用户 */
  dropped: number
}

/** 预检：只读列表里已批量填充的状态，不发请求（§9.8.1）。 */
export function prepareBatchArchive(selected: BatchArchiveCandidate[]): BatchArchivePlanPrep {
  const { kept, dropped } = capBatchSelection(selected, MAX_ITEMS)
  return { plan: planBatchArchive(kept), dropped }
}

/**
 * 执行批量归档。
 *
 * 返回全部结果（含跳过项）。已成功的条目不会因为后续失败而回滚，
 * 调用方只需把 failed 项交给「重试失败项」。
 */
export async function runBatchArchive(
  plan: BatchArchivePlan,
  options: BatchArchiveRunOptions = {},
): Promise<{ results: BatchArchiveResult[]; summary: BatchArchiveSummary }> {
  const total = plan.actionable.length
  const results: BatchArchiveResult[] = []

  // 跳过项先落进结果：用户要能看到“为什么这 2 项没动”。
  for (const item of plan.skipped) {
    results.push({ key: item.key, title: item.title, status: 'skipped', reason: 'up_to_date' })
  }

  let done = 0
  let cursor = 0
  let aborted = false

  /** 取下一个待处理项；游标自增是同步的，因此不会重复领取。 */
  function nextItem() {
    if (cursor >= plan.actionable.length) return null
    const item = plan.actionable[cursor]
    cursor += 1
    return item
  }

  async function worker() {
    while (!aborted) {
      const item = nextItem()
      if (!item) return
      const result = await runOne(item, options.signal)
      results.push(result)
      options.onResult?.(result)
      done += 1
      options.onProgress?.(done, total)
      if (result.status !== 'archived') continue
    }
  }

  // 并发固定为 2：最多两个 worker，超出部分排队。
  const workers = Array.from({ length: Math.min(CONCURRENCY, Math.max(total, 1)) }, worker)
  await Promise.all(workers)
  if (options.signal?.aborted) aborted = true

  return { results, summary: summarizeBatchResults(results) }
}

/** 执行单条：create/recreate 走 createArchive，update/retry 走 refreshArchive。 */
async function runOne(
  item: BatchArchivePlan['actionable'][number],
  signal?: AbortSignal,
): Promise<BatchArchiveResult> {
  try {
    // 画布/数据库的归档必须带 markdown；列表页没有 Y.Doc，只能取服务端已存的
    // text_content（它由编辑器自动保存写入）。章节文档则由服务端自行渲染。
    const markdown = item.sourceType === 'item' ? await loadItemMarkdown(item.sourceId) : undefined
    if (item.sourceType === 'item' && !markdown?.trim()) {
      return { key: item.key, title: item.title, status: 'failed', reason: 'not_ready' }
    }

    const payload = {
      source_type: item.sourceType,
      source_id: item.sourceId,
      base_version: item.baseVersion,
      markdown,
    }

    await withTimeout(
      async () => {
        if (item.op === 'create' || item.op === 'recreate') {
          await createArchive(payload)
        } else {
          await refreshArchive(payload)
        }
      },
      PER_ITEM_TIMEOUT_MS,
      signal,
    )
    return { key: item.key, title: item.title, status: 'archived' }
  } catch (err) {
    if (isAbort(err)) {
      return { key: item.key, title: item.title, status: 'failed', reason: 'network' }
    }
    const code = errorCodeOf(err)
    return {
      key: item.key,
      title: item.title,
      status: 'failed',
      reason: reasonFromErrorCode(code),
      message: String(code),
    }
  }
}

/**
 * 取条目的归档正文。
 *
 * 直接复用保存时写入的 `text_content`（编辑器投影的 Markdown），
 * 不重新拉 Y.Doc 再解析：批量归档不做编辑，没有必要背 8MB 快照。
 */
async function loadItemMarkdown(itemId: string): Promise<string> {
  const content = await getWorkbenchContent(itemId)
  return content.text_content ?? ''
}

/** 逐条超时：用 race 而不是给 axios 传 timeout，避免全局超时配置被覆盖。 */
async function withTimeout(
  task: () => Promise<void>,
  timeoutMs: number,
  signal?: AbortSignal,
): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    await Promise.race([
      task(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error('timeout')), timeoutMs)
      }),
      ...(signal
        ? [
            new Promise<never>((_, reject) => {
              if (signal.aborted) reject(new DOMException('aborted', 'AbortError'))
              signal.addEventListener('abort', () =>
                reject(new DOMException('aborted', 'AbortError')),
              )
            }),
          ]
        : []),
    ])
  } finally {
    if (timer) clearTimeout(timer)
  }
}

function errorCodeOf(err: unknown): number {
  const e = err as { error?: { code?: number }; code?: number }
  return Number(e?.error?.code ?? e?.code ?? 0)
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}
