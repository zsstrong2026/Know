/**
 * 画布 AI 补丁的解析与落地计划（P2-6）。
 *
 * 服务端只保证「结构合法」，这里负责把它对齐到**真实画布**：
 *   - 丢掉引用了不存在的元素 id 的 op（模型会编 id）；
 *   - 同一个元素只归入第一个 Frame，避免一个便签被两个 Frame 同时 claim；
 *   - 没有成员的 Frame 直接丢掉（空框只会给用户添麻烦）；
 *   - 重复的 move 只取第一条。
 *
 * 全部是纯函数：写 store 的动作在 `editor/canvas-ai.ts` 里，这一层可以单测。
 */

export type RawPatch = unknown

export interface CanvasFramePlan {
  kind: 'frame'
  title: string
  x: number
  y: number
  w: number
  h: number
  memberIds: string[]
}

export interface CanvasMovePlan {
  kind: 'move'
  id: string
  x: number
  y: number
}

export type CanvasPlanOp = CanvasFramePlan | CanvasMovePlan

export interface CanvasPlan {
  ops: CanvasPlanOp[]
  /** 被丢掉的 op 数（未知 id / 空 Frame / 重复 move），用于预览里提示 */
  skipped: number
}

/** 画布元素的只读快照，前端从 store 里读，也是补丁校验的依据。 */
export interface CanvasElementSnapshot {
  id: string
  kind: string
  text: string
  x: number
  y: number
  w: number
  h: number
  frameId: string
}

export interface CanvasFrameSnapshot {
  id: string
  title: string
  x: number
  y: number
  w: number
  h: number
}

/** 服务端返回的 op 是宽进结构：字段都可能缺失或类型不对。 */
interface RawOp {
  op?: unknown
  title?: unknown
  x?: unknown
  y?: unknown
  w?: unknown
  h?: unknown
  member_ids?: unknown
  id?: unknown
}

function toFinite(value: unknown): number | null {
  const number = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(number) ? number : null
}

function toStringValue(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * 把模型输出解析成结构化 op（不做画布存在性校验）。
 *
 * 只认 `create_frame` / `move_element`；其它 op 一律忽略——这是「服务端不返回
 * 破坏性补丁」之外的第二道闸，防止将来服务端放宽后前端直接照单执行。
 */
export function parseCanvasOps(raw: RawPatch): { ops: CanvasPlanOp[]; skipped: number } {
  const container = Array.isArray(raw) ? raw : (raw as { ops?: unknown })?.ops
  const list = Array.isArray(container) ? (container as RawOp[]) : []
  const ops: CanvasPlanOp[] = []
  let skipped = 0
  for (const item of list) {
    const op = toStringValue(item?.op)
    if (op === 'create_frame') {
      const x = toFinite(item.x)
      const y = toFinite(item.y)
      const w = toFinite(item.w)
      const h = toFinite(item.h)
      if (x === null || y === null || w === null || h === null || w <= 0 || h <= 0) {
        skipped += 1
        continue
      }
      const memberIds = Array.isArray(item.member_ids)
        ? item.member_ids.map((id) => toStringValue(id)).filter(Boolean)
        : []
      ops.push({ kind: 'frame', title: toStringValue(item.title), x, y, w, h, memberIds })
      continue
    }
    if (op === 'move_element') {
      const id = toStringValue(item.id)
      const x = toFinite(item.x)
      const y = toFinite(item.y)
      if (!id || x === null || y === null) {
        skipped += 1
        continue
      }
      ops.push({ kind: 'move', id, x, y })
      continue
    }
    skipped += 1
  }
  return { ops, skipped }
}

/**
 * 把 op 对齐到真实画布：过滤未知元素、去重成员、丢弃空 Frame。
 *
 * `elements` 必须是当前 store 里的真实元素；用它的 id 做白名单，
 * 模型编出来的 id 在这里被静默丢掉并计入 skipped。
 */
export function resolveCanvasPlan(
  ops: Array<CanvasFramePlan | CanvasMovePlan>,
  elements: CanvasElementSnapshot[],
): CanvasPlan {
  const known = new Set(elements.map((element) => element.id))
  // 一个元素只能属于一个 Frame：按 op 顺序先到先得
  const claimed = new Set<string>()
  const moved = new Set<string>()
  const resolved: CanvasPlanOp[] = []
  let skipped = 0

  for (const op of ops) {
    if (op.kind === 'frame') {
      const members = op.memberIds.filter((id) => {
        if (!known.has(id) || claimed.has(id)) return false
        claimed.add(id)
        return true
      })
      if (members.length === 0) {
        // 没有任何成员的空 Frame 没有意义，直接丢掉
        skipped += 1
        continue
      }
      resolved.push({ ...op, memberIds: members })
      continue
    }
    if (!known.has(op.id) || moved.has(op.id)) {
      skipped += 1
      continue
    }
    moved.add(op.id)
    resolved.push(op)
  }
  return { ops: resolved, skipped }
}

/** 预览用的计数：不读 store，只统计计划本身。 */
export function summarizePlan(plan: CanvasPlan): { frames: number; moves: number; members: number } {
  let frames = 0
  let moves = 0
  let members = 0
  for (const op of plan.ops) {
    if (op.kind === 'frame') {
      frames += 1
      members += op.memberIds.length
    } else {
      moves += 1
    }
  }
  return { frames, moves, members }
}
