/**
 * BlockSuite 斜杠菜单的中文化。
 *
 * 菜单文案来自上游硬编码的英文配置（Text / Heading / Quote…），
 * 这里在 `AffineSlashMenuWidget.configItemTransform` 上做一次运行期转换：
 *   - 简体 / 繁体中文按当前 i18n locale 翻译名称、描述、分组与 tooltip；
 *   - 其他语言保留上游英文（英文本身即正确）；
 *   - 同时把英文原名放进 searchAlias，保证中英文都能搜到。
 */
import i18n from '@/i18n'

type SlashMenuItemLike = {
  name: string
  description?: string
  group?: string
  searchAlias?: string[]
  tooltip?: { caption?: string; [key: string]: unknown }
  subMenu?: SlashMenuItemLike[]
  [key: string]: unknown
}

 /** 一份字典同时承载名称、描述与分组名；按原始英文值做 key。 */
const ZH_HANS: Record<string, string> = {
  Basic: '基础',
  List: '列表',
  Align: '对齐',
  Style: '样式',
  Text: '正文',
  'Start typing with plain text.': '开始输入纯文本。',
  'Heading 1': '标题 1',
  'Headings in the largest font.': '使用最大字号的标题。',
  'Heading 2': '标题 2',
  'Headings in the 2nd font size.': '使用第二级字号的标题。',
  'Heading 3': '标题 3',
  'Headings in the 3rd font size.': '使用第三级字号的标题。',
  'Heading 4': '标题 4',
  'Headings in the 4th font size.': '使用第四级字号的标题。',
  'Heading 5': '标题 5',
  'Headings in the 5th font size.': '使用第五级字号的标题。',
  'Heading 6': '标题 6',
  'Headings in the 6th font size.': '使用第六级字号的标题。',
  'Heading #1': '标题 1',
  'Heading #2': '标题 2',
  'Heading #3': '标题 3',
  'Heading #4': '标题 4',
  'Heading #5': '标题 5',
  'Heading #6': '标题 6',
  'Other Headings': '其他标题',
  'Bulleted List': '无序列表',
  'Create a bulleted list.': '创建无序列表。',
  'Numbered List': '有序列表',
  'Create a numbered list.': '创建有序列表。',
  'To-do List': '待办列表',
  'Add tasks to a to-do list.': '将任务添加到待办列表。',
  'Code Block': '代码块',
  'Code snippet with formatting.': '带格式的代码片段。',
  Equation: '公式',
  'Formula block with LaTeX rendering.': '使用 LaTeX 渲染的公式块。',
  Quote: '引用',
  'Add a blockquote for emphasis.': '添加引用以强调内容。',
  Divider: '分割线',
  'Visually separate content.': '在视觉上分隔内容。',
  'Align left': '左对齐',
  'Align center': '居中对齐',
  'Align right': '右对齐',
  Bold: '加粗',
  Italic: '斜体',
  Underline: '下划线',
  Strikethrough: '删除线',
  // 分组名：除 Basic/List/Align/Style 外，其余分组也要跟随语言
  Page: '页面',
  'Content & Media': '内容与媒体',
  Date: '日期',
  Database: '数据库',
  Actions: '操作',
  // Page 分组
  'New Doc': '新建文档',
  'Linked Doc': '链接文档',
  // Date 分组（描述是动态日期，不翻译）
  Today: '今天',
  Tomorrow: '明天',
  Yesterday: '昨天',
  Now: '现在',
  // Content & Media 分组
  Table: '表格',
  Image: '图片',
  Link: '链接',
  Attachment: '附件',
  Embed: '嵌入',
  'Inline equation': '行内公式',
  Callout: '高亮块',
  Frame: '画框',
  'Mind Map': '思维导图',
  // Database 分组
  'Table View': '表格视图',
  'Calendar View': '日历视图',
  'Kanban View': '看板视图',
  Todo: '待办',
  // Actions 分组
  'Move Up': '上移',
  'Move Down': '下移',
  Copy: '复制',
  Duplicate: '创建副本',
  Delete: '删除',
  // Content & Media / Page / Actions 的描述
  'For Google Drive, and more.': '用于 Google Drive 等。',
  'Create a inline equation.': '创建行内公式。',
  'Create a equation block.': '创建公式块。',
  'Let your words stand out.': '让内容更醒目。',
  'Create a simple table.': '创建简单表格。',
  'Embed a YouTube video.': '嵌入 YouTube 视频。',
  'Insert an image.': '插入图片。',
  'Start a new document.': '新建文档。',
  'Link to another document.': '链接到其他文档。',
  'Add a bookmark for reference.': '添加书签以便引用。',
  'Attach a file to document.': '将文件附加到文档。',
  'Upload a PDF to document.': '上传 PDF 到文档。',
  'Display items in a table format.': '以表格形式展示条目。',
  'Display items by date in a calendar.': '在日历中按日期展示条目。',
  'Visualize data in a dashboard.': '在看板中可视化数据。',
  'Embed a Loom video.': '嵌入 Loom 视频。',
  'Link to a GitHub repository.': '链接到 GitHub 仓库。',
  'Embed a Figma document.': '嵌入 Figma 文档。',
  'Insert a blank frame': '插入空白画框',
  'Insert a mind map': '插入思维导图',
  'Shift this line up.': '将本行上移。',
  'Shift this line down.': '将本行下移。',
  'Copy this line to clipboard.': '复制本行到剪贴板。',
  'Create a duplicate of this line.': '创建本行的副本。',
  'Remove this line permanently.': '永久删除本行。',
}

