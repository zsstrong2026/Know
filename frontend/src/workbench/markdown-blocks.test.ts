import assert from 'node:assert/strict'
import { test } from 'node:test'

import { markdownToBlockSpecs } from './markdown-blocks.ts'

test('标题按层级转段落，超过 h4 的收敛到 h4', () => {
  const specs = markdownToBlockSpecs('# 一\n##### 五')
  assert.deepEqual(
    specs.map((spec) => spec.props?.type),
    ['h1', 'h4'],
  )
  assert.equal(specs[0].text, '一')
})

test('列表、引用与分割线映射到对应块', () => {
  const specs = markdownToBlockSpecs('- 要点\n1. 步骤\n> 引用\n---')
  assert.deepEqual(
    specs.map((spec) => [spec.flavour, spec.props?.type]),
    [
      ['affine:list', 'bulleted'],
      ['affine:list', 'numbered'],
      ['affine:paragraph', 'quote'],
      ['affine:divider', undefined],
    ],
  )
})

test('代码围栏内保留原样内容，且不会被当成标题', () => {
  const specs = markdownToBlockSpecs('```go\n# 不是标题\nfmt.Println(1)\n```')
  assert.equal(specs.length, 1)
  assert.equal(specs[0].flavour, 'affine:code')
  assert.equal(specs[0].props?.language, 'go')
  assert.equal(specs[0].text, '# 不是标题\nfmt.Println(1)')
})

test('行内加粗与反引号被清理，空行不产生块', () => {
  const specs = markdownToBlockSpecs('\n**结论**：`只看证据`\n\n')
  assert.equal(specs.length, 1)
  assert.equal(specs[0].text, '结论：只看证据')
})
