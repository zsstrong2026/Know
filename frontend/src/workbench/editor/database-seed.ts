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

import type { EditorSeedContext } from './blocksuite'
import { WORKBENCH_DB_COLUMNS } from './database-columns'
import { PROTOTYPE_CARD_PROPERTIES, PROTOTYPE_COLUMN_WIDTHS, SEED_COLUMN_WIDTH } from './database-view-defaults'

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
 * 列集合与原型（AFFiNE-Know原型.html 的事项表格）一致：事项 / 状态 / 负责人 /
 * 截止日 / 优先级 / 关联文档。表格视图的列顺序由视图数据决定，这里只保证列定义存在。
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
    { id: WORKBENCH_DB_COLUMNS.owner, type: 'text', name: labels.ownerColumn, data: {} },
    { id: WORKBENCH_DB_COLUMNS.date, type: 'date', name: labels.dateColumn, data: {} },
    // 优先级只做展示与筛选；上游没有 person / relation 类型，负责人与关联项都用文本承载。
    { id: WORKBENCH_DB_COLUMNS.priority, type: 'select', name: labels.priorityColumn, data: { options: PRIORITY_OPTIONS } },
    { id: WORKBENCH_DB_COLUMNS.relation, type: 'text', name: labels.relationColumn, data: {} },
  ]
}

/**
 * 清理误加的四列（描述 / 需求来源 / 价值达成 / 所属版本）。
 *
 * 这四列是按旧原型误加进来的，真实原型（AFFiNE-Know原型.html）的事项表只有六列。
 * 已经写进 Y.Doc 的条目需要把列定义删掉，否则表头会多出四个空列。幂等。
 */
const OBSOLETE_COLUMN_IDS = ['wb-col-desc', 'wb-col-source', 'wb-col-value', 'wb-col-version']

export function removeObsoleteWorkbenchColumns(store: BsStore): boolean {
  const databaseId = findDatabaseId(store)
  const model = databaseId ? store.getModelById(databaseId) : null
  const current: any[] = model?.props?.columns$?.value ?? []
  if (!model || !current.length) return false
  const next = current.filter((column) => !OBSOLETE_COLUMN_IDS.includes(column?.id))
  if (next.length === current.length) return false
  store.updateBlock(databaseId, { columns: next })
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
    // 列宽按原型比例一次性写好（事项宽、状态/优先级窄），六列一屏能看全。
    columns: dataSource.properties$.value.map((id: string) => ({
      id,
      width: PROTOTYPE_COLUMN_WIDTHS[id] ?? SEED_COLUMN_WIDTH,
    })),
  }))
  return viewId
}

/** 看板：只补卡片可见属性，分组交给上游 defaultData 解析。 */
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
