/**
 * 数据库工具条会话（P3-1 / P3-3 / P3-4）。
 *
 * 定位：**只做视图层与调用上游公开 API**，不自研表格、不复制数据库状态。
 *
 * 关键约束（实现方案 §10.7.1）：不能出现第二份数据库状态。
 * 因此这里**不自己 new DatabaseBlockDataSource**——那样得到的 `viewManager`
 * 与上游 `<affine-database>` 组件里的不是同一个实例，`setCurrentView` 只改本对象的
 * 私有信号，页面上不会有任何变化。正确做法是从已渲染的 `affine-database` 元素上
 * 取它自己的 `dataSource`，与表格/看板/日历共用同一份视图与行数据。
 *
 * 与 Journals 的边界（§10.8）：这里不读 `journal_date`，也不写条目日历。
 */
import { effect } from '@preact/signals-core'

import {
  columnsFromModel,
  csvToPlan,
  databaseToCsv,
  optionsOf,
  rowsFromDataSource,
} from './database-csv'
import type { CsvColumnLike } from './database-csv'
import { EMPTY_DATABASE_STATE } from './database-state'
import type { DatabaseActiveFilter, DatabaseToolbarState, DatabaseFilterState } from './database-state'
import { WORKBENCH_DB_COLUMNS } from './database-seed'
import type { CanvasHandle } from './blocksuite'
import { applyDatabasePatch, buildDatabaseContext } from './database-ai'
import type { DatabaseAIContext, DatabasePatchOutcome } from './database-ai'
import type { ResolvedPatch } from '../ai/database-patch'
import { monthRange, timestampToDateOnly } from './database-dates'
import { planCardProperties, planColumnWidths } from './database-view-defaults'

// 类型与占位状态放在 database-state.ts：条目页首屏要用常量，不应因此引入编辑器包。
export type { DatabaseToolbarState }
export { EMPTY_DATABASE_STATE }

/** 行详情里的一行属性（值已转成展示文本）。 */
export interface DatabaseRowDetailField {
  columnId: string
  label: string
  type: string
  value: string
  /** select 选项的底色（原型抽屉里状态/优先级是按值着色的）。 */
  color?: string
}

/** 行详情抽屉的数据：标题 + 除标题外的全部列。 */
export interface DatabaseRowDetail {
  rowId: string
  title: string
  fields: DatabaseRowDetailField[]
}

/**
 * 工具栏单个筛选下拉的取值。
 *
 * - select 列用「选项 id 列表」：`isOneOf`，`negate` 时改用 `isNotOneOf`
 *   （原型「状态 不等于 · 已完成」）；
 * - 文本列用关键字做 `contains`；
 * - 日期列用 `month` 表示「在本月」，展开成 `after` + `before` 两条原生条件。
 */
export interface DatabaseFilterInput {
  columnId: string
  optionIds?: string[]
  text?: string
  /** select 列取反 */
  negate?: boolean
  /** 日期列「在本月」 */
  month?: boolean
}

/** 等待上游渲染出 data-view 的最长帧数（约 3 秒，覆盖首次冷启动的模块加载）。 */
const READY_RETRY_FRAMES = 180

/**
 * 工具栏「＋ 筛选」提供的三个字段。
 *
 * 与原型（AFFiNE-Know原型.html）的筛选行一致：状态、优先级两个 select 列各有
 * 「等于 / 不等于」，截止日是日期列，只提供「在本月」。
 * 上游 date 筛选只有 before / after，所以「在本月」落成两条原生条件的组合，
 * 见 `setFilters`。
 */
const FILTER_KEYS = [
  { key: 'status', columnId: WORKBENCH_DB_COLUMNS.status, kind: 'select' },
  { key: 'priority', columnId: WORKBENCH_DB_COLUMNS.priority, kind: 'select' },
  { key: 'date', columnId: WORKBENCH_DB_COLUMNS.date, kind: 'date' },
] as const

