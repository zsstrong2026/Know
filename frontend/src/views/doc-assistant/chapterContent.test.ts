import assert from 'node:assert/strict'
import test from 'node:test'
import { normalizeChapterTitleText, stripDuplicateChapterHeading } from './chapterContent'

test('strips duplicate ATX heading matching the chapter title', () => {
  const content = '## 概述\n\n本文介绍系统整体情况。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '本文介绍系统整体情况。')
})

test('strips heading regardless of heading level', () => {
  const content = '# 概述\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '正文内容。')
})

test('strips heading with manual arabic numbering', () => {
  const content = '## 1. 概述\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '正文内容。')
})

test('strips heading with manual chinese numbering', () => {
  const content = '## 一、概述\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '正文内容。')
})

test('strips heading when chapter title itself carries numbering', () => {
  const content = '## 概述\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '1. 概述'), '正文内容。')
})

test('strips closed ATX heading', () => {
  const content = '## 概述 ##\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '正文内容。')
})

test('keeps first heading when it differs from the chapter title', () => {
  const content = '### 1.1 背景\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), content)
})

test('keeps content when first non-empty line is plain text', () => {
  const content = '概述\n\n本文介绍系统整体情况。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), content)
})

test('skips leading blank lines before the duplicate heading', () => {
  const content = '\n\n## 概述\n\n正文内容。'
  assert.equal(stripDuplicateChapterHeading(content, '概述'), '正文内容。')
})

test('returns original content when empty or title is blank', () => {
  assert.equal(stripDuplicateChapterHeading('', '概述'), '')
  assert.equal(stripDuplicateChapterHeading('## 概述\n\n正文。', ''), '## 概述\n\n正文。')
})

test('normalizeChapterTitleText strips numbering and inline marks', () => {
  assert.equal(normalizeChapterTitleText('## 1. **概述**'), '概述')
  assert.equal(normalizeChapterTitleText('一、概述'), '概述')
  assert.equal(normalizeChapterTitleText(' 1、 概述 '), '概述')
  assert.equal(normalizeChapterTitleText('概述'), '概述')
})
