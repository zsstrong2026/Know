/**
 * 数据库单元格装饰层（对齐 AFFiNE-Know原型.html 的事项表格）。
 *
 * 上游把「状态 / 优先级」都渲染成同一个标签组件（只有一个背景色可配），
 * 负责人是纯文本、日期没有逾期态、空值没有占位符。要做到原型的效果，
 * 只能在渲染完成后给节点补 `data-*` 标记，视觉交给 CSS：
 *
 *   - 状态：按标签背景色反推待开始/进行中/已完成 → 文字用原型色；
 *   - 优先级：把胶囊变成「圆点 + 文字」；
 *   - 负责人：取首字渲染成圆形头像；
 *   - 截止日：早于今天标红；
 *   - 空单元格：补一个「—」。
 *
 * 只读 DOM、不改数据与交互；正在编辑的单元格一律跳过，避免和输入框抢内容。
 * 上游重渲染会丢掉这些标记，所以用 MutationObserver 持续补齐。
 */

/** 标签背景色 → 语义。颜色取自 :root 里补齐的 --affine-tag-*。 */
const STATUS_TONES: Record<string, string> = {
  'rgb(238, 240, 245)': 'todo',
  'rgb(234, 242, 255)': 'doing',
  'rgb(230, 247, 239)': 'done',
}

const PRIORITY_TONES: Record<string, string> = {
  'rgb(253, 236, 236)': 'p0',
  'rgb(255, 243, 224)': 'p1',
  'rgb(238, 240, 245)': 'p2',
}

const STATUS_COLUMN = 'wb-col-status'
const PRIORITY_COLUMN = 'wb-col-priority'
const OWNER_COLUMN = 'wb-col-owner'
const DATE_COLUMN = 'wb-col-date'
const TITLE_COLUMN = 'wb-col-title'

/** 正在编辑的单元格：里面有获得焦点的可编辑元素。 */
function isEditing(cell: Element): boolean {
  const active = document.activeElement
  return Boolean(active && cell.contains(active) && active.getAttribute('contenteditable') === 'true')
}

/** 单元格里的标签元素（状态/优先级都是 .select-selected）。 */
function tagOf(cell: Element): HTMLElement | null {
  return cell.querySelector<HTMLElement>('.select-selected')
}

function backgroundOf(element: Element | null): string {
  return element ? getComputedStyle(element).backgroundColor : ''
}

/** 日期文本形如 2026-10-30；只比较日期部分，避免时区干扰。 */
function isOverdue(text: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(text.trim())
  if (!match) return false
  const value = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])).getTime()
  const today = new Date()
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()
  return value < start
}

function decorate(cell: Element): void {
  const columnId = cell.getAttribute('data-column-id')
  if (!columnId || isEditing(cell)) return

  if (columnId === STATUS_COLUMN) {
    const tone = STATUS_TONES[backgroundOf(tagOf(cell))]
    if (tone) cell.setAttribute('data-wb-tone', tone)
    else cell.removeAttribute('data-wb-tone')
  }

  if (columnId === PRIORITY_COLUMN) {
    const tone = PRIORITY_TONES[backgroundOf(tagOf(cell))]
    const tag = tagOf(cell)
    if (tone && tag) tag.setAttribute('data-wb-prio', tone)
    else tag?.removeAttribute('data-wb-prio')
  }

  if (columnId === OWNER_COLUMN) {
    const initial = (cell.textContent ?? '').replace(/\s+/g, '').slice(0, 1)
    if (initial) cell.setAttribute('data-wb-initial', initial)
    else cell.removeAttribute('data-wb-initial')
  }

  if (columnId === DATE_COLUMN) {
    const late = isOverdue(cell.textContent ?? '')
    if (late) cell.setAttribute('data-wb-late', 'true')
    else cell.removeAttribute('data-wb-late')
  }

  // 空值占位：标题列已经有文档图标，不补「—」。
  if (columnId !== TITLE_COLUMN) {
    const empty = (cell.textContent ?? '').trim() === ''
    if (empty) cell.setAttribute('data-wb-empty', 'true')
    else cell.removeAttribute('data-wb-empty')
  }
}

function decorateAll(root: ParentNode): void {
  root
    .querySelectorAll(
      'dv-table-view-cell-container[data-column-id], affine-data-view-kanban-cell[data-column-id]',
    )
    .forEach(decorate)
}

/**
 * 在数据库宿主上安装装饰层，返回卸载函数。
 *
 * 宿主内任何 DOM 变化都可能是上游重渲染，直接全量补齐（行数在几十条量级，
 * 开销可忽略）；用 requestAnimationFrame 合并同一帧内的多次变化。
 */
export function installDatabaseCellDecorator(host: HTMLElement): () => void {
  let frame = 0
  const schedule = () => {
    if (frame) return
    frame = requestAnimationFrame(() => {
      frame = 0
      decorateAll(host)
    })
  }

  const observer = new MutationObserver(schedule)
  observer.observe(host, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['data-column-id', 'class', 'style'],
  })
  decorateAll(host)

  return () => {
    observer.disconnect()
    if (frame) cancelAnimationFrame(frame)
  }
}
