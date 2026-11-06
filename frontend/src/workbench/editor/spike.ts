/**
 * BlockSuite P0-3 spike 入口（独立 HTML，不进首页打包）。
 *
 * 页面自身不做业务渲染，只把一个可被自动化脚本驱动的 `window.__spike` 暴露出来：
 *   mount / destroy / snapshot / summary / clickView / addRows / deleteRow / setRowDate
 * 所有断言都由 scripts/editor-spike-check.mjs 通过 CDP 调用这些方法完成。
 */
import '@toeverything/theme/style.css'

import { DatabaseBlockDataSource } from '@blocksuite/affine/blocks/database'

import { mountEditor, type CanvasHandle, type EditorMode } from './blocksuite'
import {
  AUTOSAVE_DELAY_MS,
  MAX_SNAPSHOT_BYTES,
  createAutosave,
  createLocalStoragePersist,
  fromBase64,
  toBase64,
  type AutosaveHandle,
} from './spike-autosave'
import {
  PERF_DB_ROWS,
  SPIKE_BASE_DAY,
  SPIKE_DB_ROWS,
  createSpikePngBlob,
  formatLocalDate,
  seedDatabaseAndImage,
  startOfLocalDay,
  type SpikeSeedResult,
} from './spike-seed'
import { seedRichDoc, type RichSeedResult } from './spike-rich-doc'
import { runPerfBaseline, perfProgress } from './spike-perf'

const ITEM_ID = 'spike-page'
const IMAGE_ASSET_ID = 'spike:asset:png'
const AUTOSAVE_KEY = `spike:autosave:${ITEM_ID}`

const container = document.querySelector<HTMLDivElement>('#editor')
if (!container) throw new Error('[spike] 缺少 #editor 容器')

/** 每次挂载都重建资产仓库：证明图片靠 resolver 重新取，而不是复用了内存对象 */
let assetStore = new Map<string, Blob>()
let assetResolveCalls = 0

let handle: CanvasHandle | null = null
let autosave: AutosaveHandle | null = null
let seed: SpikeSeedResult | null = null
/** 从 store 反推的文档元信息（重挂载后 seed 为空，靠它拿 database/列/视图 id） */
let docInfo: DocInfo | null = null
let dataSource: any = null
let mountStartedAt = 0

interface DocInfo {
  databaseId: string
  imageId: string
  titleColumnId: string
  dateColumnId: string
  statusColumnId: string
  viewIds: { table: string; kanban: string; calendar: string }
}

function createAssetResolver() {
  return {
    resolve(assetId: string) {
      assetResolveCalls += 1
      return assetStore.get(assetId) ?? null
    },
  }
}

/** 挂载编辑器；snapshot 为空时走 seed 注入 100 行数据库 + image */
async function mount(snapshot?: Uint8Array | null): Promise<void> {
  destroy()
  mountStartedAt = Date.now()
  // 重新生成资产：模拟“刷新后从后端重新拉取”
  assetStore = new Map([[IMAGE_ASSET_ID, createSpikePngBlob()]])
  assetResolveCalls = 0

  autosave = createAutosave({
    key: AUTOSAVE_KEY,
    getSnapshot: () => {
      if (!handle) throw new Error('[spike] 编辑器尚未挂载')
      return handle.getSnapshot()
    },
  })

  handle = await mountEditor({
    element: container as HTMLDivElement,
    itemId: ITEM_ID,
    kind: 'canvas',
    mode: 'page',
    snapshot,
    editable: true,
    assets: createAssetResolver(),
    onChange: () => autosave?.markDirty(),
    seed: ctx => {
      seed = seedDatabaseAndImage(ctx, IMAGE_ASSET_ID)
    },
  })

  docInfo = resolveDocInfo(handle.store) ?? (seed ? toDocInfo(seed) : null)
  dataSource = docInfo ? new DatabaseBlockDataSource(handle.store.getModelById(docInfo.databaseId)) : null
  Object.assign(window, { __blocksuiteSpike: handle })
}