export class DatabaseToolbarController {
  state: DatabaseToolbarState = { ...EMPTY_DATABASE_STATE }
  private readonly disposers: Array<() => void> = []
  private dataSource: any = null
  private readonly dbModel: any
  private listener: (() => void) | null = null
  private retryFrames = 0
  private destroyed = false
  /** 视图名本地化表：构造时可能还没绑上 dataSource，绑定时补一次。 */
  private pendingViewNames: Record<string, string> | null = null
  /** 视图默认值只归一一次，避免每次切视图都写一遍 Y.Doc。 */
  private viewDefaultsApplied = false

  constructor(
    private readonly handle: CanvasHandle,
    private readonly host: HTMLElement,
  ) {
    const databaseId = findDatabaseId(handle.store)
    this.dbModel = databaseId ? handle.store.getModelById(databaseId) : null
    if (!this.dbModel) throw new Error('[workbench-db] 当前文档没有数据库块')
    this.tryStart()
  }

  /**
   * 等上游 `<affine-data-view-renderer>` 渲染出来再接管。
   *
   * 编辑器挂载完成 ≠ lit 渲染完成：块元素是在首帧之后才连接的。
   * 这里取的是上游公开的 `logic`（DataViewRootUILogic），再从它的 config 里拿
   * dataSource —— 与表格/看板/日历共用同一个 viewManager 与同一份行数据。
   */
  private tryStart(): void {
    if (this.destroyed) return
    const logic = this.rendererLogic()
    if (logic?.config?.dataSource) {
      this.bindDataSource(logic.config.dataSource)
      return
    }
    if (this.retryFrames++ >= READY_RETRY_FRAMES) return
    requestAnimationFrame(() => this.tryStart())
  }

  /**
   * 绑定数据源并订阅信号。
   *
   * 独立成函数是因为切形态会重建视图层：新 renderer 的 dataSource 是**同一个**
   * （store 与 dataSource 都由 block 组件持有并复用），但旧的 effect 订阅在新视图
   * 下拿不到更新，所以每次重建都要重新绑定一次。
   */
  private bindDataSource(dataSource: any): void {
    if (this.dataSource === dataSource && this.disposers.length > 0) return
    this.disposers.forEach((dispose) => dispose())
    this.disposers.length = 0
    this.dataSource = dataSource
    // derive() 里会读 rows$ / readonly$ / currentViewId$ / viewDataList$，
    // effect 自动登记依赖，任一变化就重算 state 并通知 Vue。
    this.disposers.push(
      effect(() => {
        this.state = this.derive()
        // 视图数据可能在首帧才写好：views 为空时提前 return，下一次重算再试。
        this.applyViewDefaults()
        this.listener?.()
      }),
    )
    this.state = this.derive()
    this.listener?.()
    // 构造时 data-view 可能还没渲染，localizeViewNames 会先暂存，这里补执行一次。
    if (this.pendingViewNames) this.localizeViewNames(this.pendingViewNames)
    // 视图默认值（列宽 / 看板卡片属性）跟着数据源一起归一到原型。
    this.applyViewDefaults()
  }

  /**
   * 把视图默认值对齐原型（列宽比例、看板卡片显示优先级与截止日）。
   *
   * 视图配置存在 Y.Doc 的视图数据里，改 `database-seed.ts` 只对新条目生效，
   * 而事项是全局单例、只建一次，所以老条目需要这条一次性归一。
   * 是否改由 `database-view-defaults.ts` 的纯函数判定：用户手动调整过就一律跳过。
   */
  private applyViewDefaults(): void {
    if (this.viewDefaultsApplied) return
    if (!this.dataSource || this.state.readonly) return
    const viewIds: string[] = this.dataSource.viewManager.views$.value ?? []
    // 视图还没建好时不置位，等信号更新后重试，否则这一次归一会永远错过。
    if (!viewIds.length) return
    this.viewDefaultsApplied = true
    for (const viewId of viewIds) {
      const data = this.dataSource.viewDataGet(viewId)
      if (data?.mode === 'table') {
        const next = planColumnWidths(Array.isArray(data.columns) ? data.columns : [])
        if (next) {
          this.dataSource.viewDataUpdate(viewId, (current: any) => ({ ...current, columns: next }))
        }
      } else if (data?.mode === 'kanban') {
        const visible = data?.card?.visiblePropertyIds
        const next = planCardProperties(Array.isArray(visible) ? visible.map(String) : [])
        if (next) {
          this.dataSource.viewDataUpdate(viewId, (current: any) => ({
            ...current,
            card: { ...current.card, visiblePropertyIds: next },
          }))
        }
      }
    }
  }

