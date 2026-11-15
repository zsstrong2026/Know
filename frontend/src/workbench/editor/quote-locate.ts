/**
 * 原文定位与高亮（P4）。
 *
 * 场景：用户在「智能关联」里点开一篇关联文档，希望直接看到当初被引用的那一句。
 *
 * 三层定位，从粗到细：
 *   1. 在 block 树里找「文本里含这句原话」的块（容错交给 normalizeForMatch：
 *      全角半角、标点、空白差异都不影响匹配）；
 *   2. 在画布形态下把视口平移到那个块的中心，页面形态下滚动容器；
 *   3. 在该块对应的 DOM 上做区间高亮。
 *
 * 高亮用 CSS Custom Highlight API，不支持时退回 `<mark>` 包裹；
 * 两种情况都只改渲染层，绝不写 Y.Doc —— 定位是阅读辅助，不该改文档。
 */
// 用相对路径而不是 @/ 别名：本文件被 node 单测直接加载，
// tsx 只解析相对路径（其它测试文件里的 @/ 都只是 import type，会被擦除）。
import { findTextMatches } from '../../utils/sourceLocator'
import {
  buildTextIndex,
  findTextRange,
  highlightRanges,
  scrollRectIntoContainer,
} from '../../utils/sourceLocatorDom'

import type { RelationRect } from './canvas-relations'

/** 高亮保留时长：够看清位置，又不至于长期挂在正文上。 */
const LOCATE_HIGHLIGHT_MS = 6000
/** DOM 重试帧数：块组件异步渲染，首帧通常查不到刚打开文档里的目标块。 */
const LOCATE_MAX_ATTEMPTS = 30

export interface QuoteLocateContext {
  /** BlockSuite store：遍历块、读 xywh。 */
  store: any
  /** 编辑器宿主根元素：block 组件是 shadowless，文本就在这棵 DOM 里。 */
  root: HTMLElement
  /** 画布形态下把视口移到指定模型坐标中心；返回是否成功。 */
  centerOn?(center: { x: number; y: number }): boolean
}

export interface QuoteLocateOutcome {
  blockId: string
  /** 清理高亮的函数；没找到可高亮的 DOM 时为空操作。 */
  cleanup: () => void
}

/** 块的可见文本：正文在各块的 text 上，frame 标题与形状文字在 props 里。 */
function blockTextOf(block: any): string {
  const text = block?.text ?? block?.props?.title ?? block?.props?.text
  if (!text) return ''
  return typeof text.toString === 'function' ? text.toString() : String(text)
}

/**
 * 在块树里找第一个文本包含 `quote` 的块（深度优先，保持阅读顺序）。
 *
 * 只认「含这句原话」的块：关联结果里的 target_quote 是模型照抄的原文，
 * 因此这里不做模糊扩展，宁可找不到也不跳到错误的块。
 */
export function findQuoteBlock(store: any, quote: string): { blockId: string; text: string } | null {
  const needle = String(quote ?? '').trim()
  if (!needle || !store?.root) return null

  // 递归而不是用栈批量遍历：必须严格按「父块 → 各子块原序」走，
  // 否则同一句话在多处出现时会跳到阅读顺序更靠后的那一处。
  const walk = (block: any): { blockId: string; text: string } | null => {
    if (!block) return null
    const text = blockTextOf(block)
    if (text && findTextMatches(text, needle).length > 0) {
      return { blockId: String(block.id ?? ''), text }
    }
    const children: any[] = Array.isArray(block.children) ? block.children : []
    for (const child of children) {
      const hit = walk(child)
      if (hit) return hit
    }
    return null
  }
  return walk(store.root)
}

/** 读块的包围盒（模型坐标）；拿不到就不平移视口，只做高亮。 */
function blockRectOf(store: any, blockId: string): RelationRect | null {
  const model = store?.getModelById?.(blockId)
  const raw = model?.props?.xywh
  if (typeof raw !== 'string') return null
  const numbers = raw.match(/-?\d+(?:\.\d+)?/g)
  if (!numbers || numbers.length < 4) return null
  const [x, y, w, h] = numbers.slice(0, 4).map(Number)
  if (![x, y, w, h].every((value) => Number.isFinite(value)) || w <= 0 || h <= 0) return null
  return { x, y, w, h }
}

/**
 * 定位并高亮一句原文。
 *
 * 找不到块时返回 null，调用方据此提示「原文可能已修改」，
 * 而不是静默失败让用户以为文档没打开对。
 */
export function locateQuoteInEditor(
  ctx: QuoteLocateContext,
  quote: string,
  options: { highlightMs?: number } = {},
): QuoteLocateOutcome | null {
  const hit = findQuoteBlock(ctx.store, quote)
  if (!hit) return null

  // 1. 先把视口挪过去：画布形态按块的模型中心，页面形态交给浏览器滚动
  const rect = blockRectOf(ctx.store, hit.blockId)
  if (rect && ctx.centerOn) {
    ctx.centerOn({ x: rect.x + rect.w / 2, y: rect.y + rect.h / 2 })
  }

  // 2. 再高亮。块组件是异步渲染的：刚打开文档时目标块还没出现在 DOM 里，
  //    因此重试几帧；都不行就只做定位（视口已经挪过去了）并如实返回。
  let disposed = false
  let cleanup = () => {}
  let attempts = 0
  const paint = () => {
    if (disposed) return
    attempts += 1
    const element = ctx.root.querySelector<HTMLElement>(`[data-block-id="${hit.blockId}"]`)
    if (!element) {
      if (attempts < LOCATE_MAX_ATTEMPTS) requestAnimationFrame(paint)
      return
    }
    if (!rect) {
      // 页面形态没有 xywh：滚动容器让目标块落在视口上方三分之一处
      scrollRectIntoContainer(ctx.root, element.getBoundingClientRect())
    }
    const range = findTextRange(element, quote, buildTextIndex(element))
    if (range) cleanup = highlightRanges([range])
  }
  paint()

  const timer = setTimeout(() => cleanup(), options.highlightMs ?? LOCATE_HIGHLIGHT_MS)
  return {
    blockId: hit.blockId,
    cleanup: () => {
      disposed = true
      clearTimeout(timer)
      cleanup()
    },
  }
}
