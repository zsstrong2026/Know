/**
 * 数据库视图里上游硬编码文案的本地化（P3-1 体验补齐）。
 *
 * 背景：`<affine-database>` 的视图 Tab、行按钮、搜索框占位、日历空态都是上游写死的
 * 英文字符串，没有 i18n 入口，也不随界面语言变化。原型的中文界面里这些位置必须显示中文，
 * 否则同一屏出现「表格 / 看板 / 日历」与「New Record」混排。
 *
 * 做法：只在**已知的静态文案容器**里做精确文本替换（视图栏、工具条、日历空态）。
 * 不用整树扫描，是因为行标题与单元格值也是文本节点，整树替换会把用户写的
 * 「New Record」这种内容也改掉。替换后的文本不再命中字典，不会重复替换。
 */
import i18n from '@/i18n'

/** 允许替换文本的容器；命中任一祖先即处理。 */
const SAFE_SELECTORS = [
  'component-overflow',
  '.affine-database-toolbar',
  '.calendar-toolbar',
  '.calendar-empty-month-hint',
  '.calendar-new-row',
  '.data-view-table-group-add-row',
]

const SAFE_SELECTOR = SAFE_SELECTORS.join(',')

const ZH_HANS: Record<string, string> = {
  'New Record': '新建一行',
  'New row': '新建一行',
  'Search...': '搜索行…',
  Today: '今天',
  'Nothing here yet': '还没有内容',
  "Add a row to any date, it'll appear here on the calendar.": '在任意日期新建行，它会出现在这里。',
  'Add a date property': '添加日期属性',
  'Select a date property': '选择日期属性',
}

const ZH_HANT: Record<string, string> = {
  'New Record': '新建一列',
  'New row': '新建一列',
  'Search...': '搜尋列…',
  Today: '今天',
  'Nothing here yet': '還沒有內容',
  "Add a row to any date, it'll appear here on the calendar.": '在任意日期新建列，它會出現在這裡。',
  'Add a date property': '新增日期屬性',
  'Select a date property': '選擇日期屬性',
}

function dictionaryFor(locale: string): Record<string, string> | null {
  if (locale === 'zh-TW' || locale.startsWith('zh-Hant')) return ZH_HANT
  if (locale === 'zh-CN' || locale.startsWith('zh-Hans')) return ZH_HANS
  return null
}

/** 「N More」这类带数字的溢出入口，单独处理。 */
const MORE_PATTERN = /^(\d+)\s+More$/

function translate(raw: string, dict: Record<string, string>): string | null {
  const text = raw.trim()
  if (!text) return null
  const direct = dict[text]
  if (direct) return direct
  const more = MORE_PATTERN.exec(text)
  return more ? `还有 ${more[1]} 个` : null
}

/** 只替换文本节点本身，保留其父元素的样式与事件。 */
function walk(root: Node, dict: Record<string, string>): void {
  if (root.nodeType === Node.TEXT_NODE) {
    const next = translate(root.textContent ?? '', dict)
    if (next) (root as Text).data = next
    return
  }
  root.childNodes.forEach((child) => walk(child, dict))
}

/** 搜索框的文案在 placeholder 属性上，文本替换覆盖不到。 */
function translatePlaceholders(root: Element, dict: Record<string, string>): void {
  const inputs = root.matches('input[placeholder]')
    ? [root as HTMLInputElement]
    : Array.from(root.querySelectorAll<HTMLInputElement>('input[placeholder]'))
  for (const input of inputs) {
    const next = translate(input.placeholder, dict)
    if (next && next !== input.placeholder) input.placeholder = next
  }
}

/**
 * 就地处理一个节点。
 *
 * 分支顺序很关键：
 *   1. 元素本身在安全容器里 → 整棵子树翻译；
 *   2. 元素本身不在、但子树里有安全容器 → 只翻译那些容器（首次全量扫描走这里）；
 *   3. 文本节点 → 看父元素是否在安全容器内。
 * 只做第 3 步会让首次 `apply(host)` 完全空转（host 自己不在任何安全容器里），
 * 上游已经渲染完的文案就永远不会被替换。
 */
function apply(node: Node, dict: Record<string, string>): void {
  if (node.nodeType === Node.ELEMENT_NODE) {
    const el = node as Element
    if (el.closest(SAFE_SELECTOR)) {
      walk(el, dict)
      translatePlaceholders(el, dict)
      return
    }
    el.querySelectorAll(SAFE_SELECTOR).forEach((safe) => {
      walk(safe, dict)
      translatePlaceholders(safe, dict)
    })
    return
  }
  if (node.parentElement?.closest(SAFE_SELECTOR)) walk(node, dict)
}

/**
 * 在数据库宿主上安装文案本地化。
 *
 * 返回卸载函数；非中文界面直接空转（上游本身就是英文）。
 */
export function installDatabaseTextLocale(host: HTMLElement): () => void {
  const dict = dictionaryFor(String(i18n.global.locale.value ?? ''))
  if (!dict) return () => {}

  const observer = new MutationObserver((records) => {
    for (const record of records) {
      record.addedNodes.forEach((node) => apply(node, dict))
      // 上游会在已存在节点里改文案（搜索框展开/收起、月份切换），需要跟随
      if (record.type === 'characterData') apply(record.target, dict)
      if (record.type === 'attributes') translatePlaceholders(record.target as Element, dict)
    }
  })
  observer.observe(host, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['placeholder'],
  })
  apply(host, dict)

  return () => observer.disconnect()
}
