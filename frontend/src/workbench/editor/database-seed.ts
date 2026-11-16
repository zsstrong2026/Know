/**
 * 新建「数据库」条目时的初始内容（P3-2）。
 *
 * 与 `spike-seed.ts` 的区别：这里是产品路径，不是 P0 验证。
 *   - 行数为 0（用户自己加行），只有 3 列 + 3 视图的骨架；
 *   - 列名走 i18n，六语言都能用；
 *   - 只依赖 BlockSuite 的公开数据源 API（`DatabaseBlockDataSource`），
 *     不手写 ViewBasicDataType —— 上游 calendar 视图的 defaultData 带必需字段，
 *     手写会停在空态（见 p0-spike-report §3 第 6 条）。
 */
import { DatabaseBlockDataSource } from '@blocksuite/affine/blocks/database'
import { Text } from '@blocksuite/store'

import type { EditorSeedContext } from './blocksuite'
import {
  WORKBENCH_DB_COLUMNS,
  WORKBENCH_DB_SOURCE_COLUMNS,
  WORKBENCH_SOURCE_OPTIONS,
} from './database-columns'
import {
  PROTOTYPE_CARD_PROPERTIES,
  SOURCE_COLUMN_WIDTH,
  defaultColumnWidth,
} from './database-view-defaults'

type BsStore = any

/**
 * 数据库骨架的列定义；id 固定，便于其他模块按类型反查。
 *
 * 定义搬到了 `database-columns.ts`（纯常量、可被单测引用），这里原样再导出，
 * 保持既有 `import { WORKBENCH_DB_COLUMNS } from './database-seed'` 不受影响。
 */
export { WORKBENCH_DB_COLUMNS }

export interface DatabaseSeedLabels {
  titleColumn: string
  statusColumn: string
  dateColumn: string
  ownerColumn: string
  priorityColumn: string
  relationColumn: string
  /** 来源列：取值 手动 / AI 识别 / 文档归档 */
  sourceColumn: string
  /** 来源文档列：抽取自哪篇文档的条目名快照 */
  sourceDocColumn: string
  /** 来源列的三个选项；顺序固定为 手动 → AI 识别 → 文档归档 */
  sourceOptions: [string, string, string]
  /** 数据库块标题：上游默认渲染英文 “Untitled”，这里跟条目名保持一致 */
  databaseTitle: string
  /** 状态列的三个选项；顺序固定为 待开始 → 进行中 → 已完成。 */
  statusOptions: [string, string, string]
  /**
   * 三个视图的名字。上游默认名是英文（Table View / Kanban View / Calendar View），
   * 落到 Y.Doc 后不会随语言切换，因此建视图时就写成本地化名称。
   */
  viewNames: { table: string; kanban: string; calendar: string }
}

/** 来源列的选项 id；定义搬到 `database-columns.ts`，这里原样再导出。 */
export { WORKBENCH_SOURCE_OPTIONS }

/** 优先级选项：P0/P1/P2 是跨语言通用写法，不做本地化。 */
const PRIORITY_OPTIONS = [
  { id: 'wb-pri-p0', value: 'P0', color: 'var(--affine-tag-red)' },
  { id: 'wb-pri-p1', value: 'P1', color: 'var(--affine-tag-orange)' },
  { id: 'wb-pri-p2', value: 'P2', color: 'var(--affine-tag-gray)' },
]

export interface DatabaseSeedResult {
  databaseId: string
  viewIds: { table: string; kanban: string; calendar: string }
}

/** select 列的一组选项；id 固定，避免每次重建文档都换一批 id。 */
function selectOptions(ids: readonly string[], values: readonly string[], colors: readonly string[]) {
  return values.map((value, index) => ({ id: ids[index], value, color: colors[index] }))
}

/**
 * 工作台数据库的列定义（建库与老条目补列共用）。
 *
 * 前六列与原型（AFFiNE-Know原型.html 的事项表格）一致：事项 / 状态 / 负责人 /
 * 截止日 / 优先级 / 关联文档；后两列是「来源 / 来源文档」，用于记录事项从哪来
 * （手动录入、AI 识别、文档归档抽取），放在末尾不影响原型六列的观感。
 */
