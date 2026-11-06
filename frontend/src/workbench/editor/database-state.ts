/**
 * 数据库工具条的类型与初始状态（P3-1）。
 *
 * 单独成文件的原因：条目页需要在首屏就拿到 `EMPTY_DATABASE_STATE` 做占位，
 * 而工具条控制器依赖 BlockSuite 模块图。把纯类型与常量拆出来后，
 * 条目页不会因为一个常量而静态引入整个编辑器包。
 *
 * 范围收敛（P3-1 对齐原型）：视图切换、筛选、排序、搜索、新建行都复用上游
 * `<affine-database>` 的原生头部，不再复制一份状态到这里。工作台只补上游没有的
 * 两件事——CSV 导入导出、Calendar 视图的日期属性选择。
 */

export interface DatabaseColumnState {
  id: string
  name: string
  type: string
  /** select 列的可选值；其它类型为空数组。 */
  options: Array<{ id: string; value: string }>
}

/** 工具栏上一个筛选下拉的描述：标签 + 以哪个列筛选 + 可选值。 */
export interface DatabaseFilterState {
  key: string
  columnId: string
  label: string
  options: Array<{ id: string; value: string }>
}

export interface DatabaseToolbarState {
  ready: boolean
  readonly: boolean
  rowCount: number
  /** 当前视图类型（table / kanban / calendar）；Calendar 专属控件据此显隐 */
  currentViewMode: string
  /** Calendar 视图当前绑定的日期列（date.startColumnId）；非日历视图为空串 */
  calendarDateColumnId: string
  /** 可选的日期列：日历视图只允许一个日期属性驱动（§10.8.2 规则 1） */
  dateColumns: Array<{ id: string; name: string }>
  /** 全部列（含 select 选项），工具栏的筛选下拉据此渲染 */
  columns: DatabaseColumnState[]
  /** 固定的 5 个字段筛选（优先级 / 状态 / 需求来源 / 价值达成 / 所属版本） */
  filters: DatabaseFilterState[]
  /** 排序下拉可选的列 */
  sortColumns: Array<{ id: string; label: string }>
  /** 当前视图上已生效的字段筛选（从视图数据回填，保证刷新后下拉仍显示选中值） */
  activeFilters: Array<{ columnId: string; optionIds: string[] }>
  /** 当前视图的排序；未排序时为 null */
  activeSort: { columnId: string; desc: boolean } | null
}

/** 文档还没挂载 / 还没有数据库块时的占位状态。 */
export const EMPTY_DATABASE_STATE: DatabaseToolbarState = {
  ready: false,
  readonly: true,
  rowCount: 0,
  currentViewMode: '',
  calendarDateColumnId: '',
  dateColumns: [],
  columns: [],
  filters: [],
  sortColumns: [],
  activeFilters: [],
  activeSort: null,
}
