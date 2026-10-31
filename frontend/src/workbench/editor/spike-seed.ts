/**
 * P0-3 spike 的内容注入逻辑（与 blocksuite.ts 解耦，由 spike.ts 通过 seed 回调传入）。
 *
 * 写入方式统一走 model/Y.Doc：
 *   - affine:database 的 rows = 其 children（affine:paragraph）
 *   - columns / cells / views 是 database 的 prop
 *   - 视图结构必须由 viewManager.viewAdd(type) 生成（calendar/kanban 的 defaultData 带必需字段），
 *     再叠加 calendar 的 date 映射，手写 ViewBasicDataType 会缺字段导致渲染空态
 */
import { DatabaseBlockDataSource } from '@blocksuite/affine/blocks/database'

import type { EditorSeedContext } from './blocksuite'

type BsStore = any

export const SPIKE_DB_ROWS = 100
const DAY_MS = 24 * 60 * 60 * 1000

/** 本月 1 号本地 0 点：保证 100 行里前若干行落在当前月网格内，便于日历断言 */
export const SPIKE_BASE_DAY = (() => {
  const now = new Date()
  return new Date(now.getFullYear(), now.getMonth(), 1).getTime()
})()

export interface SpikeSeedResult {
  databaseId: string
  imageId: string
  titleColumnId: string
  dateColumnId: string
  statusColumnId: string
  viewIds: { table: string; kanban: string; calendar: string }
  rowIds: string[]
}

/** 本地日历日 0 点，避免 toISOString 造成东八区前后移一天 */
export function startOfLocalDay(timestamp: number): number {
  const date = new Date(timestamp)
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
}

