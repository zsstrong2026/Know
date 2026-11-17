import assert from 'node:assert/strict'
import { test } from 'node:test'

import { maskSensitiveTerms } from './insight-mask.ts'

const empty = { persons: [], organizations: [], projects: [] }

test('空词表原样返回', () => {
  const text = '张三在星河计划里做决定'
  assert.equal(maskSensitiveTerms(text, empty), text)
})

test('长词优先，不会先被短词截断', () => {
  const out = maskSensitiveTerms('张三丰与张三', {
    ...empty,
    persons: ['张三', '张三丰'],
  })
  assert.equal(out, '某人2与某人1')
})

test('同类多词按输入顺序编号，跨类型使用不同前缀', () => {
  const out = maskSensitiveTerms('张三、李四、星河计划、某某公司', {
    persons: ['张三', '李四'],
    organizations: ['某某公司'],
    projects: ['星河计划'],
  })
  assert.equal(out, '某人1、某人2、项目、组织')
})

test('单条词不编号，重复词只替换一次', () => {
  const out = maskSensitiveTerms('李雷和韩梅梅', {
    ...empty,
    persons: ['李雷', '李雷', '  '],
  })
  assert.equal(out, '某人和韩梅梅')
})
