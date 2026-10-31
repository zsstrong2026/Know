/**
 * Page AI 动作预设表（§9.15.2、§11.2 的「Page 菜单预设对照」）。
 *
 * 这是 Page AI 菜单的**唯一来源**：每个预设只编译成六个 action 之一，
 * 不新增 action、不加 preset_id。服务端只认 action 与 target_lang，
 * 预设 instruction 一律当作用户内容处理（不是系统提示，也不是安全边界）。
 */
import type { InlineAIAction } from '@/workbench/ai/inline-ai-types'

/** 三种写入方式（§9.15.3）：替换选区 / 插入下方 / 仅解释。 */
export type PageAIWriteMode = 'replace' | 'below' | 'aside'

/** 五个菜单分组，顺序即渲染顺序。 */
export type PageAIGroupId = 'review' | 'edit' | 'generate' | 'draft' | 'other'

export interface PageAIAction {
  /** 稳定 id，供日志与测试断言；不发给服务端。 */
  id: string
  group: PageAIGroupId
  /** i18n key，六语言必须同增同删。 */
  labelKey: string
  /** 后端 action；`other.chat` 没有（不发请求）。 */
  action?: InlineAIAction
  /** 预设 instruction；作为用户内容发送，不伪装成系统提示。 */
  instruction?: string
  write: PageAIWriteMode
  /** 子菜单 id：translate / tone。 */
  submenu?: 'translate' | 'tone'
}

/** 翻译子菜单的六种语言码（§9.15.2 补充规则）。 */
export const TRANSLATE_LANGS = ['en', 'zh-Hans', 'zh-Hant', 'ja', 'ko', 'fr'] as const
export type TranslateLang = (typeof TRANSLATE_LANGS)[number]

/** 语气子菜单的五个选项；顺序即渲染顺序。 */
export const TONE_OPTIONS = ['professional', 'concise', 'friendly', 'confident', 'casual'] as const
export type ToneOption = (typeof TONE_OPTIONS)[number]

const TRANSLATE_LANG_NAMES: Record<TranslateLang, string> = {
  en: '英语',
  'zh-Hans': '简体中文',
  'zh-Hant': '繁体中文',
  ja: '日语',
  ko: '韩语',
  fr: '法语',
}

const TONE_NAMES: Record<ToneOption, string> = {
  professional: '专业',
  concise: '简洁',
  friendly: '友好',
  confident: '自信',
  casual: '轻松',
}

/** 语气 → 预设 instruction。事实、负责人、数字与截止时间必须不变。 */
const TONE_INSTRUCTIONS: Record<ToneOption, string> = {
  professional: '把语气改为专业、书面；事实、负责人、数字与截止时间不变；不新增信息。',
  concise: '把语气改为简洁、直给；事实、负责人、数字与截止时间不变；不删关键约束。',
  friendly: '把语气改为友好、亲和；事实、负责人、数字与截止时间不变；不添加客套话以外的内容。',
  confident: '把语气改为自信、肯定；事实、负责人、数字与截止时间不变；不夸大结论。',
  casual: '把语气改为轻松、口语；事实、负责人、数字与截止时间不变；保持原意。',
}

/** 19 个主项（含 3 个无 action 的分组项）。顺序与原型第 12 屏一致。 */
export const PAGE_AI_ACTIONS: readonly PageAIAction[] = [
  // 审阅文本
  {
    id: 'review.spelling',
    group: 'review',
    labelKey: 'workbench.pageAi.action.spelling',
    action: 'custom',
    instruction: '只修正错别字与拼写；不改措辞、事实与 Markdown 结构；没有错误时原样返回并说明。',
    write: 'replace',
  },
  {
    id: 'review.grammar',
    group: 'review',
    labelKey: 'workbench.pageAi.action.grammar',
    action: 'custom',
    instruction: '只修正语法、标点与明显病句；不扩写、不删信息。',
    write: 'replace',
  },
  {
    id: 'review.explain',
    group: 'review',
    labelKey: 'workbench.pageAi.action.explain',
    action: 'custom',
    instruction: '只解释含义、结构与使用场景；不修改原文、不编造事实。',
    write: 'aside',
  },
  // 编辑文本
  {
    id: 'edit.translate',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.translate',
    action: 'translate',
    write: 'replace',
    submenu: 'translate',
  },
  {
    id: 'edit.tone',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.tone',
    action: 'custom',
    write: 'replace',
    submenu: 'tone',
  },
  {
    id: 'edit.polish',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.polish',
    action: 'polish',
    instruction: '只返回润色后的文本；保留结构与专有名词；不解释。',
    write: 'replace',
  },
  {
    id: 'edit.expand',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.expand',
    action: 'custom',
    instruction: '在不新增事实的前提下补充执行顺序、条件与细节；不超过原文两倍。',
    write: 'replace',
  },
  {
    id: 'edit.shorten',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.shorten',
    action: 'custom',
    instruction: '压缩到原文一半以内；保留数字、负责人、截止时间与结论。',
    write: 'replace',
  },
  {
    id: 'edit.continue',
    group: 'edit',
    labelKey: 'workbench.pageAi.action.continue',
    action: 'continue',
    instruction: '延续原文语气与视角；最多 300 字；不重复原文。',
    write: 'below',
  },
  // 从文本生成
  {
    id: 'generate.summarize',
    group: 'generate',
    labelKey: 'workbench.pageAi.action.summarize',
    action: 'summarize',
    instruction: '3–5 条要点或一段摘要；不编造原文没有的事实。',
    write: 'below',
  },
  {
    id: 'generate.headings',
    group: 'generate',
    labelKey: 'workbench.pageAi.action.headings',
    action: 'custom',
    instruction: '生成 3–5 个候选标题，动宾结构，不改变事实；以列表返回。',
    write: 'below',
  },
  {
    id: 'generate.outline',
    group: 'generate',
    labelKey: 'workbench.pageAi.action.outline',
    action: 'outline',
    instruction: 'Markdown 标题提纲；层级不超过 3 层。',
    write: 'below',
  },
  {
    id: 'generate.actions',
    group: 'generate',
    labelKey: 'workbench.pageAi.action.extractActions',
    action: 'custom',
    instruction: '提取行动项，含负责人与截止时间；原文没有的标注「未指定」。只输出文字，不创建任务。',
    write: 'below',
  },
  {
    id: 'generate.brainstorm',
    group: 'generate',
    labelKey: 'workbench.pageAi.action.brainstorm',
    action: 'custom',
    instruction: '给出 3–5 个文字想法；不生成思维导图、不创建画布元素。',
    write: 'below',
  },
  // 起草文本
  {
    id: 'draft.article',
    group: 'draft',
    labelKey: 'workbench.pageAi.action.article',
    action: 'custom',
    instruction: '以选中文本为素材写一段文章；不引入外部事实；结构完整。',
    write: 'below',
  },
  {
    id: 'draft.tweet',
    group: 'draft',
    labelKey: 'workbench.pageAi.action.tweet',
    action: 'custom',
    instruction: '以选中文本为素材写一条短推文；保留一个明确时间点；不超过 280 字。',
    write: 'below',
  },
  {
    id: 'draft.poem',
    group: 'draft',
    labelKey: 'workbench.pageAi.action.poem',
    action: 'custom',
    instruction: '以选中文本的意象写四行短诗；不添加原文没有的事实。',
    write: 'below',
  },
  {
    id: 'draft.blog',
    group: 'draft',
    labelKey: 'workbench.pageAi.action.blog',
    action: 'custom',
    instruction: '以选中文本为结构写博客开篇；不访问网络、不引用外部链接。',
    write: 'below',
  },
  // 其他
  {
    id: 'other.chat',
    group: 'other',
    labelKey: 'workbench.pageAi.action.chat',
    write: 'aside',
  },
]