function buildWorkbenchColumns(labels: DatabaseSeedLabels) {
  return [
    { id: WORKBENCH_DB_COLUMNS.title, type: 'title', name: labels.titleColumn, data: {} },
    {
      id: WORKBENCH_DB_COLUMNS.status,
      type: 'select',
      name: labels.statusColumn,
      data: { options: selectOptions(['wb-opt-todo', 'wb-opt-doing', 'wb-opt-done'], labels.statusOptions, ['var(--affine-tag-gray)', 'var(--affine-tag-blue)', 'var(--affine-tag-green)']) },
    },
    { id: WORKBENCH_DB_COLUMNS.owner, type: 'rich-text', name: labels.ownerColumn, data: {} },
    { id: WORKBENCH_DB_COLUMNS.date, type: 'date', name: labels.dateColumn, data: {} },
    // 优先级只做展示与筛选；上游没有 person / relation 类型，负责人与关联项都用富文本承载。
    // 注意类型名必须是 `rich-text`：上游没有 `text` 这个属性类型，写成 text 的列
    // 拿不到属性元信息，读不到值也渲染不出内容（表现为「写了但表里看不到」）。
    { id: WORKBENCH_DB_COLUMNS.priority, type: 'select', name: labels.priorityColumn, data: { options: PRIORITY_OPTIONS } },
    { id: WORKBENCH_DB_COLUMNS.relation, type: 'rich-text', name: labels.relationColumn, data: {} },
    // 来源两列：默认可见（用户明确要求可追溯），排在想定位置末尾。
    {
      id: WORKBENCH_DB_COLUMNS.source,
      type: 'select',
      name: labels.sourceColumn,
      data: {
        options: selectOptions(
          [WORKBENCH_SOURCE_OPTIONS.manual, WORKBENCH_SOURCE_OPTIONS.ai, WORKBENCH_SOURCE_OPTIONS.archive],
          labels.sourceOptions,
          ['var(--affine-tag-gray)', 'var(--affine-tag-purple)', 'var(--affine-tag-teal)'],
        ),
      },
    },
    { id: WORKBENCH_DB_COLUMNS.sourceDoc, type: 'rich-text', name: labels.sourceDocColumn, data: {} },
  ]
}

/**
 * 老条目补齐「来源 / 来源文档」两列（幂等）。
 *
 * 为什么需要：列定义写在 Y.Doc 的 `columns` 里，改 seed 只对新建条目生效，
 * 而事项是全局单例、只建一次，不补列老用户永远看不到来源。
 *
 * 只追加缺失的两列（按 id 判断），不重排、不改已有列；同时把它们补进每个
 * 表格视图的 `columns`，否则视图只显示建视图时的那几列。
 */
export function ensureWorkbenchSourceColumns(
  store: BsStore,
  labels: DatabaseSeedLabels,
): boolean {
  const databaseId = findDatabaseId(store)
  if (!databaseId) return false
  const model = store.getModelById(databaseId)
  const current: any[] = model?.props?.columns$?.value ?? []
  if (!model || !current.length) return false

  const existing = new Set(current.map((column) => column?.id))
  // 只补来源两列：其余列即使用户删掉也不应该被「复活」。
  const missing = buildWorkbenchColumns(labels).filter(
    (column) =>
      WORKBENCH_DB_SOURCE_COLUMNS.includes(column.id as (typeof WORKBENCH_DB_SOURCE_COLUMNS)[number]) &&
      !existing.has(column.id),
  )
  if (!missing.length) return false

  store.updateBlock(databaseId, { columns: [...current, ...missing] })

  // 视图层的列顺序存在 view data 里：新建列若不补进视图，表格会「有列但看不到」。
  // 上游会在视图激活时 materialize，这里主动补齐可避免首帧缺列。
  const dataSource = new DatabaseBlockDataSource(model)
  const viewIds: string[] = dataSource.viewManager.views$.value ?? []
  if (!viewIds.length) return true
  for (const viewId of viewIds) {
    const data = dataSource.viewDataGet(viewId)
    if (data?.mode !== 'table') continue
    const columns: any[] = Array.isArray(data.columns) ? data.columns : []
    const present = new Set(columns.map((column) => column.id))
    const appended = missing
      .filter((column) => !present.has(column.id))
      .map((column) => ({ id: column.id, width: SOURCE_COLUMN_WIDTH }))
    if (!appended.length) continue
    dataSource.viewDataUpdate(viewId, (viewData: any) => ({
      ...viewData,
      columns: [...columns, ...appended],
    }))
  }
  return true
}