const ZH_HANT: Record<string, string> = {
  Basic: '基礎',
  List: '清單',
  Align: '對齊',
  Style: '樣式',
  Text: '內文',
  'Start typing with plain text.': '開始輸入純文字。',
  'Heading 1': '標題 1',
  'Headings in the largest font.': '使用最大字號的標題。',
  'Heading 2': '標題 2',
  'Headings in the 2nd font size.': '使用第二級字號的標題。',
  'Heading 3': '標題 3',
  'Headings in the 3rd font size.': '使用第三級字號的標題。',
  'Heading 4': '標題 4',
  'Headings in the 4th font size.': '使用第四級字號的標題。',
  'Heading 5': '標題 5',
  'Headings in the 5th font size.': '使用第五級字號的標題。',
  'Heading 6': '標題 6',
  'Headings in the 6th font size.': '使用第六級字號的標題。',
  'Heading #1': '標題 1',
  'Heading #2': '標題 2',
  'Heading #3': '標題 3',
  'Heading #4': '標題 4',
  'Heading #5': '標題 5',
  'Heading #6': '標題 6',
  'Other Headings': '其他標題',
  'Bulleted List': '無序清單',
  'Create a bulleted list.': '建立無序清單。',
  'Numbered List': '有序清單',
  'Create a numbered list.': '建立有序清單。',
  'To-do List': '待辦清單',
  'Add tasks to a to-do list.': '將任務加入待辦清單。',
  'Code Block': '程式碼區塊',
  'Code snippet with formatting.': '帶格式的程式碼片段。',
  Equation: '公式',
  'Formula block with LaTeX rendering.': '使用 LaTeX 呈現的公式區塊。',
  Quote: '引用',
  'Add a blockquote for emphasis.': '加入引用以強調內容。',
  Divider: '分隔線',
  'Visually separate content.': '在視覺上分隔內容。',
  'Align left': '左對齊',
  'Align center': '置中對齊',
  'Align right': '右對齊',
  Bold: '粗體',
  Italic: '斜體',
  Underline: '底線',
  Strikethrough: '刪除線',
  // 分組名：除 Basic/List/Align/Style 外，其餘分組也要跟隨語言
  Page: '頁面',
  'Content & Media': '內容與媒體',
  Date: '日期',
  Database: '資料庫',
  Actions: '操作',
  // Page 分組
  'New Doc': '新增文件',
  'Linked Doc': '連結文件',
  // Date 分組（描述是動態日期，不翻譯）
  Today: '今天',
  Tomorrow: '明天',
  Yesterday: '昨天',
  Now: '現在',
  // Content & Media 分組
  Table: '表格',
  Image: '圖片',
  Link: '連結',
  Attachment: '附件',
  Embed: '嵌入',
  'Inline equation': '行內公式',
  Callout: '醒目區塊',
  Frame: '畫框',
  'Mind Map': '心智圖',
  // Database 分組
  'Table View': '表格檢視',
  'Calendar View': '日曆檢視',
  'Kanban View': '看板檢視',
  Todo: '待辦',
  // Actions 分組
  'Move Up': '上移',
  'Move Down': '下移',
  Copy: '複製',
  Duplicate: '建立副本',
  Delete: '刪除',
  // Content & Media / Page / Actions 的描述
  'For Google Drive, and more.': '用於 Google Drive 等。',
  'Create a inline equation.': '建立行內公式。',
  'Create a equation block.': '建立公式區塊。',
  'Let your words stand out.': '讓內容更醒目。',
  'Create a simple table.': '建立簡單表格。',
  'Embed a YouTube video.': '嵌入 YouTube 影片。',
  'Insert an image.': '插入圖片。',
  'Start a new document.': '新增文件。',
  'Link to another document.': '連結至其他文件。',
  'Add a bookmark for reference.': '加入書籤以便引用。',
  'Attach a file to document.': '將檔案附加到文件。',
  'Upload a PDF to document.': '上傳 PDF 到文件。',
  'Display items in a table format.': '以表格形式展示條目。',
  'Display items by date in a calendar.': '在日曆中依日期展示條目。',
  'Visualize data in a dashboard.': '在看板中視覺化資料。',
  'Embed a Loom video.': '嵌入 Loom 影片。',
  'Link to a GitHub repository.': '連結至 GitHub 儲存庫。',
  'Embed a Figma document.': '嵌入 Figma 文件。',
  'Insert a blank frame': '插入空白畫框',
  'Insert a mind map': '插入心智圖',
  'Shift this line up.': '將本行上移。',
  'Shift this line down.': '將本行下移。',
  'Copy this line to clipboard.': '複製本行到剪貼簿。',
  'Create a duplicate of this line.': '建立本行的副本。',
  'Remove this line permanently.': '永久刪除本行。',
}