/** 分组的渲染顺序与 i18n key。 */
export const PAGE_AI_GROUPS: readonly { id: PageAIGroupId; labelKey: string }[] = [
  { id: 'review', labelKey: 'workbench.pageAi.group.review' },
  { id: 'edit', labelKey: 'workbench.pageAi.group.edit' },
  { id: 'generate', labelKey: 'workbench.pageAi.group.generate' },
  { id: 'draft', labelKey: 'workbench.pageAi.group.draft' },
  { id: 'other', labelKey: 'workbench.pageAi.group.other' },
]

/** 按分组取动作，顺序保持表内声明顺序。 */
export function actionsByGroup(group: PageAIGroupId): PageAIAction[] {
  return PAGE_AI_ACTIONS.filter((action) => action.group === group)
}

/** 翻译 / 语气子菜单的展开结果。 */
export interface PageAISubmenuItem {
  id: string
  label: string
  instruction?: string
  targetLang?: TranslateLang
  action: InlineAIAction
  write: PageAIWriteMode
}

/** 展开子菜单项；未知 submenu 返回空数组（不是全量）。 */
export function submenuItems(submenu: 'translate' | 'tone' | undefined): PageAISubmenuItem[] {
  if (submenu === 'translate') {
    return TRANSLATE_LANGS.map((lang) => ({
      id: `edit.translate.${lang}`,
      label: TRANSLATE_LANG_NAMES[lang],
      targetLang: lang,
      action: 'translate' as InlineAIAction,
      write: 'replace' as PageAIWriteMode,
    }))
  }
  if (submenu === 'tone') {
    return TONE_OPTIONS.map((tone) => ({
      id: `edit.tone.${tone}`,
      label: TONE_NAMES[tone],
      instruction: TONE_INSTRUCTIONS[tone],
      action: 'custom' as InlineAIAction,
      write: 'replace' as PageAIWriteMode,
    }))
  }
  return []
}

/** 翻译语言码 → 中文名；未知码原样返回，保证子菜单标题不缺字。 */
export function translateLangName(lang: string): string {
  return TRANSLATE_LANG_NAMES[lang as TranslateLang] ?? lang
}

/** 语气 id → 中文名；未知 id 原样返回。 */
export function toneName(tone: string): string {
  return TONE_NAMES[tone as ToneOption] ?? tone
}

/**
 * 把一个动作 + 子菜单选择编译成请求参数。
 *
 * `other.chat` 返回 null（不发请求）；子菜单动作必须带 submenuId。
 */
export function compilePageAIAction(
  action: PageAIAction,
  submenuId?: string,
): {
  action: InlineAIAction
  instruction?: string
  targetLang?: TranslateLang
  write: PageAIWriteMode
  labelKey: string
  labelArgs?: Record<string, string>
} | null {
  if (!action.action) return null

  if (action.submenu) {
    if (!submenuId) return null
    const item = submenuItems(action.submenu).find((candidate) => candidate.id === submenuId)
    if (!item) return null
    return {
      action: item.action,
      instruction: item.instruction,
      targetLang: item.targetLang,
      write: item.write,
      labelKey: action.labelKey,
      labelArgs: { value: item.label },
    }
  }

  return {
    action: action.action,
    instruction: action.instruction,
    write: action.write,
    labelKey: action.labelKey,
  }
}