function toDocInfo(result: SpikeSeedResult): DocInfo {
  return {
    databaseId: result.databaseId,
    imageId: result.imageId,
    titleColumnId: result.titleColumnId,
    dateColumnId: result.dateColumnId,
    statusColumnId: result.statusColumnId,
    viewIds: result.viewIds,
  }
}

/**
 * 从 store 反查本次文档的 database / 列 / 视图 id。
 * 重挂载（snapshot 恢复）时 seed 不会执行，必须靠快照内容反推，否则断言拿不到 id。
 */
function resolveDocInfo(store: any): DocInfo | null {
  const note = store.root?.children?.find((child: any) => child.flavour === 'affine:note')
  const database = note?.children?.find((child: any) => child.flavour === 'affine:database')
  if (!database) return null
  const image = note?.children?.find((child: any) => child.flavour === 'affine:image')
  const views: any[] = database.props?.views$?.value ?? []
  const columns: any[] = database.props?.columns$?.value ?? []
  const columnByType = (type: string) => columns.find(column => column.type === type)?.id ?? ''
  const findView = (mode: string) => views.find(view => view.mode === mode)?.id ?? ''
  return {
    databaseId: database.id,
    imageId: image?.id ?? '',
    titleColumnId: columnByType('title'),
    dateColumnId: columnByType('date'),
    statusColumnId: columnByType('select'),
    viewIds: {
      table: findView('table'),
      kanban: findView('kanban'),
      calendar: findView('calendar'),
    },
  }
}

function destroy(): void {
  autosave?.dispose()
  autosave = null
  handle?.destroy()
  handle = null
  seed = null
  docInfo = null
  dataSource = null
  container!.innerHTML = ''
}

function requireHandle(): CanvasHandle {
  if (!handle) throw new Error('[spike] 编辑器尚未挂载')
  return handle
}

function requireDocInfo(): DocInfo {
  if (!docInfo) throw new Error('[spike] 尚未注入数据库内容')
  return docInfo
}

/** 读单元格值：直接读 database model 的 cells$，不经过视图层缓存 */
function readCellValue(rowId: string, columnId: string): unknown {
  const db = requireHandle().store.getModelById(requireDocInfo().databaseId)
  return db?.props?.cells$?.value?.[rowId]?.[columnId]?.value ?? null
}

function rowDate(rowId: string): string {
  const value = readCellValue(rowId, requireDocInfo().dateColumnId)
  return typeof value === 'number' ? formatLocalDate(value) : ''
}

function databaseModel(): any {
  return requireHandle().store.getModelById(requireDocInfo().databaseId)
}

/** 行数 = database 的 children 数量 */
function rowCount(): number {
  return databaseModel()?.children?.length ?? 0
}

function findRowId(index: number): string {
  return databaseModel()?.children?.[index]?.id ?? ''
}

// ---------------------------------------------------------------------------
// 视图切换与单元格操作
// ---------------------------------------------------------------------------

const VIEW_LABELS: Record<string, string> = {
  table: 'Table View',
  kanban: 'Kanban View',
  calendar: 'Calendar View',
}

function nextFrame(): Promise<void> {
  return nextFrames(1)
}

/** 等 n 帧，给 lit 的异步渲染留出时间 */
function nextFrames(count = 3): Promise<void> {
  return new Promise(resolve => {
    let left = count
    const step = () => (left-- <= 0 ? resolve() : requestAnimationFrame(step))
    requestAnimationFrame(step)
  })
}

/** 点击数据库视图栏按钮（真实 DOM 交互，避免直接改 manager 造成的“假切换”） */
async function clickView(type: 'table' | 'kanban' | 'calendar'): Promise<void> {
  const label = VIEW_LABELS[type]
  const buttons = [...document.querySelectorAll<HTMLElement>('.database-view-button')]
  const target = buttons.find(button => button.querySelector('.name')?.textContent?.trim() === label)
  if (!target) {
    throw new Error(`[spike] 找不到视图按钮：${label}（现有 ${buttons.length} 个）`)
  }
  target.click()
  await nextFrame()
  await nextFrame()
}

function addRows(count: number): string[] {
  const ids: string[] = []
  for (let i = 0; i < count; i += 1) {
    const rowId: string = dataSource.rowAdd('end')
    if (rowId) ids.push(rowId)
  }
  return ids
}

