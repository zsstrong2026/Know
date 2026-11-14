import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  CHUNK_MAX_CHARS,
  joinChunkResults,
  shouldChunk,
  splitLongText,
} from './long-doc.ts'

test('短文本不切分', () => {
  assert.deepEqual(splitLongText('一段短文'), ['一段短文'])
})

test('空文本返回空数组', () => {
  assert.deepEqual(splitLongText('   '), [])
})

test('按标题边界切分，标题归属其正文', () => {
  const sectionA = `# 甲\n${'甲'.repeat(30)}`
  const sectionB = `# 乙\n${'乙'.repeat(30)}`
  const chunks = splitLongText(`${sectionA}\n${sectionB}`, 40)
  assert.equal(chunks.length, 2)
  assert.ok(chunks[0].startsWith('# 甲'))
  assert.ok(chunks[1].startsWith('# 乙'))
})

test('多个小节在限额内合并成一块', () => {
  const text = `# 甲\n${'a'.repeat(10)}\n# 乙\n${'b'.repeat(10)}`
  const chunks = splitLongText(text, 100)
  assert.equal(chunks.length, 1)
  assert.ok(chunks[0].includes('# 甲') && chunks[0].includes('# 乙'))
})

test('单节超限时按段落二次切分', () => {
  const paragraph = '段'.repeat(30)
  const text = `# 标题\n${paragraph}\n\n${paragraph}\n\n${paragraph}`
  const chunks = splitLongText(text, 40)
  assert.ok(chunks.length > 1)
  for (const chunk of chunks) {
    assert.ok(chunk.length <= 40, `块超限：${chunk.length}`)
  }
})

test('单段仍超限时硬切，保证每块不超限', () => {
  const text = '甲'.repeat(100)
  const chunks = splitLongText(text, 30)
  assert.equal(chunks.length, 4)
  for (const chunk of chunks) assert.ok(chunk.length <= 30)
})

test('默认上限是 6000 字', () => {
  assert.equal(CHUNK_MAX_CHARS, 6000)
  const long = '文'.repeat(6001)
  assert.ok(splitLongText(long).length >= 2)
})

test('joinChunkResults 用空行拼接并丢弃空块', () => {
  assert.equal(joinChunkResults([' 甲 ', '', '乙\n']), '甲\n\n乙')
})

test('shouldChunk 只对支持的动作且超限时返回 true', () => {
  const long = 'x'.repeat(7000)
  assert.equal(shouldChunk('summarize', long), true)
  assert.equal(shouldChunk('outline', long), true)
  assert.equal(shouldChunk('polish', long), false, '润色不做分块，避免破坏段落衔接')
  assert.equal(shouldChunk('summarize', '短文本'), false)
})
