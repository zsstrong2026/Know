/**
 * 只擦笔划的橡皮（§10.6.1）。
 *
 * 上游 `EraserTool` 的命中集合是 `gfx.layer.canvasElements + gfx.layer.blocks`，
 * 会把 note、形状、Frame 一起删掉。产品要求「橡皮仅整条擦除画笔笔划，不擦
 * note/形状/Frame/数据库」，因此在**不 fork 上游**的前提下注册一个本地工具类：
 * 命中判定与删除链路复用上游 CRUD，只有目标类型被收窄。
 */
import { EdgelessCRUDIdentifier } from '@blocksuite/affine/blocks/surface'
import { BaseTool } from '@blocksuite/std/gfx'

/** 允许被擦掉的元素类型（画笔与荧光笔笔划）。 */
const ERASABLE_TYPES = new Set(['brush', 'highlighter'])

/**
 * 笔划橡皮工具。
 *
 * `toolName` 不能复用 'eraser'：上游已用该名字注册了自己的 EraserTool，
 * 同名注册谁生效取决于扩展顺序，不可控。取独立名字后由工具条显式映射。
 */
export class PenOnlyEraserTool extends BaseTool {
  static toolName = 'pen-eraser'

  /** 拖拽上一采样点（模型坐标） */
  private _prev: [number, number] | null = null

  /** 本次拖拽已命中、待删除的元素 */
  private readonly _targets = new Set<any>()

  dragStart(e: any): void {
    this.doc.captureSync()
    const [x, y] = this.gfx.viewport.toModelCoord(e.point.x, e.point.y)
    this._prev = [x, y]
    this._targets.clear()
  }

  dragMove(e: any): void {
    const prev = this._prev
    if (!prev) return
    const current = this.gfx.viewport.toModelCoord(e.point.x, e.point.y)
    // 只扫 surface 上的画布元素：blocks（note/Frame 等）根本不在候选集里。
    for (const element of this.gfx.layer.canvasElements) {
      if (this._targets.has(element)) continue
      if (element.isLocked?.()) continue
      if (!ERASABLE_TYPES.has(String(element.type))) continue
      if (element.getLineIntersections?.(prev, current)) {
        this._targets.add(element)
        element.opacity = 0.3
      }
    }
    this._prev = current
  }

  dragEnd(): void {
    const targets = [...this._targets]
    // 恢复被淡化但最终没被删（例如拖拽取消）的元素透明度。
    for (const element of targets) element.opacity = 1
    if (targets.length > 0) {
      this.std.get(EdgelessCRUDIdentifier).deleteElements(targets)
      this.doc.captureSync()
    }
    this._targets.clear()
    this._prev = null
  }

  deactivate(): void {
    for (const element of this._targets) element.opacity = 1
    this._targets.clear()
    this._prev = null
  }

  /** 点击（未拖拽）也应擦掉命中的整条笔划。 */
  click(e: any): void {
    const [x, y] = this.gfx.viewport.toModelCoord(e.point.x, e.point.y)
    const hit = this.gfx.getElementByPoint(x, y)
    if (!hit || !ERASABLE_TYPES.has(String(hit.type)) || hit.isLocked?.()) return
    this.doc.captureSync()
    this.std.get(EdgelessCRUDIdentifier).deleteElements([hit])
    this.doc.captureSync()
  }
}