  /** 供 Vue 层订阅：state 每次重算后触发。 */
  onUpdate(listener: () => void): void {
    this.listener = listener
  }

  /**
   * 操作前确认绑定的仍是当前视图层的数据源。
   *
   * 切形态 / 重载会重建 block 组件，随之产生**新的** DatabaseBlockDataSource；
   * 旧的 effect 订阅在新视图下不再收到更新，工具条会停在过期状态。
   */
  private ensureBound(): boolean {
    const dataSource = this.rendererLogic()?.config?.dataSource
    if (dataSource) this.bindDataSource(dataSource)
    return Boolean(this.dataSource)
  }

  private derive(): DatabaseToolbarState {
    if (!this.dataSource) return { ...EMPTY_DATABASE_STATE }
    const currentViewId = this.dataSource.viewManager.currentViewId$.value ?? ''
    const currentView = this.dataSource.viewDataGet(currentViewId)
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    const columnMap = new Map(columns.map((column) => [column.id, column] as const))
    const filters: DatabaseFilterState[] = []
    for (const item of FILTER_KEYS) {
      const column = columnMap.get(item.columnId)
      if (!column) continue
      if (item.kind === 'date') {
        filters.push({ key: item.key, columnId: item.columnId, label: column.name, type: 'date', options: [] })
        continue
      }
      const options = optionsOf(column).map((option) => ({ id: option.id, value: option.value }))
      if (options.length) {
        filters.push({ key: item.key, columnId: item.columnId, label: column.name, type: 'select', options })
      }
    }
    const sortColumns = [WORKBENCH_DB_COLUMNS.date, WORKBENCH_DB_COLUMNS.priority]
      .map((id) => columnMap.get(id))
      .filter((column): column is NonNullable<typeof column> => Boolean(column))
      .map((column) => ({ id: column.id, label: column.name }))
    // 视图数据里已有的筛选/排序回填给下拉，刷新或切视图后控件不会显示成「未筛选」。
    const conditions: any[] = Array.isArray(currentView?.filter?.conditions)
      ? currentView.filter.conditions
      : []
    const activeFilters = readActiveFilters(conditions)
    const firstSort = currentView?.sort?.sortBy?.[0]
    const activeSort = firstSort?.ref?.name
      ? { columnId: String(firstSort.ref.name), desc: Boolean(firstSort.desc) }
      : null
    return {
      ready: (this.dataSource.viewDataList$.value as unknown[]).length > 0,
      readonly: Boolean(this.dataSource.readonly$.value),
      rowCount: (this.dataSource.rows$.value as string[]).length,
      currentViewMode: currentView?.mode ?? '',
      calendarDateColumnId: currentView?.mode === 'calendar' ? (currentView?.date?.startColumnId ?? '') : '',
      dateColumns: columns
        .filter((column) => column.type === 'date')
        .map((column) => ({ id: column.id, name: column.name })),
      columns: columns.map((column) => ({
        id: column.id,
        name: column.name,
        type: column.type,
        options: optionsOf(column).map((option) => ({ id: option.id, value: option.value })),
      })),
      filters,
      sortColumns,
      activeFilters,
      activeSort,
    }
  }

  /** 当前视图对象；工具栏的搜索/筛选/排序都作用在它身上。 */
  private currentView(): any {
    const viewId = this.dataSource?.viewManager.currentViewId$.value
    return viewId ? this.dataSource.viewManager.viewGet(viewId) : null
  }

  /**
   * 关键字搜索：直接调上游单视图的 setSearch。
   *
   * 上游会把搜索串并进 `rows$` 的映射（表格/看板/日历通用），不需要另存一份状态。
   */
  setSearch(text: string): void {
    if (!this.ensureBound()) return
    this.currentView()?.setSearch?.(text)
  }

