/**
 * 模板选择器的数据与状态逻辑（§9.16.5）。
 *
 * 与组件分离，便于单测：分类切换、搜索、我的模板加载失败、套用状态都在这里。
 * 缩略图是纯 CSS 结构示意，不创建任何 BlockSuite 实例。
 *
 * 刻意不 import `@/api/workbench`：那一层依赖 axios 与浏览器环境，
 * 会把 node 单测拖进无法解析的模块。接口由调用方以函数参数注入。
 */
import { BUILTIN_TEMPLATES, loadBuiltinTemplate, type BuiltinTemplate } from './builtin'
import { prepareTemplateSnapshot, type PreparedTemplateSnapshot } from './apply'
import type { WorkbenchTemplateSummary } from '@/types/workbench'

/** 选择器的分类标签：推荐 + 三个分类 + 我的模板。 */
export type TemplateTab = 'recommended' | 'work' | 'record' | 'writing' | 'mine'

export const TEMPLATE_TABS: readonly TemplateTab[] = [
  'recommended',
  'work',
  'record',
  'writing',
  'mine',
]

/** 选择器里的一张卡片；内置与我的模板统一成同一形状。 */
export interface TemplateCardItem {
  /** 内置模板用 id，我的模板用 `mine:<uuid>` */
  key: string
  kind: 'builtin' | 'mine'
  /** 展示名（我的模板直接用名称，内置模板走 i18n） */
  name: string
  nameKey?: string
  descriptionKey?: string
  description?: string
  category?: string
  /** 内置模板：用于取快照 */
  builtin?: BuiltinTemplate
  /** 我的模板：元数据 */
  mine?: WorkbenchTemplateSummary
}

/** 按分类过滤内置模板；推荐只取 recommended=true 的 4 个。 */
export function builtinCardsFor(tab: TemplateTab): TemplateCardItem[] {
  if (tab === 'mine') return []
  const list =
    tab === 'recommended'
      ? BUILTIN_TEMPLATES.filter((tpl) => tpl.recommended)
      : BUILTIN_TEMPLATES.filter((tpl) => tpl.category === tab)
  return list.map((tpl) => ({
    key: tpl.id,
    kind: 'builtin',
    name: tpl.id,
    nameKey: `workbench.templates.builtin.${tpl.id}.name`,
    descriptionKey: `workbench.templates.builtin.${tpl.id}.description`,
    category: tpl.category,
    builtin: tpl,
  }))
}

export function mineCards(list: WorkbenchTemplateSummary[]): TemplateCardItem[] {
  return list.map((tpl) => ({
    key: `mine:${tpl.id}`,
    kind: 'mine',
    name: tpl.name,
    description: tpl.description,
    mine: tpl,
  }))
}

/** 搜索：按名称 + 描述过滤，不跨标签合并结果。 */
export function filterCards(cards: TemplateCardItem[], keyword: string): TemplateCardItem[] {
  const q = keyword.trim().toLowerCase()
  if (!q) return cards
  return cards.filter((card) => {
    const haystack = `${card.name} ${card.description ?? ''} ${card.nameKey ?? ''}`.toLowerCase()
    return haystack.includes(q)
  })
}

/** 我的模板加载状态：失败只影响该标签，不阻塞内置模板。 */
export interface MineTemplatesState {
  loading: boolean
  error: boolean
  items: WorkbenchTemplateSummary[]
}

export async function fetchMineTemplates(
  load: () => Promise<WorkbenchTemplateSummary[]>,
): Promise<MineTemplatesState> {
  try {
    const items = await load()
    return { loading: false, error: false, items }
  } catch {
    return { loading: false, error: true, items: [] }
  }
}

/**
 * 取一张卡片的可套用快照。
 *
 * 内置模板走静态资产，我的模板走后端 content 接口；两者都过同一套校验，
 * 因此选择器不需要关心来源差异。
 */
export async function loadCardSnapshot(
  card: TemplateCardItem,
  loadMineContent: (id: string) => Promise<{ name: string; ydoc: string; editor_schema_version: number }>,
): Promise<PreparedTemplateSnapshot> {
  if (card.kind === 'builtin') {
    if (!card.builtin) throw new Error('builtin template metadata missing')
    const ydoc = await loadBuiltinTemplate(card.builtin)
    return prepareTemplateSnapshot({ name: card.builtin.id, ydoc, editor_schema_version: 1 })
  }
  const tpl = card.mine
  if (!tpl) throw new Error('template metadata missing')
  const content = await loadMineContent(tpl.id)
  return prepareTemplateSnapshot({
    name: content.name || tpl.name,
    ydoc: content.ydoc,
    editor_schema_version: content.editor_schema_version,
  })
}
