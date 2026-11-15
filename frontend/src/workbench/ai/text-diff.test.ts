import assert from 'node:assert/strict'
import { test } from 'node:test'

import { diffSegments, splitResultBlocks, stripMarkdownForPreview, toPlainDocumentText } from './text-diff.ts'

test('diffSegments：识别中文里的插入与删除', () => {
  const segments = diffSegments('今天开会讨论方案', '今天开会讨论新方案')
  assert.deepEqual(segments, [
    { op: 'equal', text: '今天开会讨论' },
    { op: 'insert', text: '新' },
    { op: 'equal', text: '方案' },
  ])
})

test('diffSegments：完全相同时只有一段 equal', () => {
  assert.deepEqual(diffSegments('原文', '原文'), [{ op: 'equal', text: '原文' }])
})

test('diffSegments：两侧都为空时返回空数组', () => {
  assert.deepEqual(diffSegments('', ''), [])
})

test('diffSegments：纯新增时只有 insert', () => {
  assert.deepEqual(diffSegments('', '新增内容'), [{ op: 'insert', text: '新增内容' }])
})

test('diffSegments：超长文本退化为整段删除 + 整段新增', () => {
  const original = '甲'.repeat(4001)
  const next = '乙'.repeat(4001)
  assert.deepEqual(diffSegments(original, next), [
    { op: 'delete', text: original },
    { op: 'insert', text: next },
  ])
})

test('splitResultBlocks：按空行切段并丢弃空段', () => {
  assert.deepEqual(splitResultBlocks('第一段\n\n第二段\n\n\n第三段'), ['第一段', '第二段', '第三段'])
})

test('splitResultBlocks：代码围栏内的空行不切分', () => {
  const markdown = '说明\n\n```js\nconst a = 1\n\nconst b = 2\n```'
  assert.deepEqual(splitResultBlocks(markdown), ['说明', '```js\nconst a = 1\n\nconst b = 2\n```'])
})

test('toPlainDocumentText：去掉 Markdown 标记与分析段落，保留分行', () => {
  const answer = [
    '## 《济南的冬天》',
    '',
    '**一圈小山**把城抱成摇篮，',
    '冬天便在里面睡得安稳。',
    '',
    '说明：待处理文本中粘贴的是一段与本次任务无关的旧内容。',
  ].join('\n')
  assert.equal(
    toPlainDocumentText(answer),
    '《济南的冬天》\n\n一圈小山把城抱成摇篮，\n冬天便在里面睡得安稳。',
  )
})

test('toPlainDocumentText：列表 / 链接 / 代码围栏都转成纯文本', () => {
  const answer = '- 甲\n- [乙](https://example.com)\n\n```js\nconst a = 1\n```'
  assert.equal(toPlainDocumentText(answer), '甲\n乙\n\nconst a = 1')
})

test('toPlainDocumentText：说明紧接正文（没有空行）时也要截断', () => {
  assert.equal(
    toPlainDocumentText('秋天的风\n说明：这里解释取象与用词。'),
    '秋天的风',
  )
})

test('toPlainDocumentText：整段都是分析时退回纯文本，避免写不进去', () => {
  assert.equal(toPlainDocumentText('说明：这是一段解释。'), '说明：这是一段解释。')
})

test('stripMarkdownForPreview：预览不出现 Markdown 源码，保留行结构', () => {
  const preview = stripMarkdownForPreview('## 标题\n**加粗**与`代码`\n\n---\n- 列表项')
  assert.equal(preview, '标题\n加粗与代码\n\n列表项')
})
