import { computed, ref, watch, type Ref } from 'vue'
import { listDocDocumentsGlobal } from '@/api/doc-assistant'
import { listWorkbenchItems } from '@/api/workbench'
import i18n from '@/i18n'
import { itemCaption } from '@/workbench/item-display'
import type { WorkbenchItem } from '@/types/workbench'

/** ⌘K 里的一条工作台结果：条目（画布/数据库）或章节文档。 */
export interface CmdkWorkbenchHit {
  key: string
  kind: 'item' | 'document'
  id: string
  title: string
  /** 条目类型或文档版本，用于副标题 */
  subtitle: string
}

/**
 * 工作台搜索（P3-6）。
 *
 * 复用既有列表接口而不是新增 `/workbench/search`：
 *   - `GET /workbench/items?q=` 已同时匹配标题与正文投影（`text_content`）；
 *   - `GET /doc-assistant/documents?q=` 匹配章节文档标题。
 * 归档知识本身是工作知识库里的知识，已在「知识库文件」分组命中，这里不重复列。
 *
 * `query` 由调用方传入（⌘K 的输入框），本组合只负责防抖、并发与过期响应丢弃。
 */
export function useCmdkWorkbench(
  query: Ref<string>,
  options: { limit?: number; debounceMs?: number } = {},
) {
  const limit = options.limit ?? 5
  const debounceMs = options.debounceMs ?? 350

  const hits = ref<CmdkWorkbenchHit[]>([])
  const loading = ref(false)
  let requestSeq = 0
  let timer: ReturnType<typeof setTimeout> | null = null

  const runSearch = async (q: string, seq: number) => {
    loading.value = true
    const [itemsRes, docsRes] = await Promise.all([
      listWorkbenchItems({ q, limit }).catch(() => null),
      listDocDocumentsGlobal({ q, limit }).catch(() => null),
    ])
    // 过期响应直接丢弃：快速连打时只保留最后一次查询结果。
    if (seq !== requestSeq) return

    const out: CmdkWorkbenchHit[] = []
    for (const item of itemsRes?.items ?? []) {
      out.push(toHit(item))
    }
    for (const doc of docsRes?.data?.documents ?? []) {
      out.push({
        key: `doc:${doc.id}`,
        kind: 'document',
        id: doc.id,
        title: doc.title,
        // 版本号让同一任务的多个版本可区分（latest_only 默认只返回最新）。
        subtitle: `v${doc.version}`,
      })
    }
    hits.value = out.slice(0, limit)
    loading.value = false
  }

  watch(query, (val) => {
    if (timer) clearTimeout(timer)
    const trimmed = (val || '').trim()
    if (!trimmed) {
      requestSeq++
      hits.value = []
      loading.value = false
      return
    }
    timer = setTimeout(() => {
      void runSearch(trimmed, ++requestSeq)
    }, debounceMs)
  })

  const clear = () => {
    requestSeq++
    hits.value = []
    loading.value = false
  }

  const hasHits = computed(() => hits.value.length > 0)

  return { hits, loading, hasHits, clear }
}

/**
 * 条目 → 命中行。
 *
 * 标题与副标题都走 i18n：副标题复用列表里的 itemCaption（“表格 / 画布 · 文档”），
 * 这样 ⌘K 与列表不会出现两套说法，也不会漏出英文类型名。
 */
function toHit(item: WorkbenchItem): CmdkWorkbenchHit {
  const t = (key: string) => i18n.global.t(key)
  return {
    key: `item:${item.id}`,
    kind: 'item',
    id: item.id,
    title: item.title || t('workbench.editor.untitled'),
    subtitle: itemCaption(item, t),
  }
}