/** 来源两列在表格视图里的默认宽度见 `database-view-defaults.ts` 的 SOURCE_COLUMN_WIDTH。 */

/**
 * 早期版本把「负责人 / 关联文档 / 来源文档」建成了 `text` 类型。
 *
 * 上游根本没有 `text` 这个属性类型（纯文本列真实类型名是 `rich-text`），
 * 这类列拿不到属性元信息：`propertyMetaGet('text')` 为 undefined，
 * 于是没有单元格渲染器、值也读不出来——表现为「写了但表里永远空白」
 * （AI 识别出的来源文档就是这么丢的）。
 *
 * 这里把这三列就地改成 `rich-text`，并把已写入的纯字符串包成 Text，
 * 保证历史数据也能显示。幂等：没有 `text` 列时直接返回 false。
 */
const LEGACY_TEXT_COLUMN_IDS = new Set<string>([
  WORKBENCH_DB_COLUMNS.owner,
  WORKBENCH_DB_COLUMNS.relation,
  WORKBENCH_DB_COLUMNS.sourceDoc,
])

export function migrateLegacyTextColumns(store: BsStore): boolean {
  const databaseId = findDatabaseId(store)
  const model = databaseId ? store.getModelById(databaseId) : null
  const current: any[] = model?.props?.columns$?.value ?? []
  if (!model || !current.length) return false

  const targets = current.filter(
    (column) => column?.type === 'text' && LEGACY_TEXT_COLUMN_IDS.has(column.id),
  )
  if (!targets.length) return false

  store.updateBlock(databaseId, {
    columns: current.map((column) =>
      targets.includes(column) ? { ...column, type: 'rich-text' } : column,
    ),
  })

  // 列类型改完后，把仍是纯字符串的历史值包成 Text；富文本列的原始值必须是 Text。
  // 这里直接从块 props 的原始 cells 读：改类型后 `cellValueGet` 会按 rich-text 的
  // schema 校验，旧字符串会被判为不合法而返回 undefined，读不到原值。
  const dataSource = new DatabaseBlockDataSource(model)
  const cells = model.props.cells$?.value ?? {}
  const rowIds: string[] = dataSource.rows$.value ?? []
  store.transact(() => {
    for (const column of targets) {
      for (const rowId of rowIds) {
        const raw = cells[rowId]?.[column.id]?.value
        if (typeof raw !== 'string' || !raw.trim()) continue
        dataSource.cellValueChange(rowId, column.id, new Text(raw))
      }
    }
  })
  return true
}

/**
 * 清理误加的列（描述 / 需求来源 / 价值达成 / 所属版本 / Progress）。
 *
 * 前四列是按旧原型误加进来的，真实原型（AFFiNE-Know原型.html）的事项表只有六列；
 * `wbjg7w6g-1` 是早期一次误操作留下的 progress 列，会让表头多出 “Progress”
 * 并把表格撑出横向滚动。已经写进 Y.Doc 的条目需要把列定义删掉，否则表头会多列。幂等。
 */
const OBSOLETE_COLUMN_IDS = [
  'wb-col-desc',
  'wb-col-source',
  'wb-col-value',
  'wb-col-version',
  'wbjg7w6g-1',
]

