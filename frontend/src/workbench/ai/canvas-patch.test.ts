import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  parseCanvasOps,
  resolveCanvasPlan,
  summarizePlan,
  type CanvasElementSnapshot,
  type CanvasFramePlan,
  type CanvasMovePlan,
} from './canvas-patch'

const elements: CanvasElementSnapshot[] = [
  { id: 'e1', kind: 'note', text: '会议纪要', x: 0, y: 0, w: 200, h: 100, frameId: '' },
  { id: 'e2', kind: 'note', text: '需求池', x: 300, y: 0, w: 200, h: 100, frameId: '' },
  { id: 'e3', kind: 'note', text: '排期', x: 600, y: 0, w: 200, h: 100, frameId: '' },
]

test('parseCanvasOps：只认两种 op，未知 op 计入 skipped', () => {
  const { ops, skipped } = parseCanvasOps({
    ops: [
      { op: 'create_frame', title: ' 本周 ', x: 0, y: 0, w: 420, h: 320, member_ids: ['e1'] },
      { op: 'delete_element', id: 'e1' },
      { op: 'move_element', id: 'e2', x: 10, y: 20 },
    ],
  })
  assert.equal(skipped, 1)
  assert.deepEqual(ops, [
    { kind: 'frame', title: '本周', x: 0, y: 0, w: 420, h: 320, memberIds: ['e1'] },
    { kind: 'move', id: 'e2', x: 10, y: 20 },
  ])
})

test('parseCanvasOps：字符串数字可被收敛，非法数值整条丢掉', () => {
  const { ops, skipped } = parseCanvasOps([
    { op: 'create_frame', x: '10', y: '20', w: '420', h: '320', member_ids: ['e1'] },
    { op: 'create_frame', x: 0, y: 0, w: 0, h: 320, member_ids: ['e2'] },
    { op: 'move_element', id: 'e1', x: 'abc', y: 1 },
  ])
  assert.equal(skipped, 2)
  assert.deepEqual(ops, [{ kind: 'frame', title: '', x: 10, y: 20, w: 420, h: 320, memberIds: ['e1'] }])
})

test('parseCanvasOps：容忍直接给数组与空输入', () => {
  assert.deepEqual(parseCanvasOps([]).ops, [])
  assert.deepEqual(parseCanvasOps(null).ops, [])
  assert.equal(parseCanvasOps({}).skipped, 0)
})

test('resolveCanvasPlan：未知元素被丢掉，空 Frame 不执行', () => {
  const frames: CanvasFramePlan[] = [
    { kind: 'frame', title: '存在', x: 0, y: 0, w: 420, h: 320, memberIds: ['e1', 'ghost'] },
    { kind: 'frame', title: '全是幽灵', x: 500, y: 0, w: 420, h: 320, memberIds: ['ghost-2'] },
  ]
  const plan = resolveCanvasPlan(frames, elements)
  assert.equal(plan.ops.length, 1)
  assert.deepEqual((plan.ops[0] as CanvasFramePlan).memberIds, ['e1'])
  assert.equal(plan.skipped, 1)
})

test('resolveCanvasPlan：同一元素不会被两个 Frame 同时收走', () => {
  const frames: CanvasFramePlan[] = [
    { kind: 'frame', title: 'A', x: 0, y: 0, w: 420, h: 320, memberIds: ['e1', 'e2'] },
    { kind: 'frame', title: 'B', x: 500, y: 0, w: 420, h: 320, memberIds: ['e2', 'e3'] },
  ]
  const plan = resolveCanvasPlan(frames, elements)
  assert.deepEqual(
    plan.ops.map((op) => (op.kind === 'frame' ? [op.title, op.memberIds] : [])),
    [
      ['A', ['e1', 'e2']],
      ['B', ['e3']],
    ],
  )
})

test('resolveCanvasPlan：重复 move 只保留第一条，未知 id 丢弃', () => {
  const moves: CanvasMovePlan[] = [
    { kind: 'move', id: 'e1', x: 10, y: 10 },
    { kind: 'move', id: 'e1', x: 99, y: 99 },
    { kind: 'move', id: 'ghost', x: 0, y: 0 },
  ]
  const plan = resolveCanvasPlan(moves, elements)
  assert.deepEqual(plan.ops, [{ kind: 'move', id: 'e1', x: 10, y: 10 }])
  assert.equal(plan.skipped, 2)
})

test('summarizePlan：按 Frame / move / 成员计数', () => {
  const plan = resolveCanvasPlan(
    [
      { kind: 'frame', title: 'A', x: 0, y: 0, w: 420, h: 320, memberIds: ['e1', 'e2'] },
      { kind: 'move', id: 'e3', x: 0, y: 0 },
    ],
    elements,
  )
  assert.deepEqual(summarizePlan(plan), { frames: 1, moves: 1, members: 2 })
})