function deleteRows(rowIds: string[]): void {
  dataSource.rowDelete(rowIds)
}

function setRowDate(rowId: string, timestamp: number): void {
  // 走 dataSource.cellValueChange：与 UI 编辑单元格同一条路径（date 属性 = 时间戳）
  dataSource.cellValueChange(rowId, requireDocInfo().dateColumnId, timestamp)
}

function setRowTitle(rowId: string, text: string): void {
  const model = requireHandle().store.getModelById(rowId)
  model?.text?.replace(0, model.text.length, text)
}

// ---------------------------------------------------------------------------
// 摘要与证据采集
// ---------------------------------------------------------------------------

function imageEvidence() {
  const img = document.querySelector<HTMLImageElement>('affine-image img')
  return {
    found: Boolean(img),
    srcPrefix: img?.src?.slice(0, 12) ?? '',
    naturalWidth: img?.naturalWidth ?? 0,
    naturalHeight: img?.naturalHeight ?? 0,
    resolveCalls: assetResolveCalls,
  }
}

function viewEvidence() {
  const toolbar = [...document.querySelectorAll<HTMLElement>('.database-view-button')]
  return {
    buttons: toolbar.map(button => ({
      name: button.querySelector('.name')?.textContent?.trim() ?? '',
      selected: button.classList.contains('selected'),
    })),
    table: document.querySelectorAll('[data-testid="dv-table-view"]').length,
    kanban: document.querySelectorAll('.kanban-view').length,
    calendar: document.querySelectorAll('[data-testid="dv-calendar-view"]').length,
    calendarDays: document.querySelectorAll('.calendar-day[data-date]').length,
    calendarEntries: document.querySelectorAll('.calendar-entry').length,
  }
}

function storageRecord() {
  const raw = localStorage.getItem(AUTOSAVE_KEY)
  if (!raw) return null
  const record = JSON.parse(raw) as { base64: string; bytes: number; savedAt: number }
  return { bytes: record.bytes, savedAt: record.savedAt, base64Length: record.base64.length }
}

function summary() {
  const rows: string[] = databaseModel()?.children?.map((child: any) => child.id) ?? []
  const info = requireDocInfo()
  return {
    rowCount: rowCount(),
    seedRowCount: seed?.rowIds.length ?? rows.length,
    databaseId: info.databaseId,
    imageId: info.imageId,
    viewIds: info.viewIds,
    firstRowDates: rows.slice(0, 3).map(rowId => ({ rowId, date: rowDate(rowId) })),
    titleColumnId: info.titleColumnId,
    dateColumnId: info.dateColumnId,
    views: viewEvidence(),
    image: imageEvidence(),
    autosave: { ...(autosave?.stats() ?? { saves: 0, lastBytes: 0, status: 'idle' }) },
    storage: storageRecord(),
    snapshotBytes: requireHandle().getSnapshot().byteLength,
    mountMs: Date.now() - mountStartedAt,
  }
}

// ---------------------------------------------------------------------------
// 拖拽：合成 DragEvent 驱动 pragmatic-drag-and-drop（HTML5 DnD）
// ---------------------------------------------------------------------------

/**
 * 在 calendar 月视图里把第 index 个卡片拖到 targetDate。
 * 说明：headless 下没有真实鼠标拖拽循环，这里用合成 DragEvent 走完整 DnD 链路
 * （dragstart → dragenter → dragover → drop），仍是上游真实的 drop 处理逻辑。
 */