export function removeObsoleteWorkbenchColumns(store: BsStore): boolean {
  const databaseId = findDatabaseId(store)
  const model = databaseId ? store.getModelById(databaseId) : null
  const current: any[] = model?.props?.columns$?.value ?? []
  if (!model || !current.length) return false
  const next = current.filter((column) => !OBSOLETE_COLUMN_IDS.includes(column?.id))
  if (next.length === current.length) return false
  store.updateBlock(databaseId, { columns: next })

  // 视图层另存一份列顺序与列宽：只删块级列的话，表头仍会照视图配置把旧列渲染出来。
  const dataSource = new DatabaseBlockDataSource(model)
  const viewIds: string[] = dataSource.viewManager.views$.value ?? []
  for (const viewId of viewIds) {
    const data = dataSource.viewDataGet(viewId)
    const columns: any[] = Array.isArray(data?.columns) ? data.columns : []
    if (!columns.some((column) => OBSOLETE_COLUMN_IDS.includes(column?.id))) continue
    dataSource.viewDataUpdate(viewId, (viewData: any) => ({
      ...viewData,
      columns: (Array.isArray(viewData?.columns) ? viewData.columns : []).filter(
        (column: any) => !OBSOLETE_COLUMN_IDS.includes(column?.id),
      ),
    }))
  }
  return true
}

/**
 * 清理识别事项时被自动建出来的「待办」状态选项（幂等）。
 *
 * 背景：早期识别写入直接采用模型返回的 status；不在选项表里的值会被写入层
 * 自动补成新选项，于是看板多出一列「待办」，与「待开始」语义重复。
 * 这里把该选项下的行迁回「待开始」，再从状态列里删掉选项。
 *
 * 只在内置「待开始」选项还在时处理：选项被用户删过就跳过，不凭空造状态。
 */
const LEGACY_TODO_STATUS_LABELS = new Set([
  '待办',
  '待辦',
  '待办事项',
  '待辦事項',
  'todo',
  'to do',
  'to-do',
])

export function removeLegacyTodoStatusOption(store: BsStore): boolean {
  const databaseId = findDatabaseId(store)
  const model = databaseId ? store.getModelById(databaseId) : null
  if (!model) return false
  const dataSource = new DatabaseBlockDataSource(model)
  const data = dataSource.propertyDataGet(WORKBENCH_DB_COLUMNS.status) as
    | { options?: Array<{ id: string; value: string }> }
    | undefined
  const options = Array.isArray(data?.options) ? data.options : []
  if (!options.some((option) => option.id === 'wb-opt-todo')) return false

  const legacyIds = new Set(
    options
      .filter(
        (option) =>
          option.id !== 'wb-opt-todo' &&
          LEGACY_TODO_STATUS_LABELS.has(option.value.trim().toLowerCase()),
      )
      .map((option) => option.id),
  )
  if (!legacyIds.size) return false

  const rowIds: string[] = dataSource.rows$.value ?? []
  store.transact(() => {
    for (const rowId of rowIds) {
      const current = dataSource.cellValueGet(rowId, WORKBENCH_DB_COLUMNS.status)
      if (typeof current === 'string' && legacyIds.has(current)) {
        dataSource.cellValueChange(rowId, WORKBENCH_DB_COLUMNS.status, 'wb-opt-todo')
      }
    }
    dataSource.propertyDataSet(WORKBENCH_DB_COLUMNS.status, {
      ...data,
      options: options.filter((option) => !legacyIds.has(option.id)),
    })
  })
  return true
}

/**
 * 在第一个 note 里插入一个空数据库块，并建好三种视图。
 *
 * 视图顺序与原型一致：表格（默认）→ 看板 → Calendar。
 * 看板按「状态」列分列、Calendar 按「截止日」列取日期，都由 columns 决定。
 */
