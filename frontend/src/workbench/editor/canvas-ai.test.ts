import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  applyCanvasPlan,
  formatXywh,
  parseXywh,
  readCanvasSnapshot,
} from './canvas-ai'
import type { CanvasPlan } from '../ai/canvas-patch'

/** 最小伪 store：只实现 canvas-ai 用到的几个方法，验的是读写契约不是 BlockSuite。 */
function makeStore() {
  const blocks = new Map<string, any>()
  const textOf = (value: string) => {
    const text = {
      length: value.length,
      toString: () => value,
      replace: (start: number, length: number, next: string) => {
        text.toString = () => value.slice(0, start) + next + value.slice(start + length)
        text.length = text.toString().length
      },
    }
    return text
  }
  const add = (id: string, flavour: string, props: Record<string, unknown>, children: any[] = []) => {
    const block = { id, flavour, props, children }
    blocks.set(id, block)
    return block
  }
  add('surface', 'affine:surface', {}, [
    add('frame-1', 'affine:frame', {
      xywh: '[0,0,400,300]',
      title: textOf('已有分组'),
      childElementIds: { n1: true },
    }),
    add('n1', 'affine:note', { xywh: '[10,10,200,100]' }, [
      add('p1', 'affine:paragraph', {}, []),
    ]),
    add('n2', 'affine:note', { xywh: '[300,10,200,100]' }, []),
    add('s1', 'affine:shape', { xywh: '[600,10,80,80]' }, []),
  ])
  // 段落文本挂在 model.text 上
  blocks.get('p1').text = textOf('会议纪要：确认排期')

  const updated: Array<[string, Record<string, unknown>]> = []
  const store = {
    root: { children: [blocks.get('surface')] },
    getModelById: (id: string) => blocks.get(id) ?? null,
    addBlock: (flavour: string, props: Record<string, unknown>, parent: string) => {
      assert.equal(parent, 'surface')
      const id = `new-${blocks.size}`
      add(id, flavour, props, [])
      return id
    },
    updateBlock: (id: string, props: Record<string, unknown>) => {
      const block = blocks.get(id)
      if (!block) return
      Object.assign(block.props, props)
      updated.push([id, props])
    },
    transact: (fn: () => void) => fn(),
  }
  return { store, blocks, updated }
}

test('parseXywh：兼容带方括号与裸数字，非法值返回 null', () => {
  assert.deepEqual(parseXywh('[10,20,300,200]'), { x: 10, y: 20, w: 300, h: 200 })
  assert.deepEqual(parseXywh('10,20,300,200'), { x: 10, y: 20, w: 300, h: 200 })
  assert.equal(parseXywh('[0,0,0,100]'), null)
  assert.equal(parseXywh('abc'), null)
  assert.equal(parseXywh(undefined), null)
  assert.equal(formatXywh(1, 2, 3, 4), '[1,2,3,4]')
})

test('readCanvasSnapshot：收集元素、Frame 与归属，frame 自身不算元素', () => {
  const { store } = makeStore()
  const snapshot = readCanvasSnapshot(store)
  assert.ok(snapshot)
  assert.equal(snapshot.surfaceId, 'surface')
  assert.deepEqual(
    snapshot.frames.map((frame) => [frame.id, frame.title, frame.w]),
    [['frame-1', '已有分组', 400]],
  )
  assert.deepEqual(
    snapshot.elements.map((element) => [element.id, element.kind, element.frameId]),
    [
      ['n1', 'note', 'frame-1'],
      ['n2', 'note', ''],
      ['s1', 'shape', ''],
    ],
  )
  // note 正文来自子段落
  assert.equal(snapshot.elements[0].text, '会议纪要：确认排期')
  assert.equal(snapshot.truncated, false)
})

test('readCanvasSnapshot：没有 surface 时返回 null（入口据此禁用）', () => {
  assert.equal(readCanvasSnapshot({ root: { children: [] } }), null)
  assert.equal(readCanvasSnapshot(null), null)
})

test('applyCanvasPlan：新建 Frame 并收成员，平移保持宽高', () => {
  const { store, blocks, updated } = makeStore()
  const plan: CanvasPlan = {
    skipped: 0,
    ops: [
      { kind: 'frame', title: '本周', x: 1000, y: 0, w: 420, h: 320, memberIds: ['n2'] },
      { kind: 'move', id: 'n2', x: 1020, y: 40 },
    ],
  }
  const outcome = applyCanvasPlan(store, plan, 'surface')
  assert.deepEqual(outcome, { frames: 1, moved: 1, failed: 0 })

  const created = [...blocks.values()].find((block) => block.flavour === 'affine:frame' && block.id !== 'frame-1')
  assert.ok(created)
  assert.equal(created.props.xywh, '[1000,0,420,320]')
  assert.deepEqual(created.props.childElementIds, { n2: true })
  // 平移只改坐标，宽高沿用原值
  const moved = updated.find(([id]) => id === 'n2')
  assert.equal(moved?.[1].xywh, '[1020,40,200,100]')
})

test('applyCanvasPlan：块不存在记 failed，不影响其它 op', () => {
  const { store } = makeStore()
  const plan: CanvasPlan = {
    skipped: 0,
    ops: [
      { kind: 'move', id: 'ghost', x: 0, y: 0 },
      { kind: 'move', id: 's1', x: 5, y: 6 },
    ],
  }
  const outcome = applyCanvasPlan(store, plan, 'surface')
  assert.deepEqual(outcome, { frames: 0, moved: 1, failed: 1 })
})

test('applyCanvasPlan：只读时不写任何东西', () => {
  const { store } = makeStore()
  const readonlyStore = { ...store, readonly: true }
  const plan: CanvasPlan = { skipped: 0, ops: [{ kind: 'move', id: 'n1', x: 0, y: 0 }] }
  assert.deepEqual(applyCanvasPlan(readonlyStore, plan, 'surface'), { frames: 0, moved: 0, failed: 1 })
})

test('applyCanvasPlan：空计划是空操作', () => {
  const { store } = makeStore()
  assert.deepEqual(applyCanvasPlan(store, { ops: [], skipped: 0 }, 'surface'), {
    frames: 0,
    moved: 0,
    failed: 0,
  })
})