async function dragCalendarEntry(index: number, targetDate: number): Promise<{ ok: boolean; detail: string }> {
  const cards = [...document.querySelectorAll<HTMLElement>('.calendar-entry')]
  const card = cards[index]
  if (!card) return { ok: false, detail: `没有第 ${index} 个卡片（共 ${cards.length}）` }
  const day = document.querySelector<HTMLElement>(`.calendar-day[data-date="${targetDate}"]`)
  if (!day) return { ok: false, detail: `月视图里没有 ${formatLocalDate(targetDate)} 这一格` }

  const cardRect = card.getBoundingClientRect()
  const dayRect = day.getBoundingClientRect()
  const startX = cardRect.left + cardRect.width / 2
  const startY = cardRect.top + cardRect.height / 2
  const endX = dayRect.left + dayRect.width / 2
  const endY = dayRect.top + Math.min(40, dayRect.height / 2)

  const dataTransfer = new DataTransfer()
  const makeEvent = (type: string, target: EventTarget, x: number, y: number) =>
    target.dispatchEvent(
      new DragEvent(type, {
        bubbles: true,
        cancelable: true,
        composed: true,
        dataTransfer,
        clientX: x,
        clientY: y,
      })
    )

  makeEvent('dragstart', card, startX, startY)
  makeEvent('dragenter', day, endX, endY)
  makeEvent('dragover', day, endX, endY)
  await nextFrame()
  const dropNotCancelled = makeEvent('drop', day, endX, endY)
  makeEvent('dragend', card, endX, endY)
  await nextFrame()
  await nextFrame()

  return {
    ok: true,
    detail: `card[${index}] "${card.textContent?.trim() ?? ''}" -> ${formatLocalDate(targetDate)}，dropDefaultPrevented=${!dropNotCancelled}`,
  }
}

/**
 * P0-2 回归：不带 seed 的空白文档应能挂载并写入段落文本。
 * 用独立的临时容器，不影响主编辑器状态。
 */
async function blankDocCheck(): Promise<{ paragraphCount: number; text: string; snapshotBytes: number }> {
  const host = document.createElement('div')
  host.style.height = '300px'
  document.body.appendChild(host)
  const blank = await mountEditor({
    element: host,
    itemId: 'spike-blank',
    kind: 'canvas',
    mode: 'page',
    editable: true,
  })
  try {
    const root = blank.store.root
    const note = root?.children?.find((child: any) => child.flavour === 'affine:note')
    const paragraph = note?.children?.find((child: any) => child.flavour === 'affine:paragraph')
    paragraph?.text.insert('空白页可输入', 0)
    return {
      paragraphCount: note?.children?.filter((child: any) => child.flavour === 'affine:paragraph').length ?? 0,
      text: paragraph?.text?.toString() ?? '',
      snapshotBytes: blank.getSnapshot().byteLength,
    }
  } finally {
    blank.destroy()
    host.remove()
  }
}

// ---------------------------------------------------------------------------
// P0-5：20 元素富文本 + 双根（page/edgeless）切换
// ---------------------------------------------------------------------------

const RICH_ITEM_ID = 'spike-rich'

interface BlockInventory {
  count: number
  flavours: Record<string, number>
  texts: string[]
  databaseRows: number
  rootId: string
  blockIds: string[]
  /** 树里出现重复 id 时记录（正常为空） */
  duplicateIds: string[]
}

/** 深度遍历 store 的 block 树，产出可与快照恢复结果逐项比对的清单 */
function blockInventory(store: any): BlockInventory {
  const ids: string[] = []
  const flavours: Record<string, number> = {}
  const texts: string[] = []
  let databaseRows = 0
  const walk = (model: any) => {
    ids.push(model.id)
    flavours[model.flavour] = (flavours[model.flavour] ?? 0) + 1
    const text = model.text?.toString?.() ?? ''
    if (text) texts.push(text)
    if (model.flavour === 'affine:database') databaseRows = model.children.length
    model.children.forEach(walk)
  }
  if (store.root) walk(store.root)
  return {
    count: ids.length,
    flavours,
    texts,
    databaseRows,
    rootId: store.root?.id ?? '',
    blockIds: ids,
    duplicateIds: ids.filter((id, index) => ids.indexOf(id) !== index),
  }
}

/** 在独立容器里挂载一份文档，不干扰主编辑器 */
async function mountInTempHost(
  itemId: string,
  mode: EditorMode,
  snapshot?: Uint8Array | null
): Promise<{ handle: CanvasHandle; host: HTMLDivElement }> {
  const host = document.createElement('div')
  host.style.height = '600px'
  document.body.appendChild(host)
  const handle = await mountEditor({
    element: host,
    itemId,
    kind: 'canvas',
    mode,
    snapshot,
    editable: true,
    seed: ctx => {
      richSeedBox.value = seedRichDoc(ctx, RICH_IMAGE_ASSET_ID)
    },
    assets: {
      resolve: () => createSpikePngBlob(),
    },
  })
  return { handle, host }
}

