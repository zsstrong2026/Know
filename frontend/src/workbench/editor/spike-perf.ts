/**
 * P3-7 性能基线探针（1000 行数据库 + 三视图回归）。
 *
 * 只做测量与取证，不做断言：阈值判断放在 scripts/editor-spike-check.mjs，
 * 这样基线数据能被脚本原样打印，回归时可以直接看出是哪一项退化。
 *
 * 测量口径（与实现方案 §14.7 对齐）：
 *   - 新建 / 恢复：从调用 mountEditor 到表格行真正出现在 DOM 的耗时；
 *   - 保存：`Y.encodeStateAsUpdate` 的耗时与字节数；
 *   - 编辑：单元格写入 → 值生效 → 两帧渲染完成的往返耗时（预热 1 次 + 连写 5 次取中位数）；
 *   - 三视图：点击真实视图按钮 → 容器出现 → 行数不变。
 */
import { mountEditor, type CanvasHandle } from './blocksuite'
import { PERF_DB_ROWS, createSpikePngBlob, seedDatabaseAndImage } from './spike-seed'

const PERF_ITEM_ID = 'spike-perf'
const PERF_ASSET_ID = 'spike:asset:perf'
const HOST_HEIGHT = '800px'
/** 单步最长等待；超时抛错而不是返回假数据 */
const STEP_TIMEOUT_MS = 30000

const VIEW_LABELS: Record<string, string> = {
  table: 'Table View',
  kanban: 'Kanban View',
  calendar: 'Calendar View',
}

export interface PerfViewRun {
  mode: string
  switchMs: number
  /** dataSource.rows$ 的行数（数据层） */
  rows: number
  /** 该视图真正渲染出来的容器数量 */
  containers: number
}

export interface PerfBaseline {
  rows: number
  create: { mountMs: number; tableReadyMs: number; domRows: number; blocks: number }
  snapshot: { encodeMs: number; bytes: number }
  restore: {
    mountMs: number
    tableReadyMs: number
    rows: number
    domRows: number
    blocks: number
    duplicateIds: string[]
  }
  views: PerfViewRun[]
  edit: { latencyMs: number; coldMs: number; samples: number[]; applied: boolean }
  scroll: { ms: number; domRows: number; scrolled: boolean }
  rowOps: { addMs: number; deleteMs: number; rows: number }
}

function now(): number {
  return performance.now()
}

/** 当前所处阶段：1000 行基线耗时较长，外部靠它定位卡在哪一步 */
let progressStage = 'idle'
let progressStartedAt = now()

export function perfProgress(): { stage: string; elapsedMs: number } {
  return { stage: progressStage, elapsedMs: Math.round(now() - progressStartedAt) }
}

function markStage(stage: string): void {
  progressStage = stage
  progressStartedAt = now()
  console.info(`[spike-perf] ${stage}`)
}

/**
 * 等 n 帧，给 lit 的异步渲染留时间。
 *
 * 用 rAF 与 setTimeout 竞速：页面被判定为不可见（无头/后台标签）时 rAF 可能不触发，
 * 只靠 rAF 会让等待永久挂住、连超时判断都走不到。
 */
function nextFrames(count = 1): Promise<void> {
  return new Promise(resolve => {
    let left = count
    const step = () => (left-- <= 0 ? resolve() : scheduleFrame(step))
    scheduleFrame(step)
  })
}

function scheduleFrame(callback: () => void): void {
  let done = false
  const finish = () => {
    if (done) return
    done = true
    callback()
  }
  requestAnimationFrame(finish)
  setTimeout(finish, 32)
}

function nextFrame(): Promise<void> {
  return nextFrames(1)
}

/** 轮询直到条件成立，返回等待耗时；超时抛错，避免把「没渲染出来」量成 0ms */
async function waitUntil(predicate: () => boolean, label: string): Promise<number> {
  const start = now()
  while (now() - start < STEP_TIMEOUT_MS) {
    if (predicate()) return now() - start
    await nextFrame()
  }
  throw new Error(`[spike-perf] 等待超时：${label}`)
}

/** 从已渲染的 data-view 上取上游 dataSource（与页面共用同一份视图与行数据） */
function readDataSource(host: HTMLElement): any | null {
  return (host.querySelector('affine-data-view-renderer') as { logic?: any } | null)?.logic?.config
    ?.dataSource ?? null
}

function countRowsInDom(host: HTMLElement): number {
  return host.querySelectorAll('data-view-table-row').length
}

function countViewContainers(host: HTMLElement, type: string): number {
  if (type === 'table') return host.querySelectorAll('[data-testid="dv-table-view"]').length
  if (type === 'kanban') return host.querySelectorAll('.kanban-view').length
  return host.querySelectorAll('.calendar-day[data-date]').length
}

/** 点上视图栏按钮（真实 DOM 交互，与用户切换走同一条链路） */
async function clickViewButton(host: HTMLElement, type: string): Promise<void> {
  const label = VIEW_LABELS[type]
  const buttons = [...host.querySelectorAll<HTMLElement>('.database-view-button')]
  const target = buttons.find(button => button.querySelector('.name')?.textContent?.trim() === label)
  if (!target) throw new Error(`[spike-perf] 找不到视图按钮：${label}`)
  target.click()
  await nextFrame()
  await nextFrame()
}

