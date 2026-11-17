/**
 * 事项表的视图默认值归一（对齐 AFFiNE-Know原型.html 的数据库页）。
 *
 * 为什么需要这一层：视图配置（列宽、看板卡片显示哪些属性）写在 Y.Doc 的视图数据里，
 * 建库之后改 `database-seed.ts` **不会**影响已有条目（§7.1 第 9 条）。
 * 而事项是全局单例、只会被创建一次，于是「改进默认值」对老用户永远不会生效。
 *
 * 这里用「只在用户没动过时才归一」的保守策略：
 *   - 列宽：seed 建表时把所有列都写成 180，或本模块上次归一过的比例宽度；
 *     只要看起来被手工拖动过（比例被破坏），就整体跳过，绝不覆盖用户的拖拽结果；
 *   - 看板卡片：seed 写的是 `[状态, 负责人]`；只有完全等于这个遗留值时，才换成原型
 *     的 `[优先级, 截止日, 负责人]`。
 *
 * 两个函数都返回 `null` 表示「不需要动」，调用方据此跳过写入，避免每次挂载都产生脏数据。
 */
import { WORKBENCH_DB_COLUMNS, WORKBENCH_DB_SOURCE_COLUMNS } from './database-columns'

/**
 * 原型里六列的宽度（px）。
 *
 * 取值参考原型 `db-table` 的视觉比例：事项最宽，状态/优先级窄，负责人与关联文档居中。
 * 目的不是像素级复刻，而是让「一屏能看全六列」，不再六列各 180px 撑出横向滚动。
 */
export const PROTOTYPE_COLUMN_WIDTHS: Record<string, number> = {
  [WORKBENCH_DB_COLUMNS.title]: 260,
  [WORKBENCH_DB_COLUMNS.status]: 104,
  [WORKBENCH_DB_COLUMNS.owner]: 116,
  [WORKBENCH_DB_COLUMNS.date]: 128,
  [WORKBENCH_DB_COLUMNS.priority]: 96,
  [WORKBENCH_DB_COLUMNS.relation]: 152,
}

/** 来源两列的宽度：比六主列窄，避免挤压「事项」列。 */
export const SOURCE_COLUMN_WIDTH = 112

/** `database-seed.ts` 建表时给每一列写的宽度。 */
export const SEED_COLUMN_WIDTH = 180

/**
 * 原型 `db-table` 的列宽比例（th:nth-child 1..6）。
 *
 * 表宽 100% 时用这组比例分配，视觉上才与原型一致：事项占三分之一，
 * 状态/优先级这类短字段不占宽，负责人/截止日居中。
 */
const PROTOTYPE_RATIOS: Record<string, number> = {
  [WORKBENCH_DB_COLUMNS.title]: 0.34,
  [WORKBENCH_DB_COLUMNS.status]: 0.14,
  [WORKBENCH_DB_COLUMNS.owner]: 0.16,
  [WORKBENCH_DB_COLUMNS.date]: 0.13,
  [WORKBENCH_DB_COLUMNS.priority]: 0.11,
  [WORKBENCH_DB_COLUMNS.relation]: 0.12,
}

/** 该列在「用户没动过」状态下应有的默认宽度。 */
export function defaultColumnWidth(id: string): number {
  return (
    PROTOTYPE_COLUMN_WIDTHS[id] ??
    ((WORKBENCH_DB_SOURCE_COLUMNS as readonly string[]).includes(id)
      ? SOURCE_COLUMN_WIDTH
      : SEED_COLUMN_WIDTH)
  )
}

/** 六主列在窄屏下的最小宽度：再窄字段名会被省略号吃掉，宁可轻微横向滚动。 */
const MIN_RATIO_WIDTH = 72

/** 归一过的主列宽度与原型比例之间的容差（浮点四舍五入累积误差）。 */
const RATIO_TOLERANCE = 1.03

/**
 * 把可用宽度按原型比例分给六主列（可放大，也可压缩到最小宽度）。
 *
 * 为什么需要：列宽是绝对值，宽屏下八列加起来只占表格的一半，
 * 右侧留一大片空白，字段全挤在左边；而原型是 `table{width:100%}`。
 * 窄屏则相反：绝对值加起来超过容器，出现横向滚动，与原型「一屏看全」不符。
 * 所以这里按预算双向调整，仅当每列都压到最小宽度仍放不下时才允许溢出。
 */
function fitToWidth(
  columns: Array<{ id: string; width: number }>,
  availableWidth: number,
): void {
  if (availableWidth <= 0) return
  const ratioColumns = columns.filter((column) => PROTOTYPE_RATIOS[column.id] !== undefined)
  if (!ratioColumns.length) return
  // 非主列（来源 / 用户自建列）宽度固定，剩余预算才交给六主列。
  const fixedWidth = columns
    .filter((column) => PROTOTYPE_RATIOS[column.id] === undefined)
    .reduce((sum, column) => sum + column.width, 0)
  const budget = availableWidth - fixedWidth
  if (budget <= 0) return
  const ratioSum = ratioColumns.reduce((sum, column) => sum + PROTOTYPE_RATIOS[column.id], 0)
  for (const column of ratioColumns) {
    column.width = Math.max(
      MIN_RATIO_WIDTH,
      Math.round((budget * PROTOTYPE_RATIOS[column.id]) / ratioSum),
    )
  }
}

