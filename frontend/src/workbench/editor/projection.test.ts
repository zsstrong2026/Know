import { test } from 'node:test'
import assert from 'node:assert/strict'
import { projectMarkdown } from './projection'

/** 造一个最小 store：root > surface/note，块结构按 BlockSuite 的形状给。 */
function storeOf(children: any[]) {
  return { root: { flavour: 'affine:page', children } }
}

function para(text: string, type = 'text', id = `p-${text}`) {
  return { id, flavour: 'affine:paragraph', text: { toString: () => text }, props: { type } }
}

test('Page 正文：标题 / 引用 / 列表 / 代码 / 分割线', () => {
  const store = storeOf([
    {
      flavour: 'affine:note',
      children: [
        para('一级标题', 'h1'),
        para('引用一句', 'quote'),
        {
          id: 'l1',
          flavour: 'affine:list',
          text: { toString: () => '无序项' },
          props: { type: 'bulleted' },
          children: [
            { id: 'l2', flavour: 'affine:list', text: { toString: () => '子项' }, props: { type: 'numbered' } },
          ],
        },
        { id: 'l3', flavour: 'affine:list', text: { toString: () => '待办项' }, props: { type: 'todo' } },
        { id: 'c1', flavour: 'affine:code', text: { toString: () => 'const a = 1' }, props: { language: 'ts' } },
        { id: 'd1', flavour: 'affine:divider' },
      ],
    },
  ])

  const { markdown, text } = projectMarkdown(store)
  assert.match(markdown, /^# 一级标题$/m)
  assert.match(markdown, /^> 引用一句$/m)
  assert.match(markdown, /^- 无序项$/m)
  assert.match(markdown, /^ {2}1\. 子项$/m)
  assert.match(markdown, /^- \[ \] 待办项$/m)
  assert.match(markdown, /```ts\nconst a = 1\n```/)
  assert.match(markdown, /^---$/m)
  // 纯文本行不含 Markdown 标记。
  assert.ok(text.split('\n').includes('一级标题'))
  assert.ok(text.split('\n').includes('引用一句'))
})

test('数据库：输出 Markdown 表格，标题列取行文本，日期列转 ISO', () => {
  // 2026-10-30 本地 0 点，避免用 UTC 时间戳造成跨时区断言不稳。
  const day = new Date(2026, 9, 30).getTime()
  const store = storeOf([
    {
      flavour: 'affine:note',
      children: [
        {
          id: 'db1',
          flavour: 'affine:database',
          props: {
            columns$: {
              value: [
                { id: 'c-title', type: 'title', name: '事项', data: {} },
                { id: 'c-date', type: 'date', name: '截止日', data: {} },
                {
                  id: 'c-status',
                  type: 'select',
                  name: '状态',
                  data: { options: [{ id: 'opt-done', value: '已完成' }] },
                },
              ],
            },
            cells$: {
              value: {
                'row-1': {
                  'c-date': { value: day },
                  'c-status': { value: 'opt-done' },
                },
              },
            },
          },
          children: [{ id: 'row-1', flavour: 'affine:paragraph', text: { toString: () => '登录页改版' } }],
        },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  assert.match(markdown, /\| 事项 \| 截止日 \| 状态 \|/)
  assert.match(markdown, /\| 登录页改版 \| 2026-10-30 \| 已完成 \|/)
})

test('数据库没有 columns 时退化为行标题列表，不丢内容', () => {
  const store = storeOf([
    {
      flavour: 'affine:note',
      children: [
        {
          id: 'db2',
          flavour: 'affine:database',
          props: { columns$: { value: [] }, cells$: { value: {} } },
          children: [
            { id: 'r1', flavour: 'affine:paragraph', text: { toString: () => '甲' } },
            { id: 'r2', flavour: 'affine:paragraph', text: { toString: () => '乙' } },
          ],
        },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  assert.equal(markdown, '甲\n乙')
})

test('画布便签：正文追加在 Page 正文之后，并带便签小节标题', () => {
  const store = storeOf([
    { flavour: 'affine:note', children: [para('正文第一段')] },
    {
      flavour: 'affine:surface',
      children: [
        {
          id: 'n1',
          flavour: 'affine:note',
          children: [para('便签内容')],
        },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  const pageIndex = markdown.indexOf('正文第一段')
  const noteIndex = markdown.indexOf('便签内容')
  assert.ok(pageIndex >= 0 && noteIndex > pageIndex, '便签应排在 Page 正文之后')
  assert.match(markdown, /### 便签/)
})

test('画布便签为空时不留下空小节标题', () => {
  const store = storeOf([
    { flavour: 'affine:note', children: [para('只有正文')] },
    { flavour: 'affine:surface', children: [{ id: 'n2', flavour: 'affine:note', children: [] }] },
  ])

  const { markdown } = projectMarkdown(store)
  assert.equal(markdown.includes('### 便签'), false)
})

test('Frame：标题成为二级标题，子元素按 childElementIds 展开', () => {
  const store = storeOf([
    {
      flavour: 'affine:surface',
      children: [
        {
          id: 'f1',
          flavour: 'affine:frame',
          props: { title: { toString: () => '本周计划' }, childElementIds: { 't1': true } },
        },
        { id: 't1', flavour: 'affine:edgeless-text', children: [para('框内文字')] },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  assert.match(markdown, /^## 本周计划$/m)
  assert.match(markdown, /框内文字/)
})

test('独立文本块：按段落展开并递归子块', () => {
  const store = storeOf([
    {
      flavour: 'affine:surface',
      children: [
        { id: 'et1', flavour: 'affine:edgeless-text', children: [para('画布文本'), para('标题文本', 'h2')] },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  assert.match(markdown, /^画布文本$/m)
  assert.match(markdown, /^## 标题文本$/m)
})

test('空 store / 无 root 返回空投影', () => {
  assert.deepEqual(projectMarkdown(null), { markdown: '', text: '' })
  assert.deepEqual(projectMarkdown({}), { markdown: '', text: '' })
  assert.deepEqual(projectMarkdown(storeOf([])), { markdown: '', text: '' })
})

test('表格单元格里的竖线被转义，不破坏 Markdown 表格', () => {
  const store = storeOf([
    {
      flavour: 'affine:note',
      children: [
        {
          id: 'db3',
          flavour: 'affine:database',
          props: {
            columns$: { value: [{ id: 'c-title', type: 'title', name: '事项', data: {} }] },
            cells$: { value: {} },
          },
          children: [
            { id: 'r1', flavour: 'affine:paragraph', text: { toString: () => 'A|B' } },
          ],
        },
      ],
    },
  ])

  const { markdown } = projectMarkdown(store)
  assert.match(markdown, /A\\\|B/)
})
