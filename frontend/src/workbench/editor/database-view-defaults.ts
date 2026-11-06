/**
 * 事项表的视图默认值归一（对齐 AFFiNE-Know原型.html 的数据库页）。
 *
 * 为什么需要这一层：视图配置（列宽、看板卡片显示哪些属性）写在 Y.Doc 的视图数据里，
 * 建库之后改 `database-seed.ts` **不会**影响已有条目（§7.1 第 9 条）。
 * 而事项是全局单例、只会被创建一次，于是「改进默认值」对老用户永远不会生效。
 *
 * 这里用「只在用户没动过时才归一」的保守策略：
 *   - 列宽：seed 建表时把所有列都写成 180；只要有一列不是 180，就认为用户手动调过，
 *     整体跳过，绝不覆盖用户的拖拽结果；
 *   - 看板卡片：seed 写的是 `[状态, 负责人]`；只有完全等于这个遗留值时，才换成原型
 *     的 `[优先级, 截止日, 负责人]`。
 *
 * 两个函数都返回 `null` 表示「不需要动」，调用方据此跳过写入，避免每次挂载都产生脏数据。
 */
import { WORKBENCH_DB_COLUMNS } from './database-columns'

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

/** `database-seed.ts` 建表时给每一列写的宽度。 */
export const SEED_COLUMN_WIDTH = 180

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
 * 计算表格视图应有的列宽；返回 `null` 表示保持原样。
 *
 * 判定「没被手动调过」的依据是**全部**列宽都还等于 seed 默认值。用户拖动任意一列后
 * 就整体放弃归一——宁可少改一次，也不要把用户排好的列宽冲掉。
 */
export function planColumnWidths(
  columns: ReadonlyArray<{ id: string; width?: number }>,
): Array<{ id: string; width: number }> | null {
  if (!columns.length) return null
  const untouched = columns.every((column) => column.width === SEED_COLUMN_WIDTH)
  if (!untouched) return null
  const next = columns.map((column) => ({
    id: column.id,
    width: PROTOTYPE_COLUMN_WIDTHS[column.id] ?? SEED_COLUMN_WIDTH,
  }))
  // 全是未知列时 next 与当前完全一致，不产生写入。
  return next.some((column, index) => column.width !== columns[index]?.width) ? next : null
}

/** 计算看板卡片应有的属性；返回 `null` 表示保持原样（用户自定义过或结构不符）。 */
export function planCardProperties(visible: readonly string[]): string[] | null {
  if (visible.length !== LEGACY_CARD_PROPERTIES.length) return null
  if (!LEGACY_CARD_PROPERTIES.every((id, index) => visible[index] === id)) return null
  return [...PROTOTYPE_CARD_PROPERTIES]
}