  /**
   * 设置筛选条件（多个条件之间是 AND）。
   *
   * 直接写视图数据的 `filter` 字段，与上游筛选面板是同一条数据通路：
   * 表格、看板、日历会一起生效，刷新后也保留。
   */
  setFilters(inputs: DatabaseFilterInput[]): void {
    if (!this.ensureBound()) return
    const viewId = this.dataSource.viewManager.currentViewId$.value
    if (!viewId) return
    const { start, end } = monthRange()
    const conditions = inputs.flatMap((input): any[] => {
      if (input.month) {
        // 「在本月」= after(本月 1 日 0 点 - 1ms) + before(下月 1 日 0 点)。
        // 上游 after/before 都是严格比较，用 -1ms 让 1 日 0 点整的行也算在本月内。
        return [
          {
            type: 'filter',
            left: { type: 'ref', name: input.columnId },
            function: 'after',
            args: [{ type: 'literal', value: start - 1 }],
          },
          {
            type: 'filter',
            left: { type: 'ref', name: input.columnId },
            function: 'before',
            args: [{ type: 'literal', value: end }],
          },
        ]
      }
      if (input.optionIds?.length) {
        return [{
          type: 'filter',
          left: { type: 'ref', name: input.columnId },
          function: input.negate ? 'isNotOneOf' : 'isOneOf',
          args: [{ type: 'literal', value: input.optionIds }],
        }]
      }
      if (input.text) {
        return [{
          type: 'filter',
          left: { type: 'ref', name: input.columnId },
          function: 'contains',
          args: [{ type: 'literal', value: input.text }],
        }]
      }
      return []
    })
    this.dataSource.viewDataUpdate(viewId, (data: any) => ({
      ...data,
      filter: { type: 'group', op: 'and', conditions },
    }))
  }

  /** 单列排序；columnId 为空表示取消排序。 */
  setSort(columnId: string, desc: boolean): void {
    if (!this.ensureBound()) return
    const viewId = this.dataSource.viewManager.currentViewId$.value
    if (!viewId) return
    this.dataSource.viewDataUpdate(viewId, (data: any) => ({
      ...data,
      sort: {
        ...(data.sort ?? {}),
        manuallySort: [],
        sortBy: columnId ? [{ ref: { type: 'ref', name: columnId }, desc }] : [],
      },
    }))
  }

  /**
   * 把上游默认英文视图名改成当前语言。
   *
   * 视图名建库时写进 Y.Doc，之后不随界面语言变化；老条目里是
   * Table View / Kanban View / Calendar View。这里只改**仍是默认名**的视图，
   * 用户自己改过名的保持原样。幂等，可随每次挂载执行。
   */
  localizeViewNames(names: Record<string, string>): void {
    this.pendingViewNames = names
    if (!this.dataSource || this.state.readonly) return
    const viewIds: string[] = this.dataSource.viewManager.views$.value ?? []
    for (const id of viewIds) {
      const view = this.dataSource.viewManager.viewGet(id)
      const current: string = view?.name$.value ?? ''
      const next = names[current]
      if (next && next !== current) view?.nameSet(next)
    }
  }

  /**
   * 指定 Calendar 视图用哪个日期属性驱动（§10.8.2 规则 1）。
   *
   * 只改 `date.startColumnId`，行数据与其它视图都不受影响；
   * 上游 CalendarSingleView 会把它接到 `dateMapping$` 并重算 `rowEntries$`。
   */
  setCalendarDateColumn(columnId: string): void {
    if (!this.ensureBound()) return
    const viewId = this.dataSource.viewManager.currentViewId$.value
    const view = viewId ? this.dataSource.viewManager.viewGet(viewId) : null
    if (!view || typeof view.setStartDateColumn !== 'function') return
    view.setStartDateColumn(columnId)
  }

  /** 新建行：上游工具行已提供同名按钮，这里仅给 CSV 导入后的提示使用。 */
  newRow(): string {
    if (!this.ensureBound()) return ''
    return this.dataSource.rowAdd('end')
  }