function dictionaryFor(locale: string): Record<string, string> | null {
  if (locale === 'zh-TW' || locale.startsWith('zh-Hant')) return ZH_HANT
  if (locale === 'zh-CN' || locale.startsWith('zh-Hans')) return ZH_HANS
  return null
}

/** 空段落占位：中文环境把 "Type '/' for commands" 也一起本地化。 */
export function localizedParagraphPlaceholder(model: {
  props?: { type?: string }
}): string {
  const type = String(model?.props?.type ?? 'text')
  const locale = String(i18n.global.locale.value ?? '')

  if (locale === 'zh-TW' || locale.startsWith('zh-Hant')) {
    if (type === 'text') return '輸入 / 使用命令'
    if (type === 'quote') return ''
    const level = Number(type.slice(1))
    return Number.isFinite(level) && level > 0 ? `標題 ${level}` : ''
  }
  if (locale === 'zh-CN' || locale.startsWith('zh-Hans')) {
    if (type === 'text') return '输入 / 使用命令'
    if (type === 'quote') return ''
    const level = Number(type.slice(1))
    return Number.isFinite(level) && level > 0 ? `标题 ${level}` : ''
  }

  const english: Record<string, string> = {
    text: "Type '/' for commands",
    h1: 'Heading 1',
    h2: 'Heading 2',
    h3: 'Heading 3',
    h4: 'Heading 4',
    h5: 'Heading 5',
    h6: 'Heading 6',
    quote: '',
  }
  return english[type] ?? ''
}

/** group 形如 `0_Basic@0`；替换分组名但保留排序位。 */
function translateGroup(group: string, dict: Record<string, string>): string {
  const [index, rest] = group.split('_')
  if (!rest) return group
  const [name, order] = rest.split('@')
  const translated = dict[name] ?? name
  return `${index}_${translated}${order !== undefined ? `@${order}` : ''}`
}

/** 递归翻译主菜单与子菜单（子菜单由上游二次构建，必须在这里一起转）。 */
export function translateSlashMenuItem<T extends SlashMenuItemLike>(item: T): T {
  const locale = String(i18n.global.locale.value ?? '')
  const dict = dictionaryFor(locale)
  if (!dict) return item

  const name = dict[item.name] ?? item.name
  const description = item.description ? dict[item.description] ?? item.description : item.description
  const group = item.group ? translateGroup(item.group, dict) : item.group
  const tooltip = item.tooltip?.caption
    ? { ...item.tooltip, caption: dict[item.tooltip.caption] ?? item.tooltip.caption }
    : item.tooltip
  // 保留英文原名作为别名，中文界面里输入 heading / quote 仍能命中。
  const searchAlias = [
    ...(item.searchAlias ?? []),
    ...(name !== item.name ? [name, item.name] : []),
  ]

  // 注意：上游用 `'subMenu' in item` 判断子菜单。
  // 普通菜单项如果也被带上 `subMenu: undefined` 键，会被误判成子菜单，
  // 递归时对 undefined 做 filter 直接抛错，整个斜杠菜单都建不出来。
  // 因此只有原本就是数组时才写这个键。
  const result = { ...item, name, description, group, tooltip, searchAlias } as T
  if (Array.isArray(item.subMenu)) {
    result.subMenu = item.subMenu.map((child) => translateSlashMenuItem(child)) as T['subMenu']
  }
  return result
}
