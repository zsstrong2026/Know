import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  buildWritePlan,
  fingerprintOf,
  hashText,
  markdownToBlocks,
  verifyFingerprint,
} from './selection-fingerprint.ts'

const baseSnapshot = {
  blockId: 'b1',
  start: 2,
  end: 6,
  text: '选中文字',
  itemId: 'item-1',
}

test('指纹包含块/位置/文本哈希/条目', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.equal(fp.blockId, 'b1')
  assert.equal(fp.start, 2)
  assert.equal(fp.end, 6)
  assert.equal(fp.itemId, 'item-1')
  assert.equal(fp.textHash, hashText('选中文字'))
})

test('选区未变时允许应用', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.deepEqual(verifyFingerprint(fp, baseSnapshot), { ok: true })
})

test('条目切换 → 拒绝并标记 item-changed', () => {
  const fp = fingerprintOf(baseSnapshot)
  const verdict = verifyFingerprint(fp, { ...baseSnapshot, itemId: 'item-2' })
  assert.deepEqual(verdict, { ok: false, reason: 'item-changed' })
})

test('目标块被删除 → block-missing', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.deepEqual(verifyFingerprint(fp, null), { ok: false, reason: 'block-missing' })
  assert.deepEqual(
    verifyFingerprint(fp, { ...baseSnapshot, blockId: 'b9' }),
    { ok: false, reason: 'block-missing' },
  )
})

test('选区位置变化 → selection-moved', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.deepEqual(
    verifyFingerprint(fp, { ...baseSnapshot, start: 3, end: 7 }),
    { ok: false, reason: 'selection-moved' },
  )
})

test('位置没变但文字被改 → text-changed（不覆盖用户新输入）', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.deepEqual(
    verifyFingerprint(fp, { ...baseSnapshot, text: '我自己改的' }),
    { ok: false, reason: 'text-changed' },
  )
})

test('aside 不产生写入计划', () => {
  const fp = fingerprintOf(baseSnapshot)
  assert.equal(buildWritePlan(fp, '解释内容', 'aside'), null)
})

test('replace / below 生成带选区范围的计划', () => {
  const fp = fingerprintOf(baseSnapshot)
  const plan = buildWritePlan(fp, '新文本', 'replace')!
  assert.equal(plan.mode, 'replace')
  assert.equal(plan.blockId, 'b1')
  assert.equal(plan.start, 2)
  assert.equal(plan.end, 6)
})

const multiSnapshot = {
  blockId: 'b1',
  start: 1,
  end: 3,
  text: '一段\n二段',
  itemId: 'item-1',
  spans: [
    { blockId: 'b1', start: 1, end: 3, text: '一段' },
    { blockId: 'b2', start: 0, end: 2, text: '二段' },
  ],
}

test('跨块指纹记录每块的位置与哈希', () => {
  const fp = fingerprintOf(multiSnapshot)
  assert.equal(fp.spans?.length, 2)
  assert.equal(fp.spans?.[1].blockId, 'b2')
  assert.equal(fp.spans?.[1].textHash, hashText('二段'))
})

test('跨块选区：中间块被改 → text-changed', () => {
  const fp = fingerprintOf(multiSnapshot)
  const changed = {
    ...multiSnapshot,
    spans: [
      { blockId: 'b1', start: 1, end: 3, text: '一段' },
      { blockId: 'b2', start: 0, end: 2, text: '被改了' },
    ],
  }
  assert.deepEqual(verifyFingerprint(fp, changed), { ok: false, reason: 'text-changed' })
})

test('跨块选区：块数量变化 → selection-moved', () => {
  const fp = fingerprintOf(multiSnapshot)
  assert.deepEqual(
    verifyFingerprint(fp, { ...multiSnapshot, spans: [multiSnapshot.spans[0]] }),
    { ok: false, reason: 'selection-moved' },
  )
})

test('跨块写入计划：段数与块数一致时逐块对齐', () => {
  const fp = fingerprintOf(multiSnapshot)
  const plan = buildWritePlan(fp, '新一\n\n新二', 'replace')!
  assert.deepEqual(plan.segments, ['新一', '新二'])
  assert.equal(plan.spans?.length, 2)
})

test('跨块写入计划：段数少于块数时缺块清空', () => {
  const fp = fingerprintOf(multiSnapshot)
  const plan = buildWritePlan(fp, '只有一段', 'replace')!
  assert.deepEqual(plan.segments, ['只有一段', ''])
})

test('跨块写入计划：段数多于块数时多余段并入最后一块', () => {
  const fp = fingerprintOf(multiSnapshot)
  const plan = buildWritePlan(fp, '甲\n\n乙\n\n丙', 'replace')!
  assert.deepEqual(plan.segments, ['甲', '乙\n\n丙'])
})

test('单块选区不产出 spans', () => {
  const fp = fingerprintOf(baseSnapshot)
  const plan = buildWritePlan(fp, '新文本', 'replace')!
  assert.equal(plan.spans, undefined)
  assert.equal(plan.segments, undefined)
})

test('Markdown 转块：标题层级夹到 3 层', () => {
  const blocks = markdownToBlocks('##### 五级标题')
  assert.deepEqual(blocks, [{ kind: 'heading', level: 3, text: '五级标题' }])
})

test('Markdown 转块：无序与有序列表分别成组', () => {
  const unordered = markdownToBlocks('- 甲\n- 乙')
  assert.deepEqual(unordered, [{ kind: 'list', ordered: false, items: ['甲', '乙'] }])

  const ordered = markdownToBlocks('1. 甲\n2. 乙')
  assert.deepEqual(ordered, [{ kind: 'list', ordered: true, items: ['甲', '乙'] }])
})

test('Markdown 转块：代码围栏原样保留，内部不被当作标题或列表', () => {
  const blocks = markdownToBlocks('```ts\n# 注释\n- 不是列表\n```')
  assert.deepEqual(blocks, [{ kind: 'code', language: 'ts', text: '# 注释\n- 不是列表' }])
})

test('Markdown 转块：连续非空行合并成一段', () => {
  const blocks = markdownToBlocks('第一行\n第二行\n\n第二段')
  assert.deepEqual(blocks, [
    { kind: 'paragraph', text: '第一行 第二行' },
    { kind: 'paragraph', text: '第二段' },
  ])
})

test('Markdown 转块：未闭合围栏不死循环', () => {
  const blocks = markdownToBlocks('```\nabc')
  assert.equal(blocks.length, 1)
  assert.equal(blocks[0].kind, 'code')
})