export function seedWorkbenchDatabase(
  { store }: EditorSeedContext,
  labels: DatabaseSeedLabels,
): DatabaseSeedResult {
  const noteId: string | undefined = store.root?.children?.find(
    (child: any) => child.flavour === 'affine:note',
  )?.id
  if (!noteId) throw new Error('[workbench-db] 缺少 note block')

  const databaseId: string = store.addBlock('affine:database', {}, noteId)
  store.updateBlock(databaseId, {
    columns: buildWorkbenchColumns(labels),
    cells: {},
    views: [],
  } as any)

  // 数据库块自带的标题行默认是英文 “Untitled”，这里写成条目名，避免与顶栏标题重复且不本地化。
  const model = store.getModelById(databaseId)
  const blockTitle = model?.props?.title
  if (blockTitle?.replace && labels.databaseTitle) {
    blockTitle.replace(0, blockTitle.length, labels.databaseTitle)
  }

  const dataSource = new DatabaseBlockDataSource(model)
  return {
    databaseId,
    viewIds: {
      table: createTableView(dataSource, labels.viewNames.table),
      // 看板的 groupBy 由上游 defaultData 自动挑选第一个可分组列（这里是状态列），
      // 手写会缺 hideEmpty / sort 等字段，反而让视图停在空态。
      kanban: createKanbanView(dataSource, labels.viewNames.kanban),
      calendar: createCalendarView(dataSource, WORKBENCH_DB_COLUMNS.date, labels.viewNames.calendar),
    },
  }
}

/** table 的 defaultData.columns 是空数组，不补的话表头/单元格都不渲染。 */
function createTableView(dataSource: any, name: string): string {
  const viewId: string = dataSource.viewManager.viewAdd('table')
  dataSource.viewDataUpdate(viewId, () => ({
    name,
    // 列宽按原型比例一次性写好（事项宽、状态/优先级窄），八列（含来源）一屏能看全。
    columns: dataSource.properties$.value.map((id: string) => ({
      id,
      width: defaultColumnWidth(id),
    })),
  }))
  return viewId
}

/** 看板：只补卡片可见属性与「显示空列」，分组交给上游 defaultData 解析。 */
function createKanbanView(dataSource: any, name: string): string {
  const viewId: string = dataSource.viewManager.viewAdd('kanban')
  dataSource.viewDataUpdate(viewId, (data: any) => ({
    ...data,
    name,
    // 状态是分组列，卡片上不重复显示；按原型展示优先级 / 截止日 / 负责人。
    card: {
      ...data.card,
      visiblePropertyIds: [...PROTOTYPE_CARD_PROPERTIES],
    },
    // 三个状态列常显（即使某列暂时没有卡片），否则用户会以为「状态只剩存在的几种」。
    // 未分组列由 CSS 按「是否有卡片」决定显隐：空的隐藏，有卡片的保留。
    groupBy: data.groupBy ? { ...data.groupBy, hideEmpty: false } : data.groupBy,
  }))
  return viewId
}

/** calendar 必须指定 date.startColumnId，否则停在“请选择日期属性”空态。 */
function createCalendarView(dataSource: any, dateColumnId: string, name: string): string {
  const viewId: string = dataSource.viewManager.viewAdd('calendar')
  dataSource.viewDataUpdate(viewId, (data: any) => ({
    ...data,
    name,
    date: { ...data.date, startColumnId: dateColumnId },
    card: { ...data.card, visiblePropertyIds: [WORKBENCH_DB_COLUMNS.status] },
  }))
  return viewId
}

/** 找到 store 里的第一个 database 块 id；独立视图与 CSV 都需要它。 */
export function findDatabaseId(store: BsStore): string {
  const root = store?.root
  if (!root) return ''
  for (const note of root.children ?? []) {
    for (const child of note.children ?? []) {
      if (child.flavour === 'affine:database') return child.id
    }
  }
  return ''
}

/** 数据库块在 store 里的模型；不存在返回 null。 */
export function getDatabaseModel(store: BsStore): any | null {
  const id = findDatabaseId(store)
  return id ? store.getModelById(id) : null
}
