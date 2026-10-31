/**
 * 编辑器偏好（§9.14.2、§9.14.3）。
 *
 * 全部是个人视图偏好：不写 Y.Doc、不产生 snapshot_version、不把归档打成 stale。
 * P1 只存 localStorage，键带租户与用户，切账号/切空间不会串。
 *
 * 读取策略：先与默认值合并，再逐项校验白名单与数值范围；非法值一律回默认。
 * JSON 损坏不能让编辑器挂载失败——偏好是锦上添花，不是启动前提。
 */

export type FontFamily = 'default' | 'serif' | 'mono'
export type CreateMode = 'page' | 'edgeless'
export type PageWidth = 'standard' | 'wide'
export type CanvasScheme = 'app' | 'plain' | 'contrast'
export type NoteBackground = 'white' | 'cream' | 'transparent'
export type NoteRadius = 'none' | 'sm' | 'md' | 'lg'
export type NoteShadow = 'none' | 'box' | 'soft'
export type NoteBorderStyle = 'solid' | 'dashed' | 'none'

export interface EditorPrefs {
  /** Page 正文与标题字体 */
  fontFamily: FontFamily
  /** Page 基础字号，14–20 的整数 */
  fontSize: number
  /** 新建普通条目的初始形态；不影响 Journals 与数据库 */
  defaultCreateMode: CreateMode
  /** 普通条目标题留空时用设备本地日期命名 */
  autoNameWithDate: boolean
  /** 无图标条目显示「添加图标」入口 */
  showAddIcon: boolean
  /** Page 代码块默认显示行号 */
  codeLineNumbers: boolean
  /** Page 默认宽度 */
  pageWidth: PageWidth
  /** Page 标题下显示更新时间/字数/可见性 */
  showDocInfo: boolean
  /** Edgeless 背景与网格 */
  canvasScheme: CanvasScheme
  /** true：滚轮直接缩放；false：滚轮平移、Ctrl/⌘+滚轮缩放 */
  scrollZoom: boolean
  /** 新建笔记默认底色 */
  noteBackground: NoteBackground
  /** 新建笔记默认圆角 */
  noteRadius: NoteRadius
  /** 新建笔记默认阴影 */
  noteShadow: NoteShadow
  /** 新建笔记默认边框样式 */
  noteBorderStyle: NoteBorderStyle
  /** 新建笔记默认边框厚度，1–4 的整数 */
  noteBorderWidth: number
}

export const DEFAULT_EDITOR_PREFS: EditorPrefs = {
  fontFamily: 'default',
  fontSize: 16,
  defaultCreateMode: 'page',
  autoNameWithDate: false,
  showAddIcon: true,
  codeLineNumbers: true,
  pageWidth: 'standard',
  showDocInfo: true,
  canvasScheme: 'app',
  scrollZoom: false,
  noteBackground: 'white',
  noteRadius: 'sm',
  noteShadow: 'box',
  noteBorderStyle: 'none',
  noteBorderWidth: 1,
}

/** 字号与边框厚度的合法区间；越界钳制而不是报错。 */
export const FONT_SIZE_MIN = 14
export const FONT_SIZE_MAX = 20
export const BORDER_WIDTH_MIN = 1
export const BORDER_WIDTH_MAX = 4

const PREFIX = 'know.workbench.editor-prefs.v1'

/** 生成存储键；缺租户或用户时返回空串（不写全局键，避免跨账号串用）。 */
export function editorPrefsKey(tenantId: string | number, userId: string): string {
  if (!tenantId || !userId) return ''
  return `${PREFIX}:${tenantId}:${userId}`
}

/** 整数钳制；非有限数回落到默认值而不是下限。 */
function clampInt(value: unknown, min: number, max: number, fallback: number): number {
  const num = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(num)) return fallback
  return Math.min(max, Math.max(min, Math.round(num)))
}

/** 白名单校验：命中返回原值，否则回默认。 */
function pick<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback
}

/** 布尔校验：只接受真正的 boolean，字符串 "false" 不算。 */
function bool(value: unknown, fallback: boolean): boolean {
  return typeof value === 'boolean' ? value : fallback
}

