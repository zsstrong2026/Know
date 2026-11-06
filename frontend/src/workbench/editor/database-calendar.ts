/**
 * 数据库 Calendar 视图的跨月拖拽增强（P3-3）。
 *
 * 上游 `CalendarDnd` 只在当前渲染出来的月格上命中日期：
 * 拖到网格最左侧/最右侧的补白日期可以跨月改期，但**不会**在悬停边缘时自动翻月。
 * 这里不改上游源码，只在工作台层监听拖拽事件：
 *   - 拖拽中指针进入网格左右边缘阈值内 → 点一次上游的「上/下个月」按钮；
 *   - 翻月后上游会重新渲染月格与 DnD 目标，拖拽本身由浏览器维持，松手仍落在新月份。
 *
 * 边界：只做「悬停边缘自动翻月」，不做周/日视图、不做多日历（§10.8.1）。
 */

/** 边缘触发宽度（像素）。太窄会难以命中，太宽会在正常拖拽时误翻月。 */
export const EDGE_FLIP_THRESHOLD = 56

/** 两次自动翻月之间的最小间隔，避免一次悬停连续翻很多个月。 */
export const EDGE_FLIP_COOLDOWN_MS = 650

/** 指针横坐标 → 翻月方向：-1 上一月 / 1 下一月 / 0 不翻。纯函数，便于单测。 */
export function edgeFlipDirection(clientX: number, rect: { left: number; right: number }, threshold = EDGE_FLIP_THRESHOLD): -1 | 0 | 1 {
  if (rect.right - rect.left <= threshold * 2) return 0
  if (clientX <= rect.left + threshold) return -1
  if (clientX >= rect.right - threshold) return 1
  return 0
}

/**
 * 绑定边缘自动翻月。返回清理函数。
 *
 * 只在「正在拖拽日历条目」时生效：dragstart 来自 `.calendar-entry`，
 * 因此普通框选、点选都不会触发翻月。
 */
export function installCalendarEdgeFlip(host: HTMLElement): () => void {
  let dragging = false
  let lastFlipAt = 0

  const onDragStart = (event: DragEvent) => {
    const target = event.target as HTMLElement | null
    dragging = Boolean(target?.closest?.('.calendar-entry'))
  }

  const onDragEnd = () => {
    dragging = false
  }

  const onDragOver = (event: DragEvent) => {
    if (!dragging) return
    const grid = host.querySelector<HTMLElement>('.calendar-grid')
    if (!grid) return
    // 拖拽期间 pointer-events 可能被预览层接管，直接按几何位置判断。
    const direction = edgeFlipDirection(event.clientX, grid.getBoundingClientRect())
    if (direction === 0) return
    const now = Date.now()
    if (now - lastFlipAt < EDGE_FLIP_COOLDOWN_MS) return
    lastFlipAt = now
    const selector =
      direction < 0
        ? '.calendar-icon-button[aria-label="Previous month"]'
        : '.calendar-icon-button[aria-label="Next month"]'
    host.querySelector<HTMLElement>(selector)?.click()
  }

  host.addEventListener('dragstart', onDragStart, true)
  host.addEventListener('dragend', onDragEnd, true)
  host.addEventListener('dragover', onDragOver, true)
  return () => {
    host.removeEventListener('dragstart', onDragStart, true)
    host.removeEventListener('dragend', onDragEnd, true)
    host.removeEventListener('dragover', onDragOver, true)
  }
}
