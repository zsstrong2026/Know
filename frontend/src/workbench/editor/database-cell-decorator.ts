/**
 * 数据库单元格装饰层（对齐 AFFiNE-Know原型.html 的事项表格）。
 *
 * 上游把「状态 / 优先级」都渲染成同一个标签组件（只有一个背景色可配），
 * 负责人是纯文本、日期没有逾期态、空值没有占位符。要做到原型的效果，
 * 只能在渲染完成后给节点补 `data-*` 标记，视觉交给 CSS：
 *
 *   - 状态：按选项色 → 待开始/进行中/已完成；
 *   - 优先级：把胶囊变成「圆点 + 文字」；
 *   - 负责人：取首字渲染成圆形头像；
 *   - 截止日：早于今天标红；
 *   - 空单元格：补一个「—」。
 *
 * 只读 DOM、不改数据与交互。上游重渲染会丢掉这些标记，所以用 MutationObserver 补齐。
 *
 * 两条硬约束（对应「优先级闪动」与「录入不跟手」两个历史问题）：
 *   1. 判定语义只读**内联 style**，绝不调 `getComputedStyle`：本层自己写的 CSS
 *      会改标签底色，用计算后样式反推会形成「加标记 → 底色变 → 摘标记 → 底色复原」
 *      的死循环，视觉上就是一直闪；而且每调一次都强制同步布局。
 *   2. 编辑中的单元格一律不碰（连属性都不写）：否则伪元素「—」会盖在输入框上，
 *      看起来像「打字没反应」。
 */
import i18n from '@/i18n'

import { WORKBENCH_DB_COLUMNS } from './database-columns'
import { UNGROUPED_GROUP_KEY } from './database-view-defaults'

/**
 * 选项颜色 → 语义。
 *
 * 取值来自 seed 写进列定义的 `var(--affine-tag-*)`（见 database-seed.ts），
 * 因此按**颜色名**判断而不是渲染后的 rgb：既兼容 `--affine-tag-*` 与
 * `--affine-v2-chip-label-*` 两种写法，也不受本层 CSS 覆盖的影响。
 */
const STATUS_BY_COLOR: Record<string, string> = {
  gray: 'todo',
  blue: 'doing',
  green: 'done',
}

const PRIORITY_BY_COLOR: Record<string, string> = {
  red: 'p0',
  orange: 'p1',
  gray: 'p2',
}

/** 旧条目里可能把颜色存成字面量，这里给一份 rgb/hex → 颜色名的兜底映射。 */
const LITERAL_COLORS: Record<string, string> = {
  'rgb(238, 240, 245)': 'gray',
  '#eef0f5': 'gray',
  'rgb(234, 242, 255)': 'blue',
  '#eaf2ff': 'blue',
  'rgb(230, 247, 239)': 'green',
  '#e6f7ef': 'green',
  'rgb(253, 236, 236)': 'red',
  '#fdecec': 'red',
  'rgb(255, 243, 224)': 'orange',
  '#fff3e0': 'orange',
}

const { status: STATUS_COLUMN, priority: PRIORITY_COLUMN } = WORKBENCH_DB_COLUMNS
const OWNER_COLUMN = WORKBENCH_DB_COLUMNS.owner
const DATE_COLUMN = WORKBENCH_DB_COLUMNS.date

/** 需要补「—」空值占位的列（标题列自带文档图标，不补）。 */
const EMPTY_PLACEHOLDER_COLUMNS = new Set<string>([
  WORKBENCH_DB_COLUMNS.status,
  WORKBENCH_DB_COLUMNS.priority,
  WORKBENCH_DB_COLUMNS.owner,
  WORKBENCH_DB_COLUMNS.date,
  WORKBENCH_DB_COLUMNS.relation,
  WORKBENCH_DB_COLUMNS.source,
  WORKBENCH_DB_COLUMNS.sourceDoc,
])

/** 正在编辑的单元格：里面有获得焦点的可编辑元素，或上游已标记 editing。 */
function isEditing(cell: Element): boolean {
  // 上游在进入编辑态时给单元格打 data-editing；用它比只看焦点更早生效，
  // 避免「刚点开、还没聚焦」的那一帧被补上伪元素而把光标挤偏。
  if (cell.closest('[data-editing="true"]')) return true
  const active = document.activeElement
  return Boolean(active && cell.contains(active) && active.getAttribute('contenteditable') === 'true')
}