export const FONT_FAMILIES: readonly FontFamily[] = ['default', 'serif', 'mono']
export const CREATE_MODES: readonly CreateMode[] = ['page', 'edgeless']
export const PAGE_WIDTHS: readonly PageWidth[] = ['standard', 'wide']
export const CANVAS_SCHEMES: readonly CanvasScheme[] = ['app', 'plain', 'contrast']
export const NOTE_BACKGROUNDS: readonly NoteBackground[] = ['white', 'cream', 'transparent']
export const NOTE_RADII: readonly NoteRadius[] = ['none', 'sm', 'md', 'lg']
export const NOTE_SHADOWS: readonly NoteShadow[] = ['none', 'box', 'soft']
export const NOTE_BORDER_STYLES: readonly NoteBorderStyle[] = ['solid', 'dashed', 'none']

/** 解析偏好：逐字段校验，未知字段忽略。 */
export function parseEditorPrefs(raw: unknown): EditorPrefs {
  const source = (raw ?? {}) as Record<string, unknown>
  const d = DEFAULT_EDITOR_PREFS
  return {
    fontFamily: pick(source.fontFamily, FONT_FAMILIES, d.fontFamily),
    fontSize: clampInt(source.fontSize, FONT_SIZE_MIN, FONT_SIZE_MAX, d.fontSize),
    defaultCreateMode: pick(source.defaultCreateMode, CREATE_MODES, d.defaultCreateMode),
    autoNameWithDate: bool(source.autoNameWithDate, d.autoNameWithDate),
    showAddIcon: bool(source.showAddIcon, d.showAddIcon),
    codeLineNumbers: bool(source.codeLineNumbers, d.codeLineNumbers),
    pageWidth: pick(source.pageWidth, PAGE_WIDTHS, d.pageWidth),
    showDocInfo: bool(source.showDocInfo, d.showDocInfo),
    canvasScheme: pick(source.canvasScheme, CANVAS_SCHEMES, d.canvasScheme),
    scrollZoom: bool(source.scrollZoom, d.scrollZoom),
    noteBackground: pick(source.noteBackground, NOTE_BACKGROUNDS, d.noteBackground),
    noteRadius: pick(source.noteRadius, NOTE_RADII, d.noteRadius),
    noteShadow: pick(source.noteShadow, NOTE_SHADOWS, d.noteShadow),
    noteBorderStyle: pick(source.noteBorderStyle, NOTE_BORDER_STYLES, d.noteBorderStyle),
    noteBorderWidth: clampInt(source.noteBorderWidth, BORDER_WIDTH_MIN, BORDER_WIDTH_MAX, d.noteBorderWidth),
  }
}

export function loadEditorPrefs(storage: Storage | null, key: string): EditorPrefs {
  if (!storage || !key) return { ...DEFAULT_EDITOR_PREFS }
  try {
    const raw = storage.getItem(key)
    if (!raw) return { ...DEFAULT_EDITOR_PREFS }
    return parseEditorPrefs(JSON.parse(raw))
  } catch {
    return { ...DEFAULT_EDITOR_PREFS }
  }
}

export function saveEditorPrefs(storage: Storage | null, key: string, prefs: EditorPrefs): void {
  if (!storage || !key) return
  try {
    storage.setItem(key, JSON.stringify(prefs))
  } catch {
    // 隐私模式或配额满：偏好写不进去只影响下次打开的默认值。
  }
}

/**
 * 把偏好投影成 CSS 变量（§9.14.3）。
 *
 * 只设置根节点变量，不遍历已有块、不调用 updateBlockProps：
 * 这样切换字体/字号/宽度对 Y.Doc 完全没有副作用。
 *
 * 两套变量：
 *   - `--wb-*` 是工作台自己的语义 token，供工作台页面样式使用；
 *   - `--affine-*` 是 BlockSuite Page 真正消费的变量，缺了它「改字号 Page 立即变化」
 *     不会生效（page-root-block.ts 用 `--affine-font-family` / `--affine-font-base` /
 *     `--affine-editor-width`）。
 */
export const FONT_STACKS: Record<FontFamily, string> = {
  // 空串表示「不覆盖」：让 BlockSuite / 全局主题用自己的默认字体栈。
  default: '',
  serif: 'Georgia, "Songti SC", "SimSun", serif',
  mono: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
}

export const PAGE_MAX_WIDTHS: Record<PageWidth, string> = {
  standard: '720px',
  wide: 'none',
}

/** BlockSuite 侧的页面宽度：全宽时铺满容器，左右留白由容器 padding 提供。 */
const AFFINE_EDITOR_WIDTHS: Record<PageWidth, string> = {
  standard: '720px',
  wide: '100%',
}