  /** 打开行详情：走上游 openDetailPanel（行详情里的字段编辑器全部复用上游）。 */
  openRow(rowId: string): void {
    if (!this.ensureBound()) return
    const logic = this.rendererLogic()
    const viewId = this.dataSource.viewManager.currentViewId$.value
    const view = viewId ? this.dataSource.viewManager.viewGet(viewId) : null
    if (!logic || !view) return
    logic.openDetailPanel({ view, rowId })
  }

  /**
   * 行详情数据（自研抽屉用）。
   *
   * 只读：把该行的每个单元格按列类型转成展示文本，select 额外带上选项底色。
   * 与表格共用同一个 dataSource，不复制状态。
   */
  rowDetail(rowId: string): DatabaseRowDetail | null {
    if (!this.ensureBound()) return null
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    const titleColumn = columns.find((column) => column.type === 'title')
    const titleRaw = titleColumn ? this.dataSource.cellValueGet(rowId, titleColumn.id) : ''
    const title = typeof titleRaw?.toString === 'function' ? String(titleRaw.toString()) : String(titleRaw ?? '')
    return {
      rowId,
      title: title.trim(),
      fields: columns
        .filter((column) => column.type !== 'title')
        .map((column) => ({
          columnId: column.id,
          label: column.name,
          type: column.type,
          ...this.formatCell(column, this.dataSource.cellValueGet(rowId, column.id)),
        })),
    }
  }

  private formatCell(column: CsvColumnLike, raw: unknown): { value: string; color?: string } {
    if (column.type === 'select' || column.type === 'multi-select') {
      const ids = Array.isArray(raw) ? raw : raw ? [raw] : []
      const options = optionsOf(column)
      const picked = ids
        .map((id) => options.find((option) => option.id === id))
        .filter((option): option is NonNullable<typeof option> => Boolean(option))
      return {
        value: picked.map((option) => option.value).join('、'),
        color: picked[0]?.color,
      }
    }
    if (column.type === 'date') {
      const stamp = typeof raw === 'number' ? raw : Number(raw)
      return { value: Number.isFinite(stamp) && stamp ? timestampToDateOnly(stamp) : '' }
    }
    if (raw == null) return { value: '' }
    const text = typeof raw?.toString === 'function' ? String(raw.toString()) : String(raw)
    return { value: text.trim() }
  }

  /** 导出当前全部行（不受筛选影响）：往返不丢列是 P3-4 的硬约束。 */
  exportCsv(): { filename: string; content: string } | null {
    if (!this.ensureBound()) return null
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    return {
      filename: 'database.csv',
      content: databaseToCsv(columns, rowsFromDataSource(this.dataSource, columns)),
    }
  }

  /**
   * 导入 CSV：
   *   1. 表头里没有的列按文本列新建（用 rich-text：它是用户可见、可继续编辑的类型）；
   *   2. select / multi-select 出现新值时补选项；
   *   3. 逐行新建并写回标题与单元格。
   * 返回写入行数，调用方据此提示。
   */
  importCsv(text: string): number {
    if (!this.ensureBound() || this.state.readonly) return 0
    const plan = csvToPlan(text, columnsFromModel(this.dbModel, this.dataSource))

    const createdIds = new Map<string, string>()
    for (const name of plan.newColumns) {
      const id = this.dataSource.propertyAdd('end', { type: 'rich-text', name })
      if (id) createdIds.set(name, id)
    }
    // 选项扩充一次写回，避免每行都触发一次列更新。
    for (const [columnId, data] of Object.entries(plan.columnData)) {
      this.dataSource.propertyDataSet(columnId, data)
    }

    let written = 0
    for (const row of plan.rows) {
      const rowId: string = this.dataSource.rowAdd('end')
      if (!rowId) continue
      // 标题列是 spacial property：值就是行块自身的 Y.Text，只能改文本、不能走 cellValueChange。
      const model = this.handle.store.getModelById(rowId)
      if (model?.text) model.text.replace(0, model.text.length, row.title)
      for (const [columnId, value] of Object.entries(row.cells)) {
        this.setCell(rowId, columnId, value)
      }
      for (const [name, value] of Object.entries(row.extra)) {
        const columnId = createdIds.get(name)
        if (columnId) this.setCell(rowId, columnId, value)
      }
      written += 1
    }
    return written
  }