/** 该列宽度是否被最小宽度截断（截断后反推不出原始预算）。 */
function isClamped(width: number): boolean {
  return width <= MIN_RATIO_WIDTH
}

/**
 * 现有列宽是否已经由本模块归一过。
 *
 * 判定依据：六主列的「宽度 / 原型比例」彼此接近 —— 归一结果一定成正比，
 * 用户拖动任意一列就会破坏这个比例。这条判定让「换窗口宽度后重新铺满」
 * 可以再次生效，同时仍然不会覆盖用户的手动调整。
 *
 * 注意：窄容器下多数列会被压到最小宽度（如 119/72/72/72/72/72），
 * 这些列反推不出预算，比例校验只用在未被截断的列上；
 * 否则混合态会被误判成「用户拖动过」而永久锁死。
 */
function isNormalized(columns: ReadonlyArray<{ id: string; width?: number }>): boolean {
  const ratioColumns = columns.filter((column) => PROTOTYPE_RATIOS[column.id] !== undefined)
  if (ratioColumns.length !== Object.keys(PROTOTYPE_RATIOS).length) return false
  const widths = ratioColumns.map((column) => column.width ?? 0)
  // 六列全被压到最小宽度：同样是我方归一的结果。
  if (widths.every((width) => isClamped(width))) return true
  if (widths.some((width) => width <= 0)) return false
  const scales = ratioColumns
    .filter((column) => !isClamped(column.width ?? 0))
    .map((column) => (column.width ?? 0) / PROTOTYPE_RATIOS[column.id])
  if (!scales.length) return true
  const min = Math.min(...scales)
  const max = Math.max(...scales)
  return min > 0 && max / min < RATIO_TOLERANCE
}

/**
 * 看板卡片的遗留属性组合：seed 早期只写 `[状态, 负责人]`。
 *
 * 状态是看板的分组列，在卡片上重复显示没有意义；原型卡片显示的是
 * 标题 + 优先级 + 头像 + 日期。
 */
export const LEGACY_CARD_PROPERTIES: readonly string[] = [
  WORKBENCH_DB_COLUMNS.status,
  WORKBENCH_DB_COLUMNS.owner,
]

/** 原型看板卡片的可见属性。 */
export const PROTOTYPE_CARD_PROPERTIES: readonly string[] = [
  WORKBENCH_DB_COLUMNS.priority,
  WORKBENCH_DB_COLUMNS.date,
  WORKBENCH_DB_COLUMNS.owner,
]

/**
 * 单列宽度是否「基本等于默认值」。
 *
 * 历史数据里有几像素的偏差（例如状态列实际 100、默认 104），严格相等会把整组
 * 列宽误判成「用户手动拖动过」，归一就永远不生效。容差取 4px 与默认宽度 4%
 * 的较大值：既吸收历史偏差，又拦得住真实的拖动（如负责人 210 vs 116）。
 */
function nearlyDefault(id: string, width: number | undefined): boolean {
  if (width === undefined) return false
  if (width === SEED_COLUMN_WIDTH) return true
  const target = defaultColumnWidth(id)
  return Math.abs(width - target) <= Math.max(4, Math.round(target * 0.04))
}

/**
 * 计算表格视图应有的列宽；返回 `null` 表示保持原样。
 *
 * 「没被手动调过」的判定分两种：
 *   - 每列宽度要么是建表时的统一值（老条目全是 180），要么已基本等于该列默认值；
 *   - 或者列宽看起来就是本模块上一次归一的结果（六主列成正比）。
 * 第二条是为了让「窗口变窄/变宽」时能重新铺满，否则归一过一次后就永远定格。
 * 用户拖动任意一列会破坏比例，此后整体放弃归一。
 *
 * `availableWidth` 是表格可视宽度：传入时把可用空间按原型比例分给六主列，
 * 让表格始终铺满容器；不传则只用原型基准宽度。
 */
export function planColumnWidths(
  columns: ReadonlyArray<{ id: string; width?: number }>,
  availableWidth = 0,
): Array<{ id: string; width: number }> | null {
  if (!columns.length) return null
  const untouched =
    isNormalized(columns) || columns.every((column) => nearlyDefault(column.id, column.width))
  if (!untouched) return null
  const next = columns.map((column) => ({
    id: column.id,
    width: defaultColumnWidth(column.id),
  }))
  // 先把原型基准宽度对齐，再按可用宽度等比分配。
  fitToWidth(next, availableWidth)
  // 宽度全都没变时不产生写入。
  return next.some((column, index) => column.width !== columns[index]?.width) ? next : null
}