/** seed 结果放在可变盒子里：闭包内赋值，调用方在 await 之后再读 */
const richSeedBox: { value: RichSeedResult | null } = { value: null }
// 用函数读取，避免 TS 把属性访问收窄成 null（赋值发生在闭包里）
const readRichSeed = (): RichSeedResult | null => richSeedBox.value
const RICH_IMAGE_ASSET_ID = 'spike:asset:rich'

/**
 * 20 元素证据：建文档 → 取快照 → 销毁 → 用快照重挂载 → 逐项比对。
 * 返回前后两份清单，由断言脚本做严格比对。
 */
async function richDocRoundTrip() {
  richSeedBox.value = null
  const createStart = performance.now()
  const first = await mountInTempHost(RICH_ITEM_ID, 'page')
  const createMs = Math.round(performance.now() - createStart)
  const before = blockInventory(first.handle.store)
  const snapshot = first.handle.getSnapshot()
  const seedRowIds = readRichSeed()?.rowIds ?? []
  first.handle.destroy()
  first.host.innerHTML = ''

  const restoreStart = performance.now()
  const second = await mountInTempHost(RICH_ITEM_ID, 'page', snapshot)
  const restoreMs = Math.round(performance.now() - restoreStart)
  const after = blockInventory(second.handle.store)
  const secondSnapshotBytes = second.handle.getSnapshot().byteLength
  const stats = second.handle.stats()
  second.handle.destroy()
  second.host.remove()
  first.host.remove()

  return {
    before,
    after,
    seedRowIds,
    snapshotBytes: snapshot.byteLength,
    restoredSnapshotBytes: secondSnapshotBytes,
    /** P3-7：20 元素文档的挂载与恢复耗时（毫秒） */
    timings: { createMs, restoreMs },
    /** 恢复时必然有一次 applyUpdate：这是快照写回 Y.Doc 的正常路径 */
    restoreApplyUpdateCalls: stats.applyUpdateCalls,
    identical: JSON.stringify(before) === JSON.stringify(after),
  }
}

/** 统计当前容器里 root 元素与 block 元素的重复情况 */
function domInventory(host: HTMLElement) {
  const pageRoots = host.querySelectorAll('affine-page-root').length
  const edgelessRoots = host.querySelectorAll('affine-edgeless-root').length
  const ids = [...host.querySelectorAll<HTMLElement>('[data-block-id]')].map(
    el => el.dataset.blockId ?? ''
  )
  return {
    pageRoots,
    edgelessRoots,
    blockElements: ids.length,
    duplicateBlockElements: [...new Set(ids.filter((id, i) => ids.indexOf(id) !== i))],
  }
}

/** 每个 block 的 props 快照，用于定位「切换后字节数变化」来自哪个 block 的哪些属性 */
function propsSnapshot(store: any): Record<string, Record<string, unknown>> {
  const out: Record<string, Record<string, unknown>> = {}
  const walk = (model: any) => {
    out[model.id] = { flavour: model.flavour, ...JSON.parse(JSON.stringify(model.props ?? {})) }
    model.children.forEach(walk)
  }
  if (store.root) walk(store.root)
  return out
}

/** 对比两份 props 快照，列出发生变化的 block 与属性名 */
function diffProps(
  before: Record<string, Record<string, unknown>>,
  after: Record<string, Record<string, unknown>>
): Array<{ id: string; flavour: string; changedKeys: string[] }> {
  return Object.keys(after)
    .filter(id => before[id])
    .map(id => {
      const keys = new Set([...Object.keys(before[id]), ...Object.keys(after[id])])
      const changedKeys = [...keys].filter(
        key => JSON.stringify(before[id][key]) !== JSON.stringify(after[id][key])
      )
      return { id, flavour: String(after[id].flavour), changedKeys }
    })
    .filter(entry => entry.changedKeys.length > 0)
}

