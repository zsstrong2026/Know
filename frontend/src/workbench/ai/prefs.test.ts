import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  DEFAULT_AI_PREFS,
  loadPrefs,
  parsePrefs,
  prefsKey,
  resolveDefaultAgent,
  savePrefs,
} from './prefs.ts'

/** 内存版 Storage：只实现用到的三个方法。 */
function memStorage(initial: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(initial))
  return {
    get length() {
      return map.size
    },
    clear: () => map.clear(),
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => [...map.keys()][index] ?? null,
    removeItem: (key: string) => void map.delete(key),
    setItem: (key: string, value: string) => void map.set(key, value),
  } as Storage
}

test('默认四项开启且默认智能体为快速问答', () => {
  assert.deepEqual(DEFAULT_AI_PREFS, {
    enabled: true,
    defaultAgentId: 'builtin-quick-answer',
    citations: true,
    confirmInsert: true,
  })
})

test('存储键带租户与用户；缺任一项返回空串', () => {
  assert.equal(prefsKey(7, 'u1'), 'know.workbench.ai-prefs.v1:7:u1')
  assert.equal(prefsKey(0, 'u1'), '')
  assert.equal(prefsKey(7, ''), '')
})

test('逐字段校验：一个字段坏了不影响其他三项', () => {
  const prefs = parsePrefs({ enabled: 'yes', defaultAgentId: 'a1', citations: false })
  assert.equal(prefs.enabled, true, '非布尔回默认')
  assert.equal(prefs.defaultAgentId, 'a1')
  assert.equal(prefs.citations, false)
  assert.equal(prefs.confirmInsert, true)
})

test('损坏的 JSON 回默认而不是抛错', () => {
  const storage = memStorage({ 'k': '{not json' })
  assert.deepEqual(loadPrefs(storage, 'k'), DEFAULT_AI_PREFS)
})

test('空键或空 storage 回默认', () => {
  assert.deepEqual(loadPrefs(memStorage(), ''), DEFAULT_AI_PREFS)
  assert.deepEqual(loadPrefs(null, 'k'), DEFAULT_AI_PREFS)
})

test('读写往返', () => {
  const storage = memStorage()
  const prefs = { ...DEFAULT_AI_PREFS, enabled: false, confirmInsert: false }
  savePrefs(storage, 'k', prefs)
  assert.deepEqual(loadPrefs(storage, 'k'), prefs)
})

test('默认智能体回退顺序：原选择 → 内置 → 我创建 → 空', () => {
  const agents = [
    { id: 'own-1' },
    { id: 'builtin-quick-answer', is_builtin: true },
  ]
  assert.equal(resolveDefaultAgent('own-1', agents), 'own-1', '仍可用时保留原选择')
  assert.equal(resolveDefaultAgent('gone', agents), 'builtin-quick-answer', '失效后回退内置')
  assert.equal(resolveDefaultAgent('gone', [{ id: 'own-2' }]), 'own-2', '没有内置时用我创建')
  assert.equal(resolveDefaultAgent('gone', []), '', '一个都没有时用租户默认模型')
})

test('未就绪的智能体不参与回退', () => {
  const agents = [
    { id: 'builtin-quick-answer', is_builtin: true, ready: false },
    { id: 'own-1', ready: true },
  ]
  assert.equal(resolveDefaultAgent('builtin-quick-answer', agents), 'own-1')
})
