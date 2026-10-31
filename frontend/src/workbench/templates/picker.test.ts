import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  builtinCardsFor,
  fetchMineTemplates,
  filterCards,
  loadCardSnapshot,
  mineCards,
  type TemplateCardItem,
} from './picker.ts'

test('推荐标签只给 4 个推荐模板', () => {
  assert.deepEqual(
    builtinCardsFor('recommended').map((card) => card.key).sort(),
    ['meeting-notes', 'project-brief', 'reading-notes', 'weekly-plan'],
  )
})

test('分类标签只给该分类的模板', () => {
  assert.deepEqual(
    builtinCardsFor('work').map((card) => card.key).sort(),
    ['meeting-notes', 'okr', 'project-brief', 'weekly-plan', 'weekly-report'],
  )
  assert.deepEqual(builtinCardsFor('record').map((card) => card.key).sort(), [
    'interview-notes',
    'reading-notes',
  ])
  assert.deepEqual(builtinCardsFor('writing').map((card) => card.key), ['article-draft'])
})

test('我的模板标签不混入内置模板', () => {
  assert.deepEqual(builtinCardsFor('mine'), [])
})

test('搜索按名称与描述过滤', () => {
  const cards = builtinCardsFor('work')
  assert.equal(filterCards(cards, 'meeting').length, 1)
  assert.equal(filterCards(cards, 'MEETING')[0].key, 'meeting-notes', '大小写不敏感')
  assert.deepEqual(filterCards(cards, '不存在的模板'), [])
  assert.equal(filterCards(cards, '  ').length, cards.length, '空白等同于不过滤')
})

test('我的模板卡片用 mine: 前缀，避免与内置 id 撞车', () => {
  const cards = mineCards([
    {
      id: 'tpl-1',
      name: '我的周复盘',
      description: '每周五使用',
      size_bytes: 1024,
      editor_schema_version: 1,
      created_at: '2026-10-30T00:00:00Z',
      updated_at: '2026-10-30T00:00:00Z',
    },
  ])
  assert.equal(cards[0].key, 'mine:tpl-1')
  assert.equal(cards[0].name, '我的周复盘')
})

test('内置模板可取出并校验快照', async () => {
  // Node 环境没有 fetch 到 vite 资源的能力，这里用 stub 验证调用链与校验
  const card: TemplateCardItem = builtinCardsFor('work')[0]
  const originalFetch = globalThis.fetch
  globalThis.fetch = (async () =>
    ({
      ok: true,
      status: 200,
      arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer,
    }) as unknown as Response) as typeof fetch
  try {
    const snapshot = await loadCardSnapshot(card, async () => {
      throw new Error('内置模板不应调用我的模板接口')
    })
    assert.equal(snapshot.bytes.byteLength, 4)
    assert.equal(snapshot.schemaVersion, 1)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('我的模板快照走 content 接口并校验 schema', async () => {
  const card = mineCards([
    {
      id: 'tpl-1',
      name: '我的周复盘',
      description: '',
      size_bytes: 1024,
      editor_schema_version: 1,
      created_at: '',
      updated_at: '',
    },
  ])[0]
  let asked = ''
  const snapshot = await loadCardSnapshot(card, async (id) => {
    asked = id
    return { name: '我的周复盘', ydoc: btoa('\x01\x02\x03'), editor_schema_version: 1 }
  })
  assert.equal(asked, 'tpl-1')
  assert.equal(snapshot.name, '我的周复盘')
  assert.equal(snapshot.bytes.byteLength, 3)
})

test('我的模板 schema 过高时拒绝', async () => {
  const card = mineCards([
    {
      id: 'tpl-2',
      name: 'x',
      description: '',
      size_bytes: 1,
      editor_schema_version: 2,
      created_at: '',
      updated_at: '',
    },
  ])[0]
  await assert.rejects(
    () =>
      loadCardSnapshot(card, async () => ({
        name: 'x',
        ydoc: btoa('\x01\x02\x03'),
        editor_schema_version: 9,
      })),
  )
})

test('我的模板加载失败只影响该标签，不抛错', async () => {
  const failed = await fetchMineTemplates(async () => {
    throw new Error('boom')
  })
  assert.equal(failed.error, true)
  assert.deepEqual(failed.items, [])

  const ok = await fetchMineTemplates(async () => [])
  assert.equal(ok.error, false)
})