/** 圆角与阴影的像素值；none 表示 0。 */
export const NOTE_RADIUS_PX: Record<NoteRadius, number> = { none: 0, sm: 4, md: 8, lg: 12 }

export function editorPrefsStyle(prefs: EditorPrefs): Record<string, string> {
  const font = FONT_STACKS[prefs.fontFamily]
  const style: Record<string, string> = {
    '--wb-page-font-size': `${prefs.fontSize}px`,
    '--wb-page-max-width': PAGE_MAX_WIDTHS[prefs.pageWidth],
    '--wb-note-radius': `${NOTE_RADIUS_PX[prefs.noteRadius]}px`,
    '--wb-note-border-width': `${prefs.noteBorderWidth}px`,
    // BlockSuite 消费的那一份
    '--affine-font-base': `${prefs.fontSize}px`,
    '--affine-editor-width': AFFINE_EDITOR_WIDTHS[prefs.pageWidth],
  }
  // 字体选「默认」时不写这两个变量，避免把主题自带字体栈覆盖成空值。
  if (font) {
    style['--wb-page-font'] = font
    style['--affine-font-family'] = font
  }
  return style
}

/**
 * 新建笔记的初始块属性（§9.14.2）。
 *
 * 只在新建笔记的创建路径读取并写进该笔记自身的 Y.Doc；
 * 改偏好不追溯、不批量改写已有笔记。
 */
export interface NoteStyleProps {
  background: NoteBackground
  radius: NoteRadius
  shadow: NoteShadow
  borderStyle: NoteBorderStyle
  /** borderStyle=none 时仍保留上次的厚度，切回实线/虚线即恢复 */
  borderWidth: number
}

export function noteStyleFromPrefs(prefs: EditorPrefs): NoteStyleProps {
  return {
    background: prefs.noteBackground,
    radius: prefs.noteRadius,
    shadow: prefs.noteShadow,
    borderStyle: prefs.noteBorderStyle,
    borderWidth: clampInt(prefs.noteBorderWidth, BORDER_WIDTH_MIN, BORDER_WIDTH_MAX, 1),
  }
}

/** 笔记底色 → CSS 颜色（透明只去底色，不删画布背景）。 */
export const NOTE_BACKGROUND_COLORS: Record<NoteBackground, string> = {
  white: '#ffffff',
  cream: '#fbf6ec',
  transparent: 'transparent',
}

/** 阴影 → BlockSuite 主题变量；无阴影用空串（上游约定 ''.length === 0 即 none）。 */
export const NOTE_SHADOW_VARS: Record<NoteShadow, string> = {
  none: '',
  box: '--affine-note-shadow-box',
  soft: '--affine-note-shadow-film',
}

/**
 * 把偏好转成 BlockSuite 的初始块属性。
 *
 * 与 `noteStyleFromPrefs` 的区别：这里给的是**上游能直接吃的值**
 * （颜色字符串 / 像素 / StrokeStyle 枚举串 / 阴影变量名）。
 */
export function noteStyleSeedFromPrefs(prefs: EditorPrefs): {
  background: string
  borderRadius: number
  borderSize: number
  borderStyle: string
  shadowType: string
} {
  const style = noteStyleFromPrefs(prefs)
  return {
    background: NOTE_BACKGROUND_COLORS[style.background],
    borderRadius: NOTE_RADIUS_PX[style.radius],
    borderSize: style.borderWidth,
    borderStyle: style.borderStyle,
    shadowType: NOTE_SHADOW_VARS[style.shadow],
  }
}

/**
 * 空标题时的自动命名（§9.14.2）。
 *
 * 只生成普通条目标题：不写 journal_date、不创建 Journal、不进月历。
 * 用设备本地日期，不用 UTC（§9.14.4）。
 */
export function autoTitleFor(date: Date = new Date()): string {
  return `${date.getFullYear()}年${date.getMonth() + 1}月${date.getDate()}日`
}

/**
 * 新建普通条目时的标题：开了自动命名且用户没填标题才用日期。
 * 返回空串表示沿用后端默认「未命名」。
 */
export function resolveCreateTitle(prefs: EditorPrefs, input: string, now = new Date()): string {
  const trimmed = input.trim()
  if (trimmed) return trimmed
  return prefs.autoNameWithDate ? autoTitleFor(now) : ''
}
