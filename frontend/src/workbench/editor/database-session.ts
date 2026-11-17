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
import { WORKBENCH_DB_COLUMNS, WORKBENCH_SOURCE_OPTIONS } from './database-seed'
import type { CanvasHandle } from './blocksuite'
import { applyDatabasePatch, buildDatabaseContext } from './database-ai'
import type { DatabaseAIContext, DatabasePatchOutcome } from './database-ai'
import type { ResolvedPatch } from '../ai/database-patch'
import { buildExistingFingerprints, type ItemWriteSchema } from '../ai/item-candidates'
import { monthRange, timestampToDateOnly } from './database-dates'
import {
  PROTOTYPE_CARD_PROPERTIES,
  defaultColumnWidth,
  planCardProperties,
  planColumnWidths,
  planKanbanUngroupVisible,
  planKanbanHideEmpty,
  planMissingViews,
  planTableGrouping,
  planInheritedSortColumns,
} from './database-view-defaults'

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

/** 表格列宽随实测容器宽度变化重算的最小差值（px）。 */
const TABLE_WIDTH_EPSILON = 4

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
  /** 上一次按实测宽度归一表格列宽时的容器宽度；变化超过阈值才重算。 */
  private appliedTableWidth = 0
  /** 上一次归一时的列 id 顺序；列增删（Add Property）也要触发重算。 */
  private appliedTableColumns = ''
  /** 固定三视图的补齐只尝试一次：上游缺可分组列时创建会失败，不能每帧重试。 */
  private viewsEnsured = false
  /**
   * 监听页面宽度变化，用于重新铺满表格列宽。
   *
   * 信号只在数据变化时触发，DOM 布局展开（侧栏收起、窗口变宽）不会重跑 effect，
   * 所以列宽会永久停在首帧量到的窄宽度上。这里用 ResizeObserver 补上这个触发源。
   */
  private resizeObserver: ResizeObserver | null = null

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
    // 新建行补默认来源：包装一次 rowAdd，覆盖上游所有建行入口。
    this.patchRowAdd()
    // 布局展开不会触发信号，单独监听宿主宽度变化来重算列宽。
    this.observeTableWidth()
  }

  /** 监听宿主元素宽度变化，变化时按新宽度重新铺满表格列宽。 */
  private observeTableWidth(): void {
    if (typeof ResizeObserver === 'undefined' || this.resizeObserver) return
    this.resizeObserver = new ResizeObserver(() => {
      if (this.destroyed || !this.dataSource) return
      const viewIds: string[] = this.dataSource.viewManager.views$.value ?? []
      if (viewIds.length) this.applyTableColumnWidths(viewIds)
    })
    this.resizeObserver.observe(this.host)
  }

  /**
   * 把视图默认值对齐原型（列宽比例、看板卡片显示优先级与截止日）。
   *
   * 视图配置存在 Y.Doc 的视图数据里，改 `database-seed.ts` 只对新条目生效，
   * 而事项是全局单例、只建一次，所以老条目需要这条一次性归一。
   * 是否改由 `database-view-defaults.ts` 的纯函数判定：用户手动调整过就一律跳过。
   */
  private applyViewDefaults(): void {
    if (!this.dataSource || this.state.readonly) return
    const initialViewIds: string[] = this.dataSource.viewManager.views$.value ?? []
    // 视图还没建好时不置位，等信号更新后重试，否则这一次归一会永远错过。
    // 注意必须先判空再补视图：views 未加载时误判成「三个都缺」会凭空造出重复视图。
    if (!initialViewIds.length) return
    // 先补固定三视图：老条目缺日历时，后面的循环才有得归一。
    this.ensureRequiredViews()
    const viewIds: string[] = this.dataSource.viewManager.views$.value ?? initialViewIds
    // 列宽必须跟着容器实测宽度走，不能只算一次：首帧布局未展开时量到的宽度
    // 会把六列全压到最小宽度，之后容器变宽也不会再变。
    this.applyTableColumnWidths(viewIds)
    if (this.viewDefaultsApplied) return
    for (const viewId of viewIds) {
      const data = this.dataSource.viewDataGet(viewId)
      if (data?.mode === 'table') {
        // 表格必须是一整张连续表：清掉从看板带过来的分组配置。
        const groupBy = planTableGrouping(data?.groupBy)
        if (groupBy) {
          this.dataSource.viewDataUpdate(viewId, (current: any) => ({ ...current, groupBy }))
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
        // 老条目的看板可能仍是 hideEmpty=true：把三个状态列点亮，避免「只有三种状态」的错觉。
        const hideEmpty = planKanbanHideEmpty(data?.groupBy)
        if (hideEmpty !== null) {
          this.dataSource.viewDataUpdate(viewId, (current: any) => ({
            ...current,
            groupBy: { ...current.groupBy, hideEmpty },
          }))
        }
        // 撤销上一版写入的「未分组」永久隐藏：改为 CSS 只隐藏空的未分组列，
        // 否则没有状态的行会从看板消失。
        const groupProperties = planKanbanUngroupVisible(
          Array.isArray(data?.groupProperties) ? data.groupProperties : [],
        )
        if (groupProperties) {
          this.dataSource.viewDataUpdate(viewId, (current: any) => ({
            ...current,
            groupProperties,
          }))
        }
      }
    }
    // 分组 / 卡片属性这类默认值只需写一次；列宽不在此守卫内，由上方单独重算。
    this.viewDefaultsApplied = true
  }

  /**
   * 按表格实测宽度重新归一六主列列宽。
   *
   * 为什么不能沿用一次性守卫：首帧时容器可能只量到几百像素（甚至量到 0），
   * 按那个宽度算出的列宽会把六列一起压到最小宽度（72px）；布局展开到 930px 后
   * 若不再重算，表格就永久定格在「左侧一小片、右侧一大片空白」。
   * 这里以「实测宽度变化 ≥ 4px 或列集合变化」为触发条件，容器变宽/变窄、
   * 抽屉里加列/删列都会重新铺满。
   */
  private applyTableColumnWidths(viewIds: string[]): void {
    const availableWidth = this.tableViewWidth()
    if (availableWidth <= 0) return
    const widthChanged =
      Math.abs(availableWidth - this.appliedTableWidth) >= TABLE_WIDTH_EPSILON
    for (const viewId of viewIds) {
      const data = this.dataSource.viewDataGet(viewId)
      if (data?.mode !== 'table') continue
      const columns: Array<{ id: string; width?: number }> = Array.isArray(data.columns)
        ? data.columns
        : []
      // 列集合变化（行详情抽屉里 Add Property / 删列）也要重新铺满：
      // 新列默认 180px，只在容器宽度变化时才重算的话，总宽会超出容器，
      // 表头被压扁而数据行保持原宽，两者就错位了。
      const signature = columns.map((column) => column.id).join('|')
      if (!widthChanged && signature === this.appliedTableColumns) continue
      this.appliedTableWidth = availableWidth
      this.appliedTableColumns = signature
      const next = planColumnWidths(columns, availableWidth)
      if (next) {
        this.dataSource.viewDataUpdate(viewId, (current: any) => ({ ...current, columns: next }))
      }
    }
  }

  /**
   * 补齐固定三视图（表格 / 看板 / 日历）。
   *
   * 产品口径：事项的三种视图「一直保留、不可增删」。而 `database-seed.ts` 只对
   * **新建**条目生效，老条目可能只有一两种视图；同时 UI 上的新建入口（「+」、
   * 菜单里的 Create / Duplicate / Delete）已按同一口径移除，不在这里自愈补齐，
   * 用户就永远补不回缺的那种视图。
   */
  private ensureRequiredViews(): void {
    if (this.viewsEnsured) return
    const manager = this.dataSource?.viewManager
    if (!manager) return
    // 只尝试一次：结构异常（如看板缺可分组列）时上游会抛错，不能每帧重试。
    this.viewsEnsured = true
    const viewIds: string[] = manager.views$.value ?? []
    const modes = viewIds
      .map((id) => this.dataSource.viewDataGet(id)?.mode)
      .filter((mode): mode is string => Boolean(mode))
    const missing = planMissingViews(modes)
    if (!missing) return
    // viewAdd 会把当前视图切到新视图，先记下用户原本停在哪一个。
    const previous: string | undefined = manager.currentViewId$.value
    for (const mode of missing) {
      try {
        const viewId: string = manager.viewAdd(mode)
        this.fillSeededView(viewId, mode)
      } catch {
        // 上游建不出该视图（如没有任何可分组列）时跳过，不影响其余视图。
      }
    }
    if (previous) manager.setCurrentView(previous)
    // 新视图名是上游英文默认名，按需换成当前语言（用户改过名的视图不受影响）。
    if (this.pendingViewNames) this.localizeViewNames(this.pendingViewNames)
  }

  /**
   * 给补建的视图写入与 `database-seed.ts` 一致的初始数据。
   *
   * 不做这一步会停在空态：table 的 `columns` 默认为空（不渲染表头），
   * calendar 缺 `date.startColumnId` 会提示「请选择日期属性」。
   */
  private fillSeededView(viewId: string, mode: string): void {
    const columns: string[] = this.dataSource.properties$.value ?? []
    if (mode === 'table') {
      this.dataSource.viewDataUpdate(viewId, (current: any) => ({
        ...current,
        columns: columns.map((id) => ({ id, width: defaultColumnWidth(id) })),
      }))
    } else if (mode === 'kanban') {
      this.dataSource.viewDataUpdate(viewId, (current: any) => ({
        ...current,
        card: { ...current.card, visiblePropertyIds: [...PROTOTYPE_CARD_PROPERTIES] },
        groupBy: current.groupBy ? { ...current.groupBy, hideEmpty: false } : current.groupBy,
      }))
    } else if (mode === 'calendar') {
      this.dataSource.viewDataUpdate(viewId, (current: any) => ({
        ...current,
        date: { ...current.date, startColumnId: WORKBENCH_DB_COLUMNS.date },
        card: { ...current.card, visiblePropertyIds: [WORKBENCH_DB_COLUMNS.status] },
      }))
    }
  }

  /** 供 Vue 层订阅：state 每次重算后触发。 */
  onUpdate(listener: () => void): void {
    this.listener = listener
  }

  /**
   * 表格可视宽度（px）。
   *
   * 取上游表格外层的实际渲染宽度；首帧还没布局时返回 0，
   * 调用方据此退化成「只用原型基准列宽」，不会写出 0 宽的视图数据。
   */
  private tableViewWidth(): number {
    // 优先取表格容器（含横向滚动区的实际宽度）；看板等非表格视图没有该元素，
    // 退化成数据库块自身宽度，避免宽度永远量到 0 而反复重试。
    const container = this.host.querySelector?.('.affine-database-table-container') as
      | HTMLElement
      | null
    const fallback = this.host.querySelector?.('affine-database') as HTMLElement | null
    const width = (container ?? fallback)?.getBoundingClientRect().width ?? 0
    return Number.isFinite(width) ? Math.floor(width) : 0
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

  /**
   * 新建行：表格插到首行、看板/日历追加到末尾，随后一律在右侧抽屉里编辑。
   *
   * 表格此前是行内聚焦标题，但新建入口在工具条最右侧，长表格里新行与入口相距很远；
   * 现在三种视图统一走右侧抽屉（上游 openDetailPanel），交互一致。
   */
  newRow(): string {
    if (!this.ensureBound()) return ''
    // 有筛选时新行往往不满足条件，会被过滤掉、看起来像「点了没反应」。
    // 新增前先清空当前视图的筛选，保证新行落在第一页首行并能直接编辑。
    if (this.state.currentViewMode === 'table') this.clearActiveFilters()
    // 表格里新行插到第一行（产品口径）；看板 / 日历仍追加到末尾，
    // 否则卡片会跳到分组顶部，破坏既有的拖拽习惯。
    const position = this.state.currentViewMode === 'table' ? 'start' : 'end'
    const rowId: string = this.dataSource.rowAdd(position)
    this.applyDefaultStatus(rowId)
    // 三种视图统一：新建后在右侧抽屉里编辑，不再在表格里行内聚焦标题。
    this.openRow(rowId)
    return rowId
  }

  /** 清空当前视图的筛选条件；没有筛选时不做任何写入。 */
  private clearActiveFilters(): void {
    const viewId = this.dataSource.viewManager.currentViewId$.value
    if (!viewId) return
    const data = this.dataSource.viewDataGet(viewId)
    const conditions = data?.filter?.conditions
    if (!Array.isArray(conditions) || conditions.length === 0) return
    this.dataSource.viewDataUpdate(viewId, (current: any) => ({
      ...current,
      filter: { ...current.filter, conditions: [] },
    }))
  }

  /**
   * 新行补默认状态「待开始」。
   *
   * 状态是看板的分组列：没有状态的行会落进上游的「未分组」桶，
   * 而该桶在工作台看板里是隐藏的，行会直接从看板上消失。
   * 选项被用户删掉时跳过，不凭空造一个状态。
   */
  private applyDefaultStatus(rowId: string): void {
    if (!rowId) return
    const data = this.dataSource.propertyDataGet(WORKBENCH_DB_COLUMNS.status) as
      | { options?: Array<{ id: string }> }
      | undefined
    const options = Array.isArray(data?.options) ? data.options : []
    if (!options.some((option) => option.id === 'wb-opt-todo')) return
    this.dataSource.cellValueChange(rowId, WORKBENCH_DB_COLUMNS.status, 'wb-opt-todo')
  }

  /**
   * 包装 `rowAdd`，让任何新建行都先补上来源「手动录入」。
   *
   * 上游建行入口很多（表格底部按钮、Enter、分组头尾、看板、日历、移动端），
   * 最终都会调 dataSource.rowAdd，逐个改不可维护，所以只在这里包一层。
   * AI 与归档同步的写入在各自路径里显式改成 `wb-src-ai` / `wb-src-archive`，
   * 这里只负责「没有特别声明的建行 = 手动录入」。
   *
   * 另外处理「向前方 / 向后方插入」：上游按参照行的物理位置插块，但表格渲染前
   * 会按视图排序重排，新行缺排序键（截止日为空）会被排到末尾，看起来像加在最后。
   * 因此这里把参照行的排序键值复制给新行，让它在排序后仍落在参照行前/后。
   */
  private patchRowAdd(): void {
    const dataSource = this.dataSource
    if (!dataSource || dataSource.__wbRowAddPatched) return
    const original = dataSource.rowAdd.bind(dataSource)
    dataSource.rowAdd = (...args: any[]) => {
      const rowId = original(...args)
      this.applyDefaultSource(dataSource, rowId)
      const anchorId = readAnchorRowId(args[0])
      if (anchorId) this.inheritSortValues(anchorId, rowId)
      return rowId
    }
    dataSource.__wbRowAddPatched = true
  }

  /**
   * 把参照行的排序键值复制给新行。
   *
   * 复制后两行排序键相同，稳定排序下新行紧跟在参照行前/后，视觉位置才符合
   * 「向前方 / 向后方插入」。标题列是行块自身的文本（spacial property），
   * 复制会造出同名行，因此明确跳过。
   */
  private inheritSortValues(anchorId: string, rowId: string): void {
    if (!rowId || rowId === anchorId) return
    const viewId = this.dataSource?.viewManager?.currentViewId$?.value
    if (!viewId) return
    const viewData = this.dataSource.viewDataGet(viewId)
    const columns = planInheritedSortColumns(viewData?.sort, [WORKBENCH_DB_COLUMNS.title])
    if (!columns) return
    for (const columnId of columns) {
      const value = this.dataSource.cellValueGet(anchorId, columnId)
      // 参照行该列本身为空时没有可继承的排序键，保持新行原样。
      if (value == null || value === '') continue
      this.dataSource.cellValueChange(rowId, columnId, value)
    }
  }

  /** 新建行补默认来源；选项被删掉时跳过，规则同 applyDefaultStatus。 */
  private applyDefaultSource(dataSource: any, rowId: string): void {
    if (!rowId) return
    const data = dataSource.propertyDataGet(WORKBENCH_DB_COLUMNS.source) as
      | { options?: Array<{ id: string }> }
      | undefined
    const options = Array.isArray(data?.options) ? data.options : []
    if (!options.some((option) => option.id === WORKBENCH_SOURCE_OPTIONS.manual)) return
    dataSource.cellValueChange(rowId, WORKBENCH_DB_COLUMNS.source, WORKBENCH_SOURCE_OPTIONS.manual)
  }

  /**
   * 当前视图的行 id（已应用排序与筛选），分页按这个顺序切页。
   *
   * 不能用 `dataSource.rows$`：那是原始顺序，表格渲染用的是视图顺序，
   * 视图有排序时两者不一致，分页会切错行。
   */
  viewRowIds(): string[] {
    const ui: any = this.rendererLogic()?.currentView$?.value
    const view: any = ui?.view ?? ui
    const rows: any[] = view?.rows$?.value ?? []
    return rows
      .map((row) => row?.rowId)
      .filter((id): id is string => typeof id === 'string' && id.length > 0)
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
      // CSV 没给状态时补「待开始」，否则该行会落进看板的「未分组」桶（已隐藏）而不可见。
      if (row.cells[WORKBENCH_DB_COLUMNS.status] == null) this.applyDefaultStatus(rowId)
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

  /**
   * 事项表的写入配置：列 id / 列名 / 关键选项文案。
   *
   * 选项文案必须从真实列数据里反查：建库语言不同，「待开始」「文档归档」的文案也不同，
   * 硬编码会把中文写进英文界面用户的事项表。
   */
  itemWriteSchema(sourceDocTitle?: string): ItemWriteSchema | null {
    if (!this.ensureBound()) return null
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    const byId = new Map(columns.map((column) => [column.id, column]))
    const status = byId.get(WORKBENCH_DB_COLUMNS.status)
    if (!status) return null
    const optionValue = (columnId: string, optionId: string): string => {
      const column = byId.get(columnId)
      if (!column) return ''
      return optionsOf(column).find((option) => option.id === optionId)?.value ?? ''
    }
    return {
      statusColumnId: status.id,
      statusColumnName: status.name,
      statusTodo: optionValue(WORKBENCH_DB_COLUMNS.status, 'wb-opt-todo'),
      statusDone: optionValue(WORKBENCH_DB_COLUMNS.status, 'wb-opt-done'),
      priorityColumnId: WORKBENCH_DB_COLUMNS.priority,
      priorityColumnName: byId.get(WORKBENCH_DB_COLUMNS.priority)?.name,
      ownerColumnId: WORKBENCH_DB_COLUMNS.owner,
      ownerColumnName: byId.get(WORKBENCH_DB_COLUMNS.owner)?.name,
      dateColumnId: WORKBENCH_DB_COLUMNS.date,
      dateColumnName: byId.get(WORKBENCH_DB_COLUMNS.date)?.name,
      relationColumnId: WORKBENCH_DB_COLUMNS.relation,
      relationColumnName: byId.get(WORKBENCH_DB_COLUMNS.relation)?.name,
      sourceColumnId: WORKBENCH_DB_COLUMNS.source,
      sourceColumnName: byId.get(WORKBENCH_DB_COLUMNS.source)?.name,
      sourceDocColumnId: WORKBENCH_DB_COLUMNS.sourceDoc,
      sourceDocColumnName: byId.get(WORKBENCH_DB_COLUMNS.sourceDoc)?.name,
      sourceDocTitle,
    }
  }

  /** 来源列某个取值的文案（manual / ai / archive）；列或选项缺失时返回空串。 */
  sourceLabel(kind: keyof typeof WORKBENCH_SOURCE_OPTIONS): string {
    if (!this.ensureBound()) return ''
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    const column = columns.find((item) => item.id === WORKBENCH_DB_COLUMNS.source)
    if (!column) return ''
    return optionsOf(column).find((option) => option.id === WORKBENCH_SOURCE_OPTIONS[kind])?.value ?? ''
  }

  /** 现有行的「标题 + 截止日」指纹，供抽取结果去重；取不到时返回空集合。 */
  existingFingerprints(): Set<string> {
    if (!this.ensureBound()) return new Set()
    const columns = columnsFromModel(this.dbModel, this.dataSource)
    const rows = rowsFromDataSource(this.dataSource, columns).map((row) => ({
      title: row.title,
      cells: Object.fromEntries(
        Object.entries(row.cells).map(([id, value]) => [
          id,
          // 日期列的真实值是毫秒时间戳，指纹只比较日期部分。
          id === WORKBENCH_DB_COLUMNS.date ? timestampToDateOnly(value) : String(value ?? ''),
        ]),
      ),
    }))
    return buildExistingFingerprints(rows, WORKBENCH_DB_COLUMNS.date)
  }

  /** 删除若干行；用于「撤销本次自动同步」。返回实际删除数量。 */
  deleteRows(rowIds: string[]): number {
    if (!this.ensureBound() || this.state.readonly) return 0
    const existing = new Set((this.dataSource.rows$.value as string[]) ?? [])
    const targets = rowIds.filter((id) => existing.has(id))
    if (!targets.length) return 0
    this.handle.store.transact(() => {
      this.dataSource.rowDelete(targets)
    })
    return targets.length
  }

  destroy(): void {
    this.destroyed = true
    this.resizeObserver?.disconnect()
    this.resizeObserver = null
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
 * 从 `rowAdd` 的入参里取出「参照行 id」。
 *
 * 只有 `{ id, before }` 这种相对位置才参照某一行；`'start'` / `'end'` / 数字下标
 * 没有参照行，返回空串（这些场景不需要继承排序键）。
 */
function readAnchorRowId(position: unknown): string {
  if (!position || typeof position !== 'object') return ''
  const id = (position as { id?: unknown }).id
  return typeof id === 'string' ? id : ''
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