/** 单元格里的标签元素（状态/优先级都是 .select-selected）。 */
function tagOf(cell: Element): HTMLElement | null {
  return cell.querySelector<HTMLElement>('.select-selected')
}

/**
 * 读标签的**内联**底色，并归一成颜色名。
 *
 * 内联 style 是上游按选项色写死的，不受任何 `!important` 覆盖，读取也没有布局开销。
 */
function colorOf(element: Element | null): string {
  if (!element) return ''
  const inline = (element as HTMLElement).style.backgroundColor.trim()
  if (!inline) return ''
  const token = /--affine(?:-v2)?-[a-z-]*?(red|orange|gray|grey|blue|green|yellow|purple|teal|pink|magenta)/.exec(inline)
  if (token) return token[1] === 'grey' ? 'gray' : token[1]
  return LITERAL_COLORS[inline] ?? ''
}

/** 只在值真的变化时写属性：同值反复 setAttribute 会持续产生 DOM 变更与重排。 */
function setAttr(el: Element, name: string, value: string | null): void {
  if (value === null) {
    if (el.hasAttribute(name)) el.removeAttribute(name)
    return
  }
  if (el.getAttribute(name) !== value) el.setAttribute(name, value)
}

/**
 * 解析日期文本里的年月日。
 *
 * 上游日期单元格按当前语言渲染，实际见过 `2026/11/10`（斜杠），
 * 也可能是 `2026-11-10`、`2026.11.10`、`2026年11月10日`；
 * 这里统一按「年 + 分隔符 + 月 + 分隔符 + 日」解析，避免只认连字符时
 * 逾期判定整列失效。
 */