/**
 * 表格视图是否应保留「按某列分组」。
 *
 * 原型的事项表是**一整张连续表格**（`db-table`），没有分组标题行；
 * 而上游表格视图支持 `groupBy`，从看板切回表格时会把分组配置带过来，
 * 于是表格被切成「待开始 / 进行中 / 已完成」若干块，每组各有一条新建行。
 * 这里把表格的 `groupBy` 清掉；看板仍保留自己的分组配置。
 *
 * 返回 `null` 表示无需改动。
 */
export function planTableGrouping(
  groupBy: { columnId?: string } | undefined | null,
): { type: 'groupBy'; columnId: string; name: string } | null {
  if (!groupBy || !groupBy.columnId) return null
  return { type: 'groupBy', columnId: '', name: '' }
}

/** 计算看板卡片应有的属性；返回 `null` 表示保持原样（用户自定义过或结构不符）。 */
export function planCardProperties(visible: readonly string[]): string[] | null {
  if (visible.length !== LEGACY_CARD_PROPERTIES.length) return null
  if (!LEGACY_CARD_PROPERTIES.every((id, index) => visible[index] === id)) return null
  return [...PROTOTYPE_CARD_PROPERTIES]
}

/**
 * 看板是否应该「显示空列」。
 *
 * 上游默认 `hideEmpty: true`（见 kanban/group-by-utils.ts 的
 * `getKanbanDefaultHideEmpty`），于是「待开始 / 进行中 / 已完成」三列里
 * 只要有一列暂时没有卡片，整列就消失，观感变成「状态只有存在的几种」。
 * 这里只在 `hideEmpty` 仍为真时改一次，用户手动改过就不动。
 */
export function planKanbanHideEmpty(groupBy: { hideEmpty?: boolean } | undefined): boolean | null {
  if (!groupBy) return null
  return groupBy.hideEmpty === false ? null : false
}

/** 上游「未分组」桶的固定 key（见 core/group-by/define.ts 的 ungroups）。 */
export const UNGROUPED_GROUP_KEY = 'Ungroups'

export interface KanbanGroupProperty {
  key: string
  hide?: boolean
  manuallyCardSort?: string[]
}

/**
 * 撤销上一版写入的「未分组」隐藏标记。
 *
 * 上一版为了让看板不出现「No 状态」列，把未分组桶永久隐藏写进了视图数据，
 * 结果没有状态的行会直接从看板消失。现在改为由 CSS 只隐藏**空的**未分组列：
 * 这里把旧的隐藏记录清掉（幂等），有卡片的未分组行才能重新看见。
 * 返回 `null` 表示不需要改动视图数据。
 */
export function planKanbanUngroupVisible(
  groupProperties: ReadonlyArray<KanbanGroupProperty>,
): KanbanGroupProperty[] | null {
  const next = groupProperties.filter(
    (group) => !(group.key === UNGROUPED_GROUP_KEY && group.hide),
  )
  return next.length === groupProperties.length ? null : next
}

/**
 * 工作台「事项」固定保留的三种视图，顺序与原型一致：表格 → 看板 → 日历。
 *
 * 产品口径：这三个视图不可新增、也不可删除，所以老条目缺哪个就在这里补齐。
 */
export const REQUIRED_VIEW_MODES = ['table', 'kanban', 'calendar'] as const

/**
 * 「向前方 / 向后方插入」需要继承的排序键列。
 *
 * 表格视图有排序时，物理位置不再等于显示位置：上游 `rowAdd` 虽按参照行插入，
 * 但渲染前会按 `sortBy` 重排，新行缺排序键（如截止日为空）就会被比较器判为
 * 「空值最后」，视觉上直接掉到列表末尾——这正是「点了插入却加在最后」的原因。
 *
 * 因此插入到某行前后时，把参照行的排序键值复制给新行：同键值在稳定排序下
 * 保持物理相邻，新行才会真的出现在参照行前/后。
 *
 * 返回 `null` 表示视图没有排序，不需要复制。
 */
export function planInheritedSortColumns(
  sort: { sortBy?: Array<{ ref?: { name?: unknown } }> } | undefined | null,
  skipColumns: readonly string[] = [],
): string[] | null {
  const sortBy = Array.isArray(sort?.sortBy) ? sort.sortBy : []
  if (!sortBy.length) return null
  const skip = new Set(skipColumns)
  const columns: string[] = []
  for (const entry of sortBy) {
    const name = entry?.ref?.name
    if (typeof name !== 'string' || !name || skip.has(name) || columns.includes(name)) continue
    columns.push(name)
  }
  return columns.length ? columns : null
}

/**
 * 计算需要补建的视图；返回 `null` 表示三种都已存在（不需要动）。
 *
 * `existingModes` 传视图数据里的 `mode` 列表，顺序不影响判定；
 * 补齐时按 `REQUIRED_VIEW_MODES` 的固定顺序追加，保证 Tab 顺序稳定。
 */
export function planMissingViews(existingModes: readonly string[]): string[] | null {
  const present = new Set(existingModes)
  const missing = REQUIRED_VIEW_MODES.filter((mode) => !present.has(mode))
  return missing.length ? [...missing] : null
}
