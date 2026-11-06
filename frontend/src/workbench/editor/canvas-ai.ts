/**
 * 画布 AI 的读写层（P2-6）：从 store 读元素快照、把通过的补丁一次性写回。
 *
 * 与 `ai/canvas-patch.ts` 的分工：那边是纯逻辑（解析/校验/去重），这边碰 store。
 * 写入必须**一个事务**且只做两件事：新建 Frame、平移元素——不删除、不改样式，
 * 因此失败只可能是「块不存在」或「只读」，不会出现半截破坏。
 */
import type { CanvasPlan, CanvasElementSnapshot, CanvasFrameSnapshot } from '../ai/canvas-patch'

/** 与服务端上限一致：超过就截断，让模型知道这不是全部。 */
export const CANVAS_SNAPSHOT_MAX_ELEMENTS = 300

/** 单个元素带给模型的文本上限，避免提示词随便签长度爆炸。 */
const ELEMENT_TEXT_MAX = 200

export interface CanvasSnapshot {
  surfaceId: string
  elements: CanvasElementSnapshot[]
  frames: CanvasFrameSnapshot[]
  truncated: boolean
}

/** 解析 `props.xywh`：上游写的是 `[x,y,w,h]`，历史数据里也可能没有方括号。 */
export function parseXywh(value: unknown): { x: number; y: number; w: number; h: number } | null {
  const text = typeof value === 'string' ? value : ''
  const numbers = text.match(/-?\d+(?:\.\d+)?/g)
  if (!numbers || numbers.length < 4) return null
  const [x, y, w, h] = numbers.slice(0, 4).map(Number)
  if (![x, y, w, h].every((item) => Number.isFinite(item))) return null
  if (w <= 0 || h <= 0) return null
  return { x, y, w, h }
}

/** 写回时统一带方括号：与上游 seed 和拖拽产生的格式一致。 */
export function formatXywh(x: number, y: number, w: number, h: number): string {
  return `[${x},${y},${w},${h}]`
}

function textOf(model: any): string {
  // 文本挂在不同地方：note/paragraph 在 model.text，frame 在 props.title，
  // shape 在 props.text。三者取到第一个非空的即可，不需要区分块类型。
  const text = model?.text ?? model?.props?.title ?? model?.props?.text
  if (!text) return ''
  const value = typeof text.toString === 'function' ? text.toString() : String(text)
  return value.trim().slice(0, ELEMENT_TEXT_MAX)
}

function kindOfFlavour(flavour: string): string {
  if (flavour === 'affine:frame') return 'frame'
  if (flavour === 'affine:note') return 'note'
  if (flavour.startsWith('affine:')) return flavour.slice('affine:'.length)
  return 'other'
}

/**
 * 读当前画布的只读快照。
 *
 * 三个来源拼起来：surface 的子块（元素与其包围盒）、frame 的 `childElementIds`
 * （元素归属）、note 子段落 / frame 标题（文本摘要）。
 * 找不到 surface（Page 形态或空画布）时返回 null，调用方据此禁用入口。
 */
export function readCanvasSnapshot(store: any): CanvasSnapshot | null {
  const root = store?.root
  if (!root) return null
  const surface = (root.children ?? []).find((child: any) => child.flavour === 'affine:surface')
  if (!surface) return null

  const children: any[] = Array.isArray(surface.children) ? surface.children : []
  const frames: CanvasFrameSnapshot[] = []
  const frameOf = new Map<string, string>()

  for (const child of children) {
    if (child.flavour !== 'affine:frame') continue
    const rect = parseXywh(child.props?.xywh)
    if (!rect) continue
    const model = store.getModelById?.(child.id)
    frames.push({ id: child.id, title: textOf(model), ...rect })
    const members = child.props?.childElementIds ?? {}
    for (const [elementId, included] of Object.entries(members)) {
      if (included) frameOf.set(elementId, child.id)
    }
  }

  const elements: CanvasElementSnapshot[] = []
  let truncated = false
  for (const child of children) {
    const rect = parseXywh(child.props?.xywh)
    if (!rect) continue
    if (elements.length >= CANVAS_SNAPSHOT_MAX_ELEMENTS) {
      truncated = true
      break
    }
    const kind = kindOfFlavour(String(child.flavour ?? ''))
    // frame 自己不算「可整理的元素」，它已经在 frames 里
    if (kind === 'frame') continue
    elements.push({
      id: child.id,
      kind,
      text: kind === 'note' ? noteText(store, child) : textOf(store.getModelById?.(child.id)),
      ...rect,
      frameId: frameOf.get(child.id) ?? '',
    })
  }

  return { surfaceId: surface.id, elements, frames, truncated }
}

/** note 的正文在子段落里；拼接成一行的前 200 字够模型判断主题。 */
function noteText(store: any, note: any): string {
  const parts: string[] = []
  for (const child of note.children ?? []) {
    const text = textOf(store.getModelById?.(child.id))
    if (text) parts.push(text)
    if (parts.join(' ').length >= ELEMENT_TEXT_MAX) break
  }
  return parts.join(' ').slice(0, ELEMENT_TEXT_MAX).trim()
}

export interface ApplyCanvasOutcome {
  frames: number
  moved: number
  /** 因块不存在 / 只读而跳过的 op 数 */
  failed: number
}

/**
 * 一次性写入补丁。
 *
 * 全部放在一个 `store.transact` 里：任一 op 失败就地记数、不影响其它 op，
 * 但不会出现「只写了一半就中断保存」——事务提交后由编辑器正常脏标记与保存队列落盘。
 */
export function applyCanvasPlan(store: any, plan: CanvasPlan, surfaceId: string): ApplyCanvasOutcome {
  const outcome: ApplyCanvasOutcome = { frames: 0, moved: 0, failed: 0 }
  if (!store || !surfaceId || plan.ops.length === 0) return outcome
  if (store.readonly || store.readOnly) {
    outcome.failed = plan.ops.length
    return outcome
  }

  store.transact(() => {
    for (const op of plan.ops) {
      if (op.kind === 'frame') {
        try {
          const frameId = store.addBlock(
            'affine:frame',
            { xywh: formatXywh(op.x, op.y, op.w, op.h) },
            surfaceId,
          )
          if (!frameId) {
            outcome.failed += 1
            continue
          }
          // 标题是 Y.Text：空文本用 replace(0, 0, title) 写入
          const title = store.getModelById?.(frameId)?.props?.title
          if (op.title && title && typeof title.replace === 'function') {
            title.replace(0, title.length ?? 0, op.title)
          }
          // 成员关系写在 frame 的 childElementIds 上（上游的归属机制）
          store.updateBlock(frameId, {
            childElementIds: Object.fromEntries(op.memberIds.map((id) => [id, true])),
          })
          outcome.frames += 1
        } catch {
          outcome.failed += 1
        }
        continue
      }

      try {
        // 只用 model 读 xywh：视图对象不带 props
        const model = store.getModelById?.(op.id)
        const rect = parseXywh(model?.props?.xywh)
        if (!rect) {
          outcome.failed += 1
          continue
        }
        // 只挪位置，宽高保持不变
        store.updateBlock(op.id, { xywh: formatXywh(op.x, op.y, rect.w, rect.h) })
        outcome.moved += 1
      } catch {
        outcome.failed += 1
      }
    }
  })

  return outcome
}