/**
 * 最近的可纵向滚动祖先：用于验证 1000 行滚动不卡死。
 *
 * 找不到显式 `overflow: auto/scroll` 时退回文档滚动元素：编辑器宿主没有设置
 * overflow 时，实际滚动的是整页，这时不返回 null 才能量到真实的滚动耗时。
 */
function findScrollable(start: Element | null): HTMLElement | null {
  let node: Element | null = start
  while (node && node !== document.body) {
    const style = getComputedStyle(node)
    const scrollable = /(auto|scroll)/.test(style.overflowY) && node.scrollHeight > node.clientHeight + 4
    if (scrollable) return node as HTMLElement
    node = node.parentElement
  }
  const root = document.scrollingElement as HTMLElement | null
  return root && root.scrollHeight > root.clientHeight + 4 ? root : null
}

/** 收集容器内重复的 block id（切换/恢复后出现重复是硬故障） */
function duplicateBlockIds(host: HTMLElement): string[] {
  const ids = [...host.querySelectorAll<HTMLElement>('[data-block-id]')].map(el => el.dataset.blockId ?? '')
  return [...new Set(ids.filter((id, index) => id && ids.indexOf(id) !== index))]
}

function countBlocks(store: any): number {
  let count = 0
  const walk = (model: any) => {
    count += 1
    model.children.forEach(walk)
  }
  if (store.root) walk(store.root)
  return count
}

async function mountPerfDoc(
  host: HTMLElement,
  rowCount: number,
  snapshot?: Uint8Array | null
): Promise<CanvasHandle> {
  return mountEditor({
    element: host,
    itemId: PERF_ITEM_ID,
    kind: 'database',
    mode: 'page',
    snapshot,
    editable: true,
    assets: { resolve: () => createSpikePngBlob() },
    seed: ctx => {
      seedDatabaseAndImage(ctx, PERF_ASSET_ID, rowCount)
    },
  })
}

/**
 * 一次完整基线：新建 → 保存 → 恢复 → 三视图 → 编辑 → 滚动 → 增删行。
 * 所有临时 DOM 在结束时清理，不污染主编辑器。
 *
 * rowCount 可调：先用小行数确认链路，再跑 1000 行基线。
 */
