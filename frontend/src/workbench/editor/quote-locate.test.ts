import assert from 'node:assert/strict'
import { test } from 'node:test'

import { findQuoteBlock } from './quote-locate'

/**
 * 最小伪 store：只提供 findQuoteBlock 用到的 root / children / text / props。
 *
 * 这一层是纯逻辑（找块），真正的高亮要 DOM 与 CSS Highlight API，
 * 因此这里只覆盖「能否定位到正确的块」这个最容易出错的判断。
 */
function block(id: string, text: string, children: any[] = []) {
  return { id, text: { toString: () => text }, children, props: {} }
}

function storeOf(root: any) {
  return { root }
}

test('findQuoteBlock：按阅读顺序命中第一个块', () => {
  const root = block('root', '', [
    block('p1', '第一段：需求范围确认'),
    block('p2', '第二段：需求范围确认与排期'),
  ])
  const hit = findQuoteBlock(storeOf(root), '需求范围确认')
  // 两段都含这句话，必须取阅读顺序更靠前的 p1
  assert.equal(hit?.blockId, 'p1')
})

test('findQuoteBlock：递归进子块', () => {
  const root = block('root', '', [
    block('list', '', [block('item', '把接口对齐到二期')]),
  ])
  assert.equal(findQuoteBlock(storeOf(root), '接口对齐')?.blockId, 'item')
})

test('findQuoteBlock：忽略标点与空白差异', () => {
  const root = block('root', '', [block('p', '结论：优先做「智能关联」，其次做导出。')])
  // 关联结果里的原句可能丢了标点，匹配仍应命中
  assert.equal(findQuoteBlock(storeOf(root), '优先做智能关联')?.blockId, 'p')
})

test('findQuoteBlock：frame 标题也能被匹配（文字在 props 上）', () => {
  const root = block('root', '', [
    { id: 'frame', text: undefined, children: [], props: { title: { toString: () => '二期范围' } } },
  ])
  assert.equal(findQuoteBlock(storeOf(root), '二期范围')?.blockId, 'frame')
})

test('findQuoteBlock：找不到时返回 null', () => {
  const root = block('root', '', [block('p', '完全无关的内容')])
  assert.equal(findQuoteBlock(storeOf(root), '这句话不存在'), null)
})

test('findQuoteBlock：空引文与空 store 安全返回 null', () => {
  assert.equal(findQuoteBlock(storeOf(block('root', '')), '   '), null)
  assert.equal(findQuoteBlock({}, '任意'), null)
  assert.equal(findQuoteBlock(null, '任意'), null)
})
