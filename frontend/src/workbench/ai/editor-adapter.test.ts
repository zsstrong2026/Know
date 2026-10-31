import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createPageAIAdapter,
  currentTextSelection,
  isCodeBlockSelection,
} from './editor-adapter.ts'

/**
 * 造一个最小的 BlockSuite store 桩。
 *
 * 关键点：selection 必须通过 handle.getSelections() 提供（std 视图作用域），
 * store 上没有 selection —— 这正是之前两次实现踩过的坑，测试要守住它。
 */
function makeHandle(options: {
  selections?: unknown[]
  blocks?: Record<string, { text?: string; flavour?: string; parent?: string }>
}) {
  const blocks = options.blocks ?? {
    b1: { text: '0123456789abcdef' },
    code1: { text: 'const a = 1', flavour: 'affine:code' },
  }
  const store = {
    // 故意不提供 selection / selectionManager：谁再加回来这条测试就会提醒他。
    getModelById: (id: string) => {
      const block = blocks[id]
      if (!block) return null
      return { text: { toString: () => block.text ?? '' } }
    },
    getBlock: (id: string) => {
      const block = blocks[id]
      if (!block) return undefined
      return { flavour: block.flavour ?? 'affine:paragraph', parent: block.parent ?? 'root', id }
    },
    root: {
      children: [
        {
          flavour: 'affine:note',
          children: [
            { flavour: 'affine:paragraph', model: { text: { toString: () => '第一段' } } },
            { flavour: 'affine:list', model: { text: { toString: () => '列表项' } } },
          ],
        },
      ],
    },
  }
  return {
    store,
    getSelections: () => options.selections ?? [],
  }
}

/** 正向选区的标准形状。 */
const forwardSelection = {
  type: 'text',
  from: { blockId: 'b1', index: 2, length: 3 },
  to: { blockId: 'b1', index: 5, length: 4 },
  reverse: false,
}

test('从 getSelections 读取选区（不是 store.selection）', () => {
  const handle = makeHandle({ selections: [forwardSelection] })
  const adapter = createPageAIAdapter({ handle: () => handle, itemId: () => 'item-1' })
  const snapshot = adapter.getSelection()!

  assert.equal(snapshot.blockId, 'b1')
  assert.equal(snapshot.start, 2)
  assert.equal(snapshot.end, 9, 'end = to.index + to.length')
  assert.equal(snapshot.text, '2345678')
  assert.equal(snapshot.itemId, 'item-1')
})

test('反向选区把起止点对调', () => {
  const handle = makeHandle({
    selections: [
      {
        type: 'text',
        from: { blockId: 'b1', index: 9, length: 2 },
        to: { blockId: 'b1', index: 1, length: 3 },
        reverse: true,
      },
    ],
  })
  const snapshot = createPageAIAdapter({ handle: () => handle, itemId: () => 'i' }).getSelection()!
  assert.equal(snapshot.start, 1)
  assert.equal(snapshot.end, 11)
  assert.equal(snapshot.text, '123456789a')
})

test('无选区 / 非文本选区 / 折叠选区都返回 null', () => {
  const empty = createPageAIAdapter({ handle: () => makeHandle({}), itemId: () => 'i' })
  assert.equal(empty.getSelection(), null)

  const blockSel = createPageAIAdapter({
    handle: () =>
      makeHandle({ selections: [{ type: 'block', blockId: 'b1' }] }),
    itemId: () => 'i',
  })
  assert.equal(blockSel.getSelection(), null)

  const collapsed = createPageAIAdapter({
    handle: () =>
      makeHandle({
        selections: [
          { type: 'text', from: { blockId: 'b1', index: 4, length: 0 }, to: null },
        ],
      }),
    itemId: () => 'i',
  })
  assert.equal(collapsed.getSelection(), null, '零长度选区不弹工具条')
})

test('跨块选区不支持，返回 null', () => {
  const handle = makeHandle({
    selections: [
      {
        type: 'text',
        from: { blockId: 'b1', index: 1, length: 2 },
        to: { blockId: 'code1', index: 0, length: 3 },
        reverse: false,
      },
    ],
  })
  assert.equal(createPageAIAdapter({ handle: () => handle, itemId: () => 'i' }).getSelection(), null)
})

test('偏移越界时当作无选区，不返回半截文本', () => {
  // b1 的正文是 '0123456789abcdef'（16 字符）；index 14 + length 4 = 18 > 16。
  const handle = makeHandle({
    selections: [
      { type: 'text', from: { blockId: 'b1', index: 14, length: 4 }, to: null },
    ],
  })
  assert.equal(createPageAIAdapter({ handle: () => handle, itemId: () => 'i' }).getSelection(), null)
})

test('代码块判定：自身或祖先命中都算', () => {
  const handle = makeHandle({
    selections: [{ type: 'text', from: { blockId: 'code1', index: 0, length: 3 }, to: null }],
    blocks: {
      code1: { text: 'const a = 1', flavour: 'affine:code' },
      nested: { text: 'x', flavour: 'affine:paragraph', parent: 'code1' },
    },
  })
  const adapter = createPageAIAdapter({ handle: () => handle, itemId: () => 'i' })
  assert.equal(adapter.isCodeBlock?.(), true)

  assert.equal(isCodeBlockSelection(handle.store, 'b1'), false)
  assert.equal(isCodeBlockSelection(handle.store, 'nested'), true, '祖先命中也要算')
})

test('页面纯文本只取 note 下的段落与列表', () => {
  const adapter = createPageAIAdapter({ handle: () => makeHandle({}), itemId: () => 'i' })
  assert.equal(adapter.getPageText(), '第一段\n列表项')
})

test('currentTextSelection 不依赖 store 上的 selection 属性', () => {
  const handle = makeHandle({ selections: [forwardSelection] })
  assert.ok(currentTextSelection(handle))
  // store 上确实没有 selection：这条断言防止实现回退到错误来源。
  assert.equal('selection' in handle.store, false)
})