function parseDateParts(text: string): [number, number, number] | null {
  const match = /(\d{4})\s*[-/年.]\s*(\d{1,2})\s*[-/月.]\s*(\d{1,2})/.exec(text.trim())
  if (!match) return null
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

/** 是否早于今天；只比较日期部分，避免时区干扰。 */
function isOverdue(text: string): boolean {
  const parts = parseDateParts(text)
  if (!parts) return false
  const value = new Date(parts[0], parts[1] - 1, parts[2]).getTime()
  const today = new Date()
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()
  return value < start
}

/**
 * 看板卡片的日期只显示「月-日」。
 *
 * 上游日期单元格渲染的是当前语言的完整日期（2026/11/12、2026年11月12日…），
 * 原型卡片用的是紧凑的 11-12；这里解析出月日，交给 CSS 的 `content: attr()` 展示。
 */
function shortDate(text: string): string | null {
  const parts = parseDateParts(text)
  if (!parts) return null
  return `${String(parts[1]).padStart(2, '0')}-${String(parts[2]).padStart(2, '0')}`
}

function decorate(cell: Element): void {
  const columnId = cell.getAttribute('data-column-id')
  if (!columnId) return

  if (isEditing(cell)) {
    // 编辑态必须摘掉空值占位：`::after` 的「—」会盖在输入框/光标位置上，
    // 表现为「在列表里录入时输入框不显示内容」。
    setAttr(cell, 'data-wb-empty', null)
    return
  }

  if (columnId === STATUS_COLUMN) {
    // 标记写在标签自身：CSS 用的是 `.select-selected[data-wb-tone=…]`。
    const tag = tagOf(cell)
    if (tag) setAttr(tag, 'data-wb-tone', STATUS_BY_COLOR[colorOf(tag)] ?? null)
  }

  if (columnId === PRIORITY_COLUMN) {
    const tag = tagOf(cell)
    if (tag) setAttr(tag, 'data-wb-prio', PRIORITY_BY_COLOR[colorOf(tag)] ?? null)
  }

  if (columnId === OWNER_COLUMN) {
    const initial = (cell.textContent ?? '').replace(/\s+/g, '').slice(0, 1)
    setAttr(cell, 'data-wb-initial', initial || null)
  }

  if (columnId === DATE_COLUMN) {
    const text = cell.textContent ?? ''
    const overdue = isOverdue(text)
    setAttr(cell, 'data-wb-late', overdue ? 'true' : null)
    // 卡片里的日期换成原型的「11-12 / 已逾期」，具体展示交给 CSS。
    if (cell.closest('affine-data-view-kanban-card')) {
      const label = overdue
        ? i18n.global.t('workbench.database.kanban.overdue')
        : shortDate(text)
      setAttr(cell, 'data-wb-date', label && label !== '' ? label : null)
    }
  }

  // 空值占位：只处理事项表自己的列，避免用户新增的列也被补「—」。
  // 判定只看文本，**不能**把类型图标（svg）当成内容：空 select 单元格里
  // 只有一个类型图标，旧逻辑会把它误判成「有内容」，看板卡片就留下一排空行。
  if (EMPTY_PLACEHOLDER_COLUMNS.has(columnId)) {
    const hasTagText = Array.from(cell.querySelectorAll('.select-selected')).some(
      (tag) => (tag.textContent ?? '').trim() !== '',
    )
    const hasMedia = Boolean(cell.querySelector('img, input, button'))
    const empty = !hasTagText && !hasMedia && (cell.textContent ?? '').trim() === ''
    setAttr(cell, 'data-wb-empty', empty ? 'true' : null)
  }
}

/**
 * 看板列头补上状态语义（`data-wb-tone`），并把「未分组」列改成当前语言。
 *
 * 上游列头里的标签用的是选项自身的底色，配色与表格里的状态胶囊同源；
 * 因此按同一套颜色映射反推语义，列顺序变化时圆点颜色也不会错。
 * 未分组列的标题是上游拼出来的 `No ${列名}`（中文界面即「No 状态」），
 * 这里替换成「未设置状态」，避免中英混排；有卡片的未分组列仍会显示。
 */
function decorateKanbanGroup(group: Element): void {
  const tag = group.querySelector<HTMLElement>('.group-header .tag, .group-header .select-selected')
  setAttr(group, 'data-wb-tone', STATUS_BY_COLOR[colorOf(tag)] ?? null)

  if (group.getAttribute('data-key') !== UNGROUPED_GROUP_KEY) return
  const label = i18n.global.t('workbench.database.kanban.ungrouped')
  const header = group.querySelector('.group-header')
  if (!header) return
  const walker = document.createTreeWalker(header, NodeFilter.SHOW_TEXT)
  while (walker.nextNode()) {
    const node = walker.currentNode as Text
    // 上游固定是 `No ${列名}`；替换后不再命中，不会反复触发观察器。
    if (/^No\s+\S/.test(node.data)) node.data = label
  }
}

/**
 * 给宿主打「事项表」标记。
 *
 * 只有事项表带固定列（wb-col-*）和三档状态（wb-opt-*）。卡片精简、
 * 隐藏「新建分组」等原型样式只应对它生效，不能影响用户自建的数据库看板。
 */
function markThingsHost(root: ParentNode): void {
  if (!(root instanceof HTMLElement)) return
  const isThings = Boolean(
    root.querySelector('[data-column-id="wb-col-status"]') ??
      root.querySelector('affine-data-view-kanban-group[data-key="wb-opt-todo"]'),
  )
  setAttr(root, 'data-wb-things', isThings ? 'true' : null)
}

function decorateAll(root: ParentNode): void {
  markThingsHost(root)
  root
    .querySelectorAll(
      'dv-table-view-cell-container[data-column-id], affine-data-view-kanban-cell[data-column-id]',
    )
    .forEach(decorate)
  root.querySelectorAll('affine-data-view-kanban-group').forEach(decorateKanbanGroup)
}

/** 目标节点是否落在正在编辑的单元格里。 */
function isInsideEditing(target: Node): boolean {
  const el = target.nodeType === Node.ELEMENT_NODE ? (target as Element) : target.parentElement
  return Boolean(el?.closest('[data-editing="true"]'))
}

/**
 * 在数据库宿主上安装装饰层，返回卸载函数。
 *
 * 变更批次若全部落在编辑中的单元格里（打字 → characterData 连续变化），
 * 直接跳过：既不干扰输入，也避免每按键都全量扫一遍。
 *
 * 装饰**同步**执行（不再排队到下一帧）：MutationObserver 回调发生在绘制之前，
 * 在这里补齐标记，标签的首帧就已经是「圆点 + 文字」/ 原型配色，
 * 不会先闪一下上游的橙色胶囊再变形。
 */
export function installDatabaseCellDecorator(host: HTMLElement): () => void {
  const observer = new MutationObserver((records) => {
    if (records.length && records.every((record) => isInsideEditing(record.target))) return
    decorateAll(host)
  })
  observer.observe(host, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    // 只关心可能改变「值 → 语义」的属性；data-wb-* 由本模块自己写，故意不监听，
    // 否则每次写标记都会再触发一轮，形成自我循环。
    attributeFilter: ['data-column-id', 'style'],
  })
  decorateAll(host)

  return () => observer.disconnect()
}