export function formatLocalDate(timestamp: number): string {
  const date = new Date(timestamp)
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/**
 * 注入 database（100 行 / 3 列 / 3 视图）+ image block。
 * imageAssetId 由调用方写入 AssetResolver 的资产仓库，这里只存 sourceId。
 */
export function seedDatabaseAndImage(
  { store }: EditorSeedContext,
  imageAssetId: string
): SpikeSeedResult {
  const root = store.root
  if (!root) throw new Error('[spike-seed] 缺少根 block')
  const noteId = root.children.find((child: any) => child.flavour === 'affine:note')?.id
  if (!noteId) throw new Error('[spike-seed] 缺少 note block')

  const titleColumnId = 'spike-title'
  const dateColumnId = 'spike-date'
  const statusColumnId = 'spike-status'
  const statusOptions = [
    { id: 'opt-todo', value: 'Todo', color: 'var(--affine-tag-blue)' },
    { id: 'opt-doing', value: 'Doing', color: 'var(--affine-tag-orange)' },
    { id: 'opt-done', value: 'Done', color: 'var(--affine-tag-green)' },
  ]

  const databaseId: string = store.addBlock('affine:database', {}, noteId)

  // 1. 行：database 的 children 即行，标题写在 paragraph.text 上（title 列是 spacial property）
  //    注意 store.transact 不返回 fn 的结果，id 要收集到外层数组
  const rowIds: string[] = []
  store.transact(() => {
    for (let i = 0; i < SPIKE_DB_ROWS; i += 1) {
      const rowId: string = store.addBlock('affine:paragraph', {}, databaseId)
      store.getModelById(rowId)?.text.insert(`Row ${String(i + 1).padStart(3, '0')}`, 0)
      rowIds.push(rowId)
    }
  })

  // 2. columns / cells：标题列 id 用 'spike-title' + type 'title'，
  //    data-source 会把它当作 fixed property 的替代列，properties$ 里保留顺序
  const cells: Record<string, Record<string, { columnId: string; value: unknown }>> = {}
  rowIds.forEach((rowId, index) => {
    cells[rowId] = {
      [dateColumnId]: { columnId: dateColumnId, value: SPIKE_BASE_DAY + index * DAY_MS },
      [statusColumnId]: { columnId: statusColumnId, value: statusOptions[index % 3].id },
    }
  })
  store.updateBlock(databaseId, {
    columns: [
      { id: titleColumnId, type: 'title', name: 'Title', data: {} },
      { id: dateColumnId, type: 'date', name: 'Date', data: {} },
      { id: statusColumnId, type: 'select', name: 'Status', data: { options: statusOptions } },
    ],
    cells,
    views: [],
  } as any)

  // 3. 三视图：用 dataSource.viewManager 生成默认结构，再补 table 列 / calendar 日期映射
  const dataSource = new DatabaseBlockDataSource(store.getModelById(databaseId))
  const viewIds = {
    table: createTableView(dataSource),
    kanban: dataSource.viewManager.viewAdd('kanban') as string,
    calendar: createCalendarView(dataSource, dateColumnId),
  }

  // 4. image block：sourceId 交给 AssetResolver 解析（渲染前不会写死 blob URL）
  const imageId: string = store.addBlock(
    'affine:image',
    { sourceId: imageAssetId, width: 120, height: 120, caption: 'spike-asset' },
    noteId
  )

  return {
    databaseId,
    imageId,
    titleColumnId,
    dateColumnId,
    statusColumnId,
    viewIds,
    rowIds,
  }
}

function createTableView(dataSource: any): string {
  const viewId: string = dataSource.viewManager.viewAdd('table')
  // table 的 defaultData.columns 是空数组，不补的话表头/单元格都不渲染
  dataSource.viewDataUpdate(viewId, () => ({
    columns: dataSource.properties$.value.map((id: string) => ({ id, width: 160 })),
  }))
  return viewId
}

/** calendar 必须指定 date.startColumnId，否则停在 "Select or create date property" 空态 */
function createCalendarView(dataSource: any, dateColumnId: string): string {
  const viewId: string = dataSource.viewManager.viewAdd('calendar')
  dataSource.viewDataUpdate(viewId, (data: any) => ({
    date: { ...data.date, startColumnId: dateColumnId },
    card: { ...data.card, visiblePropertyIds: ['spike-status'] },
  }))
  return viewId
}

/**
 * 生成 8x8 纯色 PNG Blob（洋红），用于验证 blobSync → ResourceController → <img> 链路。
 * 手写 PNG 字节而不用 canvas：headless 下 canvas 未必可用，且这样零依赖。
 */
export function createSpikePngBlob(): Blob {
  const width = 8
  const height = 8
  const raw: number[] = []
  for (let y = 0; y < height; y += 1) {
    raw.push(0) // filter: none
    for (let x = 0; x < width; x += 1) raw.push(220, 20, 130, 255)
  }
  const png = concatBytes(
    new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', ihdrBytes(width, height)),
    pngChunk('IDAT', zlibStore(new Uint8Array(raw))),
    pngChunk('IEND', new Uint8Array(0))
  )
  return new Blob([png as Uint8Array<ArrayBuffer>], { type: 'image/png' })
}

/** zlib「存储块」编码：只需要生成合法 PNG，不需要压缩率 */
function zlibStore(data: Uint8Array): Uint8Array {
  const out: number[] = [0x78, 0x01]
  const maxBlock = 65535
  for (let offset = 0; offset < data.length || offset === 0; offset += maxBlock) {
    const chunk = data.subarray(offset, offset + maxBlock)
    const isLast = offset + chunk.length >= data.length
    out.push(isLast ? 1 : 0)
    out.push(chunk.length & 0xff, (chunk.length >> 8) & 0xff)
    out.push(~chunk.length & 0xff, (~chunk.length >> 8) & 0xff)
    for (const byte of chunk) out.push(byte)
  }
  const adler = adler32(data)
  out.push((adler >>> 24) & 0xff, (adler >>> 16) & 0xff, (adler >>> 8) & 0xff, adler & 0xff)
  return new Uint8Array(out)
}

function adler32(data: Uint8Array): number {
  let a = 1
  let b = 0
  for (const byte of data) {
    a = (a + byte) % 65521
    b = (b + a) % 65521
  }
  return ((b << 16) | a) >>> 0
}

function ihdrBytes(width: number, height: number): Uint8Array {
  const bytes = new Uint8Array(13)
  const view = new DataView(bytes.buffer)
  view.setUint32(0, width)
  view.setUint32(4, height)
  bytes[8] = 8 // bit depth
  bytes[9] = 6 // color type: RGBA
  return bytes
}

function pngChunk(type: string, data: Uint8Array): Uint8Array<ArrayBuffer> {
  const typeBytes = new Uint8Array([...type].map(ch => ch.charCodeAt(0)))
  const body = concatBytes(typeBytes, data)
  const out = new Uint8Array(12 + data.length)
  const view = new DataView(out.buffer)
  view.setUint32(0, data.length)
  out.set(body, 4)
  view.setUint32(8 + data.length, crc32(body))
  return out
}

let crcTable: Uint32Array | null = null
function crc32(bytes: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256)
    for (let i = 0; i < 256; i += 1) {
      let c = i
      for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
      crcTable[i] = c >>> 0
    }
  }
  let crc = 0xffffffff
  for (const byte of bytes) crc = crcTable[(crc ^ byte) & 0xff] ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const part of parts) {
    out.set(part, offset)
    offset += part.length
  }
  return out
}
