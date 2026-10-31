import assert from 'node:assert/strict'
import { test } from 'node:test'

import { projectMarkdown } from './projection.ts'

/** 构造一个最小 store：root 下挂 note，note 里放指定块。 */
function storeWith(blocks: any[]) {
  return {
    root: {
      flavour: 'affine:page',
      children: [{ flavour: 'affine:note', children: blocks }],
    },
  }
}

const text = (value: string) => ({ toString: () => value })

test('段落按 type 转成对应级别的标题', () => {
  const result = projectMarkdown(
    storeWith([
      { flavour: 'affine:paragraph', props: { type: 'h1' }, text: text('总览') },
      { flavour: 'affine:paragraph', props: { type: 'h2' }, text: text('本周') },
      { flavour: 'affine:paragraph', props: { type: 'text' }, text: text('正文') },
    ]),
  )
  assert.equal(result.markdown, '# 总览\n\n## 本周\n\n正文')
  assert.equal(result.text, '总览\n本周\n正文')
})

test('列表输出为 Markdown 列表项，有序列表用 1.', () => {
  const result = projectMarkdown(
    storeWith([
      { flavour: 'affine:list', props: { type: 'bulleted' }, text: text('甲') },
      { flavour: 'affine:list', props: { type: 'numbered' }, text: text('乙') },
    ]),
  )
  assert.equal(result.markdown, '- 甲\n1. 乙')
})

test('代码块保留语言标记与围栏', () => {
  const result = projectMarkdown(
    storeWith([
      { flavour: 'affine:code', props: { language: 'typescript' }, text: text('const a = 1') },
    ]),
  )
  assert.equal(result.markdown, '```typescript\nconst a = 1\n```')
})

test('分隔线转成 ---，空段落被跳过', () => {
  const result = projectMarkdown(
    storeWith([
      { flavour: 'affine:paragraph', props: { type: 'text' }, text: text('   ') },
      { flavour: 'affine:divider' },
    ]),
  )
  assert.equal(result.markdown, '---')
})

test('surface 上的便签不进入正文（P2-2 才投影）', () => {
  const store = {
    root: {
      flavour: 'affine:page',
      children: [
        { flavour: 'affine:surface', children: [] },
        { flavour: 'affine:note', children: [{ flavour: 'affine:paragraph', text: text('只有这段') }] },
      ],
    },
  }
  assert.equal(projectMarkdown(store).markdown, '只有这段')
})

test('空 store / 无 note 时返回空串而不是抛错', () => {
  assert.equal(projectMarkdown(null).markdown, '')
  assert.equal(projectMarkdown({ root: { flavour: 'affine:page', children: [] } }).markdown, '')
})
