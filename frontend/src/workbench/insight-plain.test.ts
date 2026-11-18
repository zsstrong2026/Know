import assert from 'node:assert/strict'
import { test } from 'node:test'

import { plainAnswer } from './insight-plain.ts'

test('plainAnswer：去掉标题、加粗与列表符号', () => {
  assert.equal(
    plainAnswer('## 结论\n**方向**：更偏 I\n- 独处恢复\n- 少社交'),
    '结论\n方向：更偏 I\n独处恢复\n少社交',
  )
})

test('plainAnswer：保留有序列表序号，表格竖线与分隔行被清理', () => {
  assert.equal(
    plainAnswer('1. 第一点\n2. 第二点\n| 项 | 值 |\n| --- | --- |\n| 能量 | I |'),
    '1. 第一点\n2. 第二点\n项 值\n能量 I',
  )
})

test('plainAnswer：链接、行内代码与引用符号不残留', () => {
  assert.equal(
    plainAnswer('> 参见《文档》第 3 段，`quote` 与 [说明](https://example.com)'),
    '参见《文档》第 3 段，quote 与 说明',
  )
})

test('plainAnswer：代码围栏行被丢弃，围栏内文本原样保留', () => {
  assert.equal(plainAnswer('说明\n```\nkeep **this**\n```\n结束'), '说明\nkeep **this**\n结束')
})

test('plainAnswer：空输入返回空串', () => {
  assert.equal(plainAnswer(''), '')
})
