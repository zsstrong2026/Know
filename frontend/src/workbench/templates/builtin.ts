/**
 * 内置文档模板（§9.16.3）。
 *
 * 8 个中文高频模板，内容是 8 份 Y.Doc 快照。快照通过 `new URL(..., import.meta.url)`
 * 交给 Vite 处理成独立资源 URL —— 只在用户真的选中某个模板时才 fetch，
 * 普通打开文档不会下载任何 .ydoc。
 *
 * 不要用 `import.meta.glob`：单测在纯 Node 下跑，那里没有 Vite 的转换。
 * `new URL(...)` 在 Node 与 Vite 下都是合法语法，两边一致。
 */

/** 模板分类。推荐是跨分类的精选集合，不参与分类筛选。 */
export type TemplateCategory = 'work' | 'record' | 'writing'

export interface BuiltinTemplate {
  id: string
  /** i18n 键后缀：文案在 workbench.templates.builtin.<id>.name / .description */
  category: TemplateCategory
  /** 是否进「推荐」 */
  recommended: boolean
  /** 快照资源 URL；拿到后由调用方 fetch 成字节 */
  assetUrl: string
}

/** 8 个内置模板；顺序即选择器里的展示顺序。 */
export const BUILTIN_TEMPLATES: readonly BuiltinTemplate[] = [
  {
    id: 'meeting-notes',
    category: 'work',
    recommended: true,
    assetUrl: new URL('./assets/meeting-notes.ydoc', import.meta.url).href,
  },
  {
    id: 'project-brief',
    category: 'work',
    recommended: true,
    assetUrl: new URL('./assets/project-brief.ydoc', import.meta.url).href,
  },
  {
    id: 'weekly-plan',
    category: 'work',
    recommended: true,
    assetUrl: new URL('./assets/weekly-plan.ydoc', import.meta.url).href,
  },
  {
    id: 'okr',
    category: 'work',
    recommended: false,
    assetUrl: new URL('./assets/okr.ydoc', import.meta.url).href,
  },
  {
    id: 'weekly-report',
    category: 'work',
    recommended: false,
    assetUrl: new URL('./assets/weekly-report.ydoc', import.meta.url).href,
  },
  {
    id: 'interview-notes',
    category: 'record',
    recommended: false,
    assetUrl: new URL('./assets/interview-notes.ydoc', import.meta.url).href,
  },
  {
    id: 'reading-notes',
    category: 'record',
    recommended: true,
    assetUrl: new URL('./assets/reading-notes.ydoc', import.meta.url).href,
  },
  {
    id: 'article-draft',
    category: 'writing',
    recommended: false,
    assetUrl: new URL('./assets/article-draft.ydoc', import.meta.url).href,
  },
]

export function findBuiltinTemplate(id: string): BuiltinTemplate | undefined {
  return BUILTIN_TEMPLATES.find((tpl) => tpl.id === id)
}

/**
 * 取内置模板快照的 base64。
 *
 * 失败时抛错而不是返回空，让选择器显示「模板加载失败 + 重试」，
 * 而不是静默套用一个空文档。
 */
export async function loadBuiltinTemplate(tpl: BuiltinTemplate): Promise<string> {
  const resp = await fetch(tpl.assetUrl)
  if (!resp.ok) throw new Error(`template asset ${tpl.id} responded ${resp.status}`)
  const buf = new Uint8Array(await resp.arrayBuffer())
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < buf.length; i += chunk) {
    binary += String.fromCharCode(...buf.subarray(i, i + chunk))
  }
  return btoa(binary)
}
