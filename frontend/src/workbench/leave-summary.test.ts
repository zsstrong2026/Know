import assert from 'node:assert/strict'
import { test } from 'node:test'

import { buildLeaveSummary, type LeaveSummaryTexts } from './leave-summary.ts'

const texts: LeaveSummaryTexts = {
  counts: '将失去访问：{kb} 个工作知识库、{knowledge} 条知识、{items} 个画布/数据库、{docs} 个章节文档',
  retention: '数据保留 {days} 天；{days} 天内重新加入本 workspace 可恢复。',
  noTransfer: '不会转交给管理员或其他成员。',
  fallback: '退出后将失去对当前 workspace 私有工作数据的访问，30 天内重新加入可恢复。',
}

const preview = {
  kb_count: 1,
  knowledge_count: 12,
  item_count: 3,
  doc_count: 5,
  purge_after_days: 30,
  export_links: [],
}

test('有预览时拼出数量、保留期与不转交三行', () => {
  const summary = buildLeaveSummary(preview, texts)
  const lines = summary.split('\n')
  assert.equal(lines.length, 3)
  assert.match(lines[0], /1 个工作知识库、12 条知识、3 个画布\/数据库、5 个章节文档/)
  assert.match(lines[1], /数据保留 30 天/)
  assert.equal(lines[2], texts.noTransfer)
})

test('多个同名占位符都会被替换', () => {
  const summary = buildLeaveSummary(preview, texts)
  assert.ok(!summary.includes('{days}'), '占位符必须全部替换')
  assert.equal(summary.match(/30 天/g)?.length, 2)
})

test('无预览时回退到通用文案，不显示 0', () => {
  assert.equal(buildLeaveSummary(null, texts), texts.fallback)
})

test('零值数量正常渲染为 0', () => {
  const empty = { ...preview, knowledge_count: 0, item_count: 0, doc_count: 0 }
  const summary = buildLeaveSummary(empty, texts)
  assert.match(summary.split('\n')[0], /0 条知识、0 个画布\/数据库、0 个章节文档/)
})

test('未知占位符保持原样，不抛错', () => {
  const summary = buildLeaveSummary(preview, { ...texts, counts: '{unknown} 条' })
  assert.equal(summary.split('\n')[0], '{unknown} 条')
})
