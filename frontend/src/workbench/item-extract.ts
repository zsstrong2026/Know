/**
 * 归档后自动把文档里的安排写进「事项」表。
 *
 * 为什么不用「暂存 + 等事项页消费」：用户明确要求归档后直接入库，
 * 而归档页（文档条目）没有事项表的编辑器实例，因此这里用一次**离屏会话**：
 *   1. 取事项条目的最新快照；
 *   2. 挂到隐藏容器里，读到数据库块与列配置；
 *   3. 用同一个 `applyDatabasePatch` 写入（与事项页共用一条写路径）；
 *   4. 立刻落盘并销毁编辑器。
 *
 * 约束：
 *   - 归档不能因为事项同步失败而失败：所有异常在这里吞掉，只返回统计；
 *   - 写盘用快照里的 `snapshot_version` 作为 base_version，若另一标签页正在编辑，
 *     服务端会返回 2401，本次同步放弃（不覆盖用户正在改的表）；
 *   - 撤销只在本次会话内有效：据返回的行 id 再开一次离屏会话删除。
 */
import { getWorkbenchContent, requestItemCandidates, saveWorkbenchContent } from '@/api/workbench'
import { WORKBENCH_DB_COLUMNS } from '@/workbench/editor/database-columns'
import { columnsFromModel, optionsOf } from '@/workbench/editor/database-csv'
import { applyDatabasePatch } from '@/workbench/editor/database-ai'
import { timestampToDateOnly } from '@/workbench/editor/database-dates'
import { buildExistingFingerprints, buildItemPatch, dedupeCandidates } from '@/workbench/ai/item-candidates'
import type { ItemWriteSchema } from '@/workbench/ai/item-candidates'
import { base64ToBytes, bytesToBase64 } from '@/workbench/editor/use-editor-session'
import { projectMarkdown } from '@/workbench/editor/projection'
import { currentClientId } from '@/workbench/save-queue'
import { useWorkbenchStore } from '@/stores/workbench'

/** 结果：写入行数与行 id（撤销用）；0 表示没有可同步的内容。 */
export interface ItemSyncOutcome {
  rows: number
  rowIds: string[]
  /** 失败原因（仅用于日志/调试，不对用户展示细节） */
  error?: string
}

/** 离屏会话的公共骨架：挂载 → 回调 → 落盘 → 销毁。 */
async function withThingsEditor<T>(
  handler: (ctx: {
    store: any
    model: any
    dataSource: any
    schema: ItemWriteSchema
  }) => T,
  sourceTitle?: string,
): Promise<T | null> {
  const store = useWorkbenchStore()
  const things = await store.ensureThingsItem()
  const content = await getWorkbenchContent(things.id)
  const host = document.createElement('div')
  // 必须离开视口但仍参与布局：BlockSuite 的渲染依赖尺寸探测。
  host.style.cssText = 'position:fixed;left:-20000px;top:0;width:1000px;height:800px;'
  document.body.appendChild(host)

  const { mountEditor } = await import('@/workbench/editor/blocksuite')
  const { DatabaseBlockDataSource } = await import('@blocksuite/affine/blocks/database')
  const { ensureWorkbenchSourceColumns, migrateLegacyTextColumns, seedWorkbenchDatabase } = await import(
    '@/workbench/editor/database-seed'
  )
  const { editorSeedLabels } = await import('@/workbench/editor/use-editor-session')
  const labels = editorSeedLabels()
  const handle = await mountEditor({
    element: host,
    itemId: things.id,
    kind: 'database',
    mode: 'page',
    snapshot: content.ydoc ? base64ToBytes(content.ydoc) : null,
    editable: true,
    // 事项表还没被打开过时是空快照：这里补建，否则没有数据库块可写。
    seed: (ctx) => {
      seedWorkbenchDatabase(ctx, { ...labels, databaseTitle: things.title })
    },
  })
  try {
    // 老表可能还没有「来源 / 来源文档」两列：写入前先补齐，保证来源能落库。
    migrateLegacyTextColumns(handle.store)
    ensureWorkbenchSourceColumns(handle.store, labels)
    const databaseId = findDatabaseId(handle.store)
    const model = databaseId ? handle.store.getModelById(databaseId) : null
    if (!model) return null
    const dataSource = new DatabaseBlockDataSource(model)
    const schema = buildSchema(dataSource, model, sourceTitle)
    if (!schema) return null
    const result = handler({ store: handle.store, model, dataSource, schema })

    // 立刻落盘：离屏会话没有保存队列，不能依赖防抖。
    const markdown = projectMarkdown(handle.store).markdown
    await saveWorkbenchContent(things.id, {
      base_version: content.snapshot_version,
      editor_schema_version: content.editor_schema_version,
      ydoc: bytesToBase64(handle.getSnapshot()),
      text_content: markdown,
      client_id: currentClientId(),
    })
    return result
  } finally {
    handle.destroy()
    host.remove()
  }
}

