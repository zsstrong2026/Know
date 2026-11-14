import assert from 'node:assert/strict'
import { test } from 'node:test'

import { applyIssuesToText, parseProofreadIssues } from './proofread.ts'

test('parseProofreadIssues：解析裸 JSON', () => {
  const issues = parseProofreadIssues('{"issues":[{"original":"在坐","suggestion":"再坐","reason":"错别字"}]}')
  assert.deepEqual(issues, [{ original: '在坐', suggestion: '再坐', reason: '错别字' }])
})

test('parseProofreadIssues：剥掉 Markdown 围栏与前后解释', () => {
  const raw = '好的，结果如下：\n```json\n{"issues":[{"original":"a","suggestion":"b","reason":"c"}]}\n```\n以上。'
  assert.deepEqual(parseProofreadIssues(raw), [{ original: 'a', suggestion: 'b', reason: 'c' }])
})

test('parseProofreadIssues：字符串里的花括号不会截断 JSON', () => {
  const raw = '{"issues":[{"original":"{x}","suggestion":"{y}","reason":"花括号"}]}'
  assert.deepEqual(parseProofreadIssues(raw), [{ original: '{x}', suggestion: '{y}', reason: '花括号' }])
})

test('parseProofreadIssues：空数组返回空列表', () => {
  assert.deepEqual(parseProofreadIssues('{"issues":[]}'), [])
})

test('parseProofreadIssues：非法输出返回空列表而不是抛错', () => {
  assert.deepEqual(parseProofreadIssues('没有 JSON'), [])
  assert.deepEqual(parseProofreadIssues('{"issues":[{"original":'), [])
  assert.deepEqual(parseProofreadIssues('{"issues":"oops"}'), [])
})

test('parseProofreadIssues：丢弃两侧都空的条目', () => {
  const raw = '{"issues":[{"original":"","suggestion":"","reason":"x"},{"original":"a","suggestion":"b"}]}'
  assert.deepEqual(parseProofreadIssues(raw), [{ original: 'a', suggestion: 'b', reason: '' }])
})

test('applyIssuesToText：按问题替换对应片段', () => {
  const next = applyIssuesToText('我们在坐讨论这个方安。', [
    { original: '在坐', suggestion: '再坐', reason: '' },
    { original: '方安', suggestion: '方案', reason: '' },
  ])
  assert.equal(next, '我们再坐讨论这个方案。')
})

test('applyIssuesToText：取消勾选的问题不改', () => {
  const next = applyIssuesToText(
    '我们在坐讨论这个方安。',
    [
      { original: '在坐', suggestion: '再坐', reason: '' },
      { original: '方安', suggestion: '方案', reason: '' },
    ],
    new Set([1]),
  )
  // 只应用第 0 条，第 1 条保持原文
  assert.equal(next, '我们再坐讨论这个方安。')
})

test('applyIssuesToText：找不到原文片段时跳过该条', () => {
  const next = applyIssuesToText('原文', [{ original: '不存在', suggestion: '替换', reason: '' }])
  assert.equal(next, '原文')
})

test('applyIssuesToText：替换后长度变化不影响后续定位', () => {
  const next = applyIssuesToText('ab', [
    { original: 'a', suggestion: 'AAAA', reason: '' },
    { original: 'b', suggestion: 'BBBB', reason: '' },
  ])
  assert.equal(next, 'AAAABBBB')
})

test('applyIssuesToText：原文为空或无问题时不改动', () => {
  assert.equal(applyIssuesToText('', [{ original: 'a', suggestion: 'b', reason: '' }]), '')
  assert.equal(applyIssuesToText('原文', []), '原文')
})