/**
 * 双根切换证据：在同一 store 上 page ↔ edgeless 往返。
 * 关键点：切换只调 handle.setMode（重建 std/host），不碰 Y.Doc。
 */
async function rootSwitchCheck(cycles = 5) {
  richSeedBox.value = null
  const { handle, host } = await mountInTempHost('spike-dual-root', 'page')
  const pageBefore = blockInventory(handle.store)
  const propsOnPage = propsSnapshot(handle.store)
  const snapshotBefore = handle.getSnapshot()
  const statsBefore = handle.stats()
  const domBefore = domInventory(host)
  // 记下 Y.Doc / store 的对象引用，切换后比对「是不是同一个实例」
  const storeRef = handle.store
  const spaceDocRef = handle.doc.spaceDoc

  handle.setMode('edgeless')
  await nextFrames()
  // P2-8：视口参数（平移/缩放）也要跨往返保持，不能每次切回来都“自动适应内容”
  const viewportOnEnter = handle.getViewport()
  const viewportApplied = handle.setViewport({ zoom: 1.5, x: 320, y: 240 })
  await nextFrames(1)
  const viewportMoved = handle.getViewport()
  const edgelessInventory = blockInventory(handle.store)
  const domEdgeless = domInventory(host)
  const snapshotInEdgeless = handle.getSnapshot()
  // 定位首次切到 edgeless 时多出来的字节来自哪个 block 的哪些 props
  const propsAfterEdgeless = propsSnapshot(handle.store)  // 在 edgeless 新增 block 前取证
  const firstSwitchPropDiff = diffProps(propsOnPage, propsAfterEdgeless)

  // 在 edgeless root 下新增一个 block（同一 store，等价于画布里加一段文字）
  const noteId = handle.store.root.children.find((c: any) => c.flavour === 'affine:note')?.id
  const addedId: string = handle.store.addBlock('affine:paragraph', {}, noteId)
  handle.store.getModelById(addedId)?.text.insert('edgeless 下新增的段落', 0)
  await nextFrames()

  handle.setMode('page')
  await nextFrames()
  const pageAfter = blockInventory(handle.store)
  const domPageAfter = domInventory(host)

  // 剩余往返：只记录总量与字节数，检查是否会累积
  const rounds: Array<{ round: number; mode: EditorMode; blocks: number; bytes: number; dupes: number }> = []
  for (let i = 1; i < cycles; i += 1) {
    const next: EditorMode = i % 2 === 1 ? 'edgeless' : 'page'
    handle.setMode(next)
    await nextFrames(1)
    const inventory = blockInventory(handle.store)
    rounds.push({
      round: i,
      mode: next,
      blocks: inventory.count,
      bytes: handle.getSnapshot().byteLength,
      dupes: inventory.duplicateIds.length,
    })
  }
  // 往返结束后再回到 edgeless，读一次视口：与离开前的值比对
  handle.setMode('edgeless')
  await nextFrames()
  const viewportAfterRoundTrips = handle.getViewport()
  const statsAfter = handle.stats()
  const pageAfterBytes = handle.getSnapshot().byteLength
  // 切换前后 store / spaceDoc 必须是同一实例（否则就是「重建 doc」而不是双根切换）
  const identity = {
    sameStore: storeRef === handle.store,
    sameSpaceDoc: spaceDocRef === handle.doc.spaceDoc,
    sameRootId: storeRef.root?.id === handle.store.root?.id,
  }
  handle.destroy()
  host.remove()

  return {
    pageBefore,
    edgelessInventory,
    pageAfter,
    addedBlockId: addedId,
    bytes: {
      pageBefore: snapshotBefore.byteLength,
      edgeless: snapshotInEdgeless.byteLength,
      pageAfter: pageAfterBytes,
    },
    viewport: {
      onEnter: viewportOnEnter,
      applied: viewportApplied,
      moved: viewportMoved,
      afterRoundTrips: viewportAfterRoundTrips,
    },
    applyUpdate: { before: statsBefore.applyUpdateCalls, after: statsAfter.applyUpdateCalls },
    viewMounts: { before: statsBefore.viewMounts, after: statsAfter.viewMounts },
    viewUnmounts: statsAfter.viewUnmounts,
    modeSwitches: statsAfter.modeSwitches,
    /** 切换是否复用了同一个 store / 同一个 Y.Doc 实例 */
    identity,
    dom: { pageBefore: domBefore, edgeless: domEdgeless, pageAfter: domPageAfter },
    /** 首次 page→edgeless 时发生变化的 block props（解释字节数 +37 的来源） */
    firstSwitchPropDiff,
    rounds,
  }
}

