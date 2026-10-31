/**
 * P0-5 富文本文档 seed：为「20 元素编辑 + 保存/恢复」与「双根切换」提供固定内容。
 *
 * 与 spike-seed.ts（100 行数据库）分开：这里追求 block 种类覆盖，不追求数据量。
 * 全部通过 store.addBlock 写入 Y.Doc，因此 page / edgeless 两种 root 读的是同一份数据。
 */
import { DatabaseBlockDataSource } from '@blocksuite/affine/blocks/database'

import type { EditorSeedContext } from './blocksuite'

type BsStore = any

/** 21 个 block 的骨架（root/surface/note/image/database 由本文件其余函数补齐） */
interface BlockSpec {
  flavour: string
  text?: string
  props?: Record<string, unknown>
}

export const RICH_BLOCK_SPECS: BlockSpec[] = [
  { flavour: 'affine:paragraph', props: { type: 'h1' }, text: 'P0-5 标题 H1' },
  { flavour: 'affine:paragraph', props: { type: 'h2' }, text: '二级标题 H2' },
  { flavour: 'affine:paragraph', props: { type: 'h3' }, text: '三级标题 H3' },
  { flavour: 'affine:paragraph', text: '普通段落：验证快照恢复后的正文一致性。' },
  { flavour: 'affine:paragraph', text: '第二个段落：双根切换后文本必须一致。' },
  { flavour: 'affine:paragraph', props: { type: 'quote' }, text: '引用块：路线 2 双根切换验证。' },
  { flavour: 'affine:list', props: { type: 'bulleted' }, text: '无序列表项 A' },
  { flavour: 'affine:list', props: { type: 'numbered' }, text: '有序列表项 1' },
  { flavour: 'affine:list', props: { type: 'todo' }, text: '待办列表项' },
  { flavour: 'affine:code', props: { language: 'typescript' }, text: 'const mode = "page" | "edgeless"' },
  { flavour: 'affine:divider' },
]

export interface RichSeedResult {
  databaseId: string
  imageId: string
  tableViewId: string
  rowIds: string[]
  /** 供选择器/断言使用的固定文本（写入后不再变化） */
  markers: string[]
}

export const RICH_DB_ROWS = 5

/** 写入 21 个 block：标题 3 + 段落 3 + 列表 3 + 引用 1 + 代码 1 + 分割线 1 + 图片 1 + 数据库（1 + 5 行） */
export function seedRichDoc({ store }: EditorSeedContext, imageAssetId: string): RichSeedResult {
  const noteId = findNoteId(store)
  RICH_BLOCK_SPECS.forEach(spec => addSpecBlock(store, noteId, spec))
  const table = seedRichDatabase(store, noteId)
  const imageId = addSpecBlock(store, noteId, {
    flavour: 'affine:image',
    props: { sourceId: imageAssetId, width: 120, height: 120, caption: 'rich-asset' },
  })
  return {
    ...table,
    imageId,
    markers: RICH_BLOCK_SPECS.map(spec => spec.text).filter((text): text is string => Boolean(text)),
  }
}

function findNoteId(store: BsStore): string {
  const noteId = store.root?.children?.find((child: any) => child.flavour === 'affine:note')?.id
  if (!noteId) throw new Error('[spike-rich] 缺少 note block')
  return noteId
}

/** 追加一个 block 并写入文本；divider 之类没有 text 的 block 直接跳过 */
function addSpecBlock(store: BsStore, parentId: string, spec: BlockSpec): string {
  const id: string = store.addBlock(spec.flavour, spec.props ?? {}, parentId)
  if (spec.text) store.getModelById(id)?.text?.insert(spec.text, 0)
  return id
}

/** 数据库：2 列 + 5 行 + 1 个 table 视图（双根切换里 database 在两种 root 下都要可读） */
function seedRichDatabase(store: BsStore, noteId: string): Omit<RichSeedResult, 'imageId' | 'markers'> {
  const databaseId: string = store.addBlock('affine:database', {}, noteId)
  const rowIds: string[] = []
  store.transact(() => {
    for (let i = 1; i <= RICH_DB_ROWS; i += 1) {
      const rowId: string = store.addBlock('affine:paragraph', {}, databaseId)
      store.getModelById(rowId)?.text.insert(`数据库行 ${i}`, 0)
      rowIds.push(rowId)
    }
  })
  store.updateBlock(databaseId, buildDatabaseProps(rowIds))
  const dataSource = new DatabaseBlockDataSource(store.getModelById(databaseId))
  return { databaseId, tableViewId: createTableView(dataSource), rowIds }
}

function buildDatabaseProps(rowIds: string[]) {
  const dateColumnId = 'rich-date'
  const cells: Record<string, Record<string, { columnId: string; value: unknown }>> = {}
  rowIds.forEach((rowId, index) => {
    cells[rowId] = { [dateColumnId]: { columnId: dateColumnId, value: Date.now() + index * 86400000 } }
  })
  return {
    columns: [
      { id: 'rich-title', type: 'title', name: 'Title', data: {} },
      { id: dateColumnId, type: 'date', name: 'Date', data: {} },
    ],
    cells,
    views: [],
  } as any
}

/** table 视图的 defaultData.columns 为空，必须按 properties$ 补齐，否则表头/单元格不渲染 */
function createTableView(dataSource: any): string {
  const viewId: string = dataSource.viewManager.viewAdd('table')
  dataSource.viewDataUpdate(viewId, () => ({
    columns: dataSource.properties$.value.map((id: string) => ({ id, width: 160 })),
  }))
  return viewId
}