/** 从列定义读出写入配置；状态列缺失时返回 null（表结构异常，不做任何写入）。 */
function buildSchema(dataSource: any, model: any, sourceTitle?: string): ItemWriteSchema | null {
  const columns = columnsFromModel(model, dataSource)
  const byId = new Map(columns.map((column) => [column.id, column]))
  const status = byId.get(WORKBENCH_DB_COLUMNS.status)
  if (!status) return null
  const optionValue = (columnId: string, optionId: string): string => {
    const column = byId.get(columnId)
    if (!column) return ''
    return optionsOf(column).find((option) => option.id === optionId)?.value ?? ''
  }
  const sourceOptions = byId.get(WORKBENCH_DB_COLUMNS.source)
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
    sourceLabel: sourceOptions
      ? (optionsOf(sourceOptions).find((option) => option.id === 'wb-src-archive')?.value ?? '')
      : '',
    sourceDocColumnId: WORKBENCH_DB_COLUMNS.sourceDoc,
    sourceDocColumnName: byId.get(WORKBENCH_DB_COLUMNS.sourceDoc)?.name,
    sourceDocTitle: sourceTitle,
  }
}

/**
 * 识别正文里的安排并写入事项表。
 *
 * `markdown` 用归档时提交的同一份正文，避免「归档了一份、识别了另一份」。
 */
export async function syncItemsAfterArchive(options: {
  itemId: string
  sourceTitle: string
  markdown: string
  agentId?: string
}): Promise<ItemSyncOutcome> {
  try {
    const response = await requestItemCandidates({
      item_id: options.itemId,
      markdown: options.markdown,
      agent_id: options.agentId,
      source_title: options.sourceTitle,
      limit: 20,
    })
    const { kept } = dedupeCandidates(response.items ?? [])
    if (!kept.length) return { rows: 0, rowIds: [] }

    const outcome = await withThingsEditor(
      ({ store, model, dataSource, schema }) => {
        // 与事项表已有行去重：同标题 + 同截止日不重复写。
        // 日期列的原始值是毫秒时间戳，先转成 YYYY-MM-DD 再建指纹（否则永远对不上）。
        const columns = columnsFromModel(model, dataSource)
        const titleColumn = columns.find((column) => column.type === 'title')
        const rows = (dataSource.rows$.value as string[]).map((row) => ({
          title: titleColumn
            ? String(dataSource.cellValueGet(row, titleColumn.id)?.toString?.() ?? '')
            : '',
          cells: {
            [WORKBENCH_DB_COLUMNS.date]: timestampToDateOnly(
              dataSource.cellValueGet(row, WORKBENCH_DB_COLUMNS.date),
            ),
          },
        }))
        const existing = buildExistingFingerprints(rows, WORKBENCH_DB_COLUMNS.date)
        const { kept: fresh } = dedupeCandidates(kept, existing)
        if (!fresh.length) return { rows: 0, rowIds: [] }
        const patch = buildItemPatch(fresh, schema)
        const result = applyDatabasePatch({
          store,
          dataSource,
          model,
          patch,
          allowOverwrite: false,
        })
        return { rows: result.rows, rowIds: result.rowIds }
      },
      options.sourceTitle,
    )
    return outcome ?? { rows: 0, rowIds: [] }
  } catch (error) {
    return { rows: 0, rowIds: [], error: String(error) }
  }
}

/** 撤销一次同步：按行 id 删除（行已不存在时忽略）。 */
export async function undoItemSync(rowIds: string[]): Promise<number> {
  if (!rowIds.length) return 0
  try {
    const deleted = await withThingsEditor(({ store, dataSource }) => {
      const existing = new Set((dataSource.rows$.value as string[]) ?? [])
      const targets = rowIds.filter((id) => existing.has(id))
      if (!targets.length) return 0
      store.transact(() => dataSource.rowDelete(targets))
      return targets.length
    })
    return deleted ?? 0
  } catch {
    return 0
  }
}

/** 与 database-seed 同一套遍历规则：找第一个数据库块。 */
function findDatabaseId(store: any): string {
  for (const note of store?.root?.children ?? []) {
    for (const child of note.children ?? []) {
      if (child.flavour === 'affine:database') return child.id
    }
  }
  return ''
}