export async function runPerfBaseline(rowCount: number = PERF_DB_ROWS): Promise<PerfBaseline> {
  const host = document.createElement('div')
  host.style.height = HOST_HEIGHT
  // 真实工作台里编辑器在可滚动的视口内；临时容器若不加 overflow，1000 行会被裁掉、
  // 量不到滚动成本（scrollTop 赋值无效）。这里显式做滚动容器。
  host.style.overflowY = 'auto'
  document.body.appendChild(host)

  let handle: CanvasHandle | null = null
  let restored: CanvasHandle | null = null
  try {
    // 1. 新建：空白文档 + rowCount 行 seed（含 database / 三列 / 三视图）
    markStage('create:mount')
    const createStart = now()
    handle = await mountPerfDoc(host, rowCount)
    const createdMountMs = round(now() - createStart)
    // 挂载返回 ≠ lit 渲染完成：data-view-renderer 是首帧之后才连接的
    markStage('create:data-view')
    await waitUntil(() => readDataSource(host) !== null, '新建后 data-view 渲染')
    const dataSource = readDataSource(host)
    if (!dataSource) throw new Error('[spike-perf] 新建后拿不到 dataSource')
    markStage('create:rows')
    await waitUntil(() => countRowsInDom(host) > 0, '新建后表格行渲染')
    const create = {
      mountMs: createdMountMs,
      // 建库到「行已渲染」的总耗时 = mount 调用 + data-view 连接 + 首行渲染
      tableReadyMs: round(now() - createStart),
      domRows: countRowsInDom(host),
      blocks: countBlocks(handle.store),
    }
    const rows = (dataSource.rows$.value as string[]).length

    // 2. 保存：编码一次快照（真实保存链路的 CPU 部分）
    markStage('snapshot:encode')
    const encodeStart = now()
    const snapshot = handle.getSnapshot()
    const snapshotRun = { encodeMs: round(now() - encodeStart), bytes: snapshot.byteLength }

    // 3. 恢复：销毁后用同一份快照重挂载
    markStage('restore:mount')
    handle.destroy()
    host.innerHTML = ''
    const restoreStart = now()
    restored = await mountPerfDoc(host, rowCount, snapshot)
    // 快照写回 Y.Doc + 建 store + 首次视图挂载的耗时（不含 lit 行渲染）
    const restoredMountMs = round(now() - restoreStart)
    markStage('restore:data-view')
    await waitUntil(() => readDataSource(host) !== null, '恢复后 data-view 渲染')
    const restoredSource = readDataSource(host)
    if (!restoredSource) throw new Error('[spike-perf] 恢复后拿不到 dataSource')
    markStage('restore:rows')
    await waitUntil(() => countRowsInDom(host) > 0, '恢复后表格行渲染')
    const restore = {
      mountMs: restoredMountMs,
      // 从调用 mount 到「行已渲染」的端到端耗时
      tableReadyMs: round(now() - restoreStart),
      rows: (restoredSource.rows$.value as string[]).length,
      domRows: countRowsInDom(host),
      blocks: countBlocks(restored.store),
      duplicateIds: duplicateBlockIds(host),
    }

    // 4. 三视图：逐个切换，记录耗时与容器数量（行数必须始终为 rowCount）
    const views: PerfViewRun[] = []
    for (const mode of ['table', 'kanban', 'calendar']) {
      markStage(`views:${mode}`)
      const switchStart = now()
      await clickViewButton(host, mode)
      await waitUntil(() => countViewContainers(host, mode) > 0, `${mode} 视图容器`)
      views.push({
        mode,
        switchMs: round(now() - switchStart),
        rows: (restoredSource.rows$.value as string[]).length,
        containers: countViewContainers(host, mode),
      })
    }

    // 5. 编辑延迟：写一个单元格 → 值生效 → 两帧渲染完成（用户可感知的闭环）
    //
    // 单次采样在无头环境抖动极大（同一构建实测 60–400ms），因此：
    //   - 先做一次预热写入，消化切回表格后的整表重排（记 coldMs，只记录不判定）；
    //   - 再连写 5 次取中位数，作为「连续编辑的稳态延迟」。
    markStage('edit:clickTable')
    await clickViewButton(host, 'table')
    const dateColumnId = findColumnId(restored, 'date')
    const rowIds = restoredSource.rows$.value as string[]

    markStage('edit:warmup')
    const coldStart = now()
    restoredSource.cellValueChange(rowIds[1], dateColumnId, Date.now())
    await nextFrames(2)
    const coldMs = round(now() - coldStart)

    const samples: number[] = []
    for (let i = 0; i < 5; i += 1) {
      markStage(`edit:sample${i + 1}`)
      const rowId = rowIds[i]
      const start = now()
      restoredSource.cellValueChange(rowId, dateColumnId, Date.now() + i + 1)
      await waitUntil(() => Boolean(restoredSource.cellValueGet(rowId, dateColumnId)), '单元格写入生效')
      await nextFrames(2)
      samples.push(round(now() - start))
    }
    const sorted = [...samples].sort((a, b) => a - b)
    const edit = {
      latencyMs: sorted[Math.floor(sorted.length / 2)],
      coldMs,
      samples,
      applied: true,
    }

    // 6. 滚动：滚到列表末尾，记录耗时与最终 DOM 行数（当前未开虚拟滚动，行全在 DOM）
    markStage('scroll')
    const scrollTarget = findScrollable(host.querySelector('affine-data-view-renderer'))
    let scroll = { ms: 0, domRows: countRowsInDom(host), scrolled: false }
    if (scrollTarget) {
      const before = scrollTarget.scrollTop
      const scrollStart = now()
      scrollTarget.scrollTop = scrollTarget.scrollHeight
      await nextFrames(2)
      scroll = {
        ms: round(now() - scrollStart),
        domRows: countRowsInDom(host),
        scrolled: scrollTarget.scrollTop !== before,
      }
    }

    // 7. 增删行：上限内的一次行操作耗时
    markStage('rowOps:add')
    const addStart = now()
    restoredSource.rowAdd('end')
    await waitUntil(() => (restoredSource.rows$.value as string[]).length === rows + 1, '新增行生效')
    const addMs = round(now() - addStart)

    const deleteStart = now()
    markStage('rowOps:delete')
    const lastRowId: string = (restoredSource.rows$.value as string[]).at(-1) ?? ''
    restoredSource.rowDelete([lastRowId])
    await waitUntil(() => (restoredSource.rows$.value as string[]).length === rows, '删除行生效')
    const deleteMs = round(now() - deleteStart)

    return {
      rows,
      create,
      snapshot: snapshotRun,
      restore,
      views,
      edit,
      scroll,
      rowOps: { addMs, deleteMs, rows: (restoredSource.rows$.value as string[]).length },
    }
  } finally {
    handle?.destroy()
    restored?.destroy()
    host.remove()
  }
}

/** 按列类型找列 id（seed 只建了 title / date / select 三列） */
function findColumnId(handle: CanvasHandle | null, type: string): string {
  const database = (handle as any)?.store?.root?.children
    ?.find((child: any) => child.flavour === 'affine:note')
    ?.children?.find((child: any) => child.flavour === 'affine:database')
  // columns 是 signalled prop：优先读 `columns$`，退化成普通数组时再直接取
  const columns: Array<{ id: string; type: string }> =
    database?.props?.columns$?.value ?? database?.props?.columns ?? []
  const id = columns.find(column => column.type === type)?.id
  if (!id) throw new Error(`[spike-perf] 找不到 ${type} 列（现有 ${columns.length} 列）`)
  return id
}

/** 保留一位小数：毫秒噪声大，多余精度没有意义 */
function round(value: number): number {
  return Math.round(value * 10) / 10
}
