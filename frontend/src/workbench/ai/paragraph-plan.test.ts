import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  SEGMENT_BATCH_MAX_CHARS,
  SEGMENT_BATCH_MAX_PARAGRAPHS,
  buildSegmentBatches,
  parseSegmentReply,
} from './paragraph-plan'

test('buildSegmentBatches：带编号打包，超预算的段落交给规则分段', () => {
  const paragraphs = ['甲'.repeat(60), '乙'.repeat(60), '丙'.repeat(SEGMENT_BATCH_MAX_CHARS)]
  const batches = buildSegmentBatches(paragraphs)

  assert.equal(batches.length, 1)
  assert.deepEqual(batches[0].indexes, [0, 1])
  assert.ok(batches[0].text.startsWith('[[P1]]\n'))
  assert.ok(batches[0].text.includes('[[P2]]\n'))
  assert.ok(!batches[0].text.includes('[[P3]]'))
})

test('buildSegmentBatches：超过单批段落数时拆批', () => {
  const paragraphs = Array.from({ length: SEGMENT_BATCH_MAX_PARAGRAPHS + 2 }, () => '短句。')
  const batches = buildSegmentBatches(paragraphs)
  assert.equal(batches.length, 2)
  assert.deepEqual(batches[0].indexes, [0, 1, 2, 3, 4, 5])
  assert.deepEqual(batches[1].indexes, [6, 7])
})

test('parseSegmentReply：按编号读取分段，忽略开场白与未知编号', () => {
  const reply = [
    '以下是分段结果：',
    '[[P1]]',
    '第一句。',
    '第二句。',
    '[[P2]]',
    '第三句。',
    '[[P9]]',
    '不应采用',
  ].join('\n')

  const parsed = parseSegmentReply(reply, { indexes: [0, 1], text: '' })
  assert.deepEqual(parsed.get(0), ['第一句。', '第二句。'])
  assert.deepEqual(parsed.get(1), ['第三句。'])
  assert.equal(parsed.size, 2)
})

test('parseSegmentReply：标记与正文同一行也能解析', () => {
  const parsed = parseSegmentReply('[[P1]] 第一句。 第二句。', { indexes: [0], text: '' })
  assert.deepEqual(parsed.get(0), ['第一句。 第二句。'])
})

test('parseSegmentReply：空正文不产出结果', () => {
  const parsed = parseSegmentReply('[[P1]]\n\n\n[[P2]]\n   ', { indexes: [0, 1], text: '' })
  assert.equal(parsed.size, 0)
})