// ---------------------------------------------------------------------------
// 8MB 边界：验证保存队列拒绝而不是静默截断
// ---------------------------------------------------------------------------

async function testOversizeRejection(): Promise<{ rejected: boolean; error?: string; wroteToStorage: boolean }> {
  const oversized = new Uint8Array(MAX_SNAPSHOT_BYTES + 1024)
  const probeKey = `${AUTOSAVE_KEY}:oversize-probe`
  localStorage.removeItem(probeKey)
  const probe = createAutosave({
    key: probeKey,
    getSnapshot: () => oversized,
    persist: createLocalStoragePersist(),
    delayMs: 0,
  })
  probe.markDirty()
  await probe.flush()
  const stats = probe.stats()
  probe.dispose()
  const wroteToStorage = localStorage.getItem(probeKey) !== null
  localStorage.removeItem(probeKey)
  return {
    rejected: stats.status === 'error',
    error: stats.error,
    wroteToStorage,
  }
}

// ---------------------------------------------------------------------------
// 对外 API
// ---------------------------------------------------------------------------

const spikeApi = {
  constants: { AUTOSAVE_DELAY_MS, MAX_SNAPSHOT_BYTES, SPIKE_DB_ROWS, SPIKE_BASE_DAY, PERF_DB_ROWS },
  mount: (snapshotBase64?: string | null) =>
    mount(snapshotBase64 ? fromBase64(snapshotBase64) : null),
  destroy,
  summary,
  snapshotBase64: () => toBase64(requireHandle().getSnapshot()),
  snapshotBytes: () => requireHandle().getSnapshot().byteLength,
  clickView,
  addRows,
  rowCount,
  rowIdAt: findRowId,
  deleteRows,
  setRowDate,
  setRowTitle,
  rowDate,
  rowDateAt: (index: number) => rowDate(findRowId(index)),
  baseDay: () => SPIKE_BASE_DAY,
  startOfLocalDay,
  formatLocalDate,
  dragCalendarEntry,
  blankDocCheck,
  testOversizeRejection,
  richDocRoundTrip,
  rootSwitchCheck,
  /** P3-7：性能基线 + 三视图回归（独立容器，不影响主编辑器） */
  perfBaseline: (rows?: number) => runPerfBaseline(rows),
  perfProgress,
  // 主编辑器的形态切换（路线 2）：同一 store/doc 换 root，不重放快照
  setMode: (mode: EditorMode) => requireHandle().setMode(mode),
  getMode: () => requireHandle().getMode(),
  editorStats: () => requireHandle().stats(),
  flushAutosave: () => autosave?.flush(),
  autosaveStats: () => autosave?.stats() ?? null,
  storageRecord,
  viewEvidence,
  imageEvidence,
  /** 触发一次真实编辑器变更（供 2 秒防抖断言） */
  typeIntoFirstParagraph: (text: string) => {
    const root = requireHandle().store.root
    const note = root?.children?.find((child: any) => child.flavour === 'affine:note')
    const paragraph = note?.children?.find((child: any) => child.flavour === 'affine:paragraph')
    if (!paragraph) throw new Error('[spike] 找不到第一个 paragraph')
    paragraph.text.insert(text, paragraph.text.length)
    return paragraph.text.toString()
  },
}

Object.assign(window, { __spike: spikeApi })

// 默认自动挂载一次，便于人工打开页面查看
mount()
  .then(() => console.info('[blocksuite-spike] mounted', summary()))
  .catch((error: unknown) => {
    console.error('[blocksuite-spike] mount failed', error)
    container.textContent = `BlockSuite 挂载失败：${String(error)}`
  })

export {}