  /**
   * 写一个单元格。
   *
   * 富文本列的原始值是 Y.Text，直接塞字符串会渲染成空；这里对字符串统一走
   * 列的 `rawValue.fromJson`（文本 → 原始值）。日期是时间戳、多选是数组，
   * 本来就是原始值，不需要转换。
   */
  private setCell(rowId: string, columnId: string, value: unknown): void {
    const type = this.dataSource.propertyTypeGet(columnId)
    const fromJson = type ? this.dataSource.propertyMetaGet(type)?.config?.rawValue?.fromJson : null
    const raw = typeof value === 'string' && fromJson ? fromJson({ value }) : value
    this.dataSource.cellValueChange(rowId, columnId, raw)
  }

  /**
   * 采样表结构与行快照，供数据库 AI 生成补丁（P3-5）。
   *
   * 只读：不写 store，也不持有第二份状态；行数超过上限时原样返回 truncated。
   */
  aiContext(): DatabaseAIContext | null {
    if (!this.ensureBound()) return null
    return buildDatabaseContext(this.dbModel, this.dataSource)
  }

  /**
   * 一次性写入 AI 补丁（P3-5）。
   *
   * 整包放在一个 `store.transact` 里，任一 op 失败全部回滚；
   * 成功后由编辑器正常脏标记与保存队列落盘（AI 不持有独立写连接）。
   */
  applyAiPatch(patch: ResolvedPatch, allowOverwrite: boolean): DatabasePatchOutcome | null {
    if (!this.ensureBound() || this.state.readonly) return null
    return applyDatabasePatch({
      store: this.handle.store,
      dataSource: this.dataSource,
      model: this.dbModel,
      patch,
      allowOverwrite,
    })
  }

  destroy(): void {
    this.destroyed = true
    this.disposers.forEach((dispose) => dispose())
    this.disposers.length = 0
    this.listener = null
    this.dataSource = null
  }

  /** 上游渲染出来的 DataViewRootUILogic；每次现查，切形态重建视图后也能拿到新的。 */
  private rendererLogic(): any {
    return (this.host.querySelector('affine-data-view-renderer') as { logic?: any } | null)?.logic ?? null
  }
}

/** 从 store 里找第一个数据库块 id（与 seed 模块同一套遍历规则）。 */
function findDatabaseId(store: any): string {
  for (const note of store?.root?.children ?? []) {
    for (const child of note.children ?? []) {
      if (child.flavour === 'affine:database') return child.id
    }
  }
  return ''
}

/**
 * 把视图数据里的筛选条件还原成工具条 chip 的状态。
 *
 * 只认工具条自己会写出的四种函数：select 的 `isOneOf` / `isNotOneOf`，
 * 日期列的 `after` / `before`。日期列必须在同一列上同时存在 after 与 before，
 * 才认作「在本月」——单独一条 after/before 是用户在上游筛选面板里加的自定义条件，
 * 工具条表达不了，就不显示 chip（表格仍按上游条件过滤，不丢数据）。
 */
function readActiveFilters(conditions: any[]): DatabaseActiveFilter[] {
  const result = new Map<string, DatabaseActiveFilter>()
  const dateFunctions = new Map<string, Set<string>>()
  for (const condition of conditions) {
    if (condition?.type !== 'filter') continue
    const columnId = String(condition?.left?.name ?? '')
    if (!columnId) continue
    const fn = String(condition?.function ?? '')
    if (fn === 'isOneOf' || fn === 'isNotOneOf') {
      const value = condition?.args?.[0]?.value
      if (!Array.isArray(value)) continue
      result.set(columnId, {
        columnId,
        optionIds: value.map(String),
        negate: fn === 'isNotOneOf',
      })
      continue
    }
    if (fn === 'after' || fn === 'before') {
      const set = dateFunctions.get(columnId) ?? new Set<string>()
      set.add(fn)
      dateFunctions.set(columnId, set)
    }
  }
  for (const [columnId, fns] of dateFunctions) {
    if (fns.has('after') && fns.has('before')) {
      result.set(columnId, { columnId, optionIds: [], month: true })
    }
  }
  return [...result.values()]
}
