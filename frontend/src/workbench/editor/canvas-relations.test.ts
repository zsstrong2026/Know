import assert from 'node:assert/strict'
import { test } from 'node:test'
import * as Y from 'yjs'

import {
  RELATION_CARD_HEIGHT,
  RELATION_CARD_WIDTH,
  applyRelationPlan,
  layoutRelationCards,
  readDocAnchor,
  readRelationsState,
  relationLabelRect,
  writeRelationsState,
} from './canvas-relations'
import type { RelationCardState, RelationView } from './canvas-relations'

/** 文档卡片（root 下的 note）的固定尺寸，与 blocksuite.ts 的 NOTE_CARD_* 一致。 */
const DOC_W = 498
const DOC_H = 420
const ANCHOR = { x: 0, y: 0, w: DOC_W, h: DOC_H }

function relationOf(itemId: string, label = '同一口径'): RelationView {
  return {
    itemId,
    title: `文档 ${itemId}`,
    reason: '讲同一件事',
    strength: 'strong',
    quote: '本文原句',
    targetQuote: '对方原句',
    label,
    source: 'mention',
  }
}

/**
 * 最小伪 store / crud：只实现写入链路用到的契约。
 *
 * root 下预置 surface 与「文档卡片」note —— 现在锚点就是这张 note，
 * 写入层不应该再为它创建任何块。
 */
function makeCanvas(options: { readonly?: boolean; failConnectorFor?: string; noDoc?: boolean } = {}) {
  const blocks = new Map<string, any>()
  const added: Array<{ flavour: string; parent: string; id: string }> = []
  const connectors: Array<{ id: string; props: Record<string, unknown> }> = []
  const removed: string[] = []
  let seq = 0

  const children: any[] = [{ id: 'surface', flavour: 'affine:surface', children: [] }]
  if (!options.noDoc) {
    children.push({
      id: 'doc-note',
      flavour: 'affine:note',
      props: { displayMode: 'doc', xywh: `[0,0,${DOC_W},${DOC_H}]` },
      children: [],
    })
  }

  const store = {
    readonly: options.readonly === true,
    root: { children },
    getModelById: (id: string) => blocks.get(id) ?? null,
    addBlock: (flavour: string, props: Record<string, unknown>, parent: string) => {
      seq += 1
      const id = `b${seq}`
      blocks.set(id, { id, flavour, props, children: [] })
      added.push({ flavour, parent, id })
      return id
    },
    deleteBlock: (id: string) => blocks.delete(id),
    transact: (fn: () => void) => fn(),
  }

  const crud = {
    addElement: (type: string, props: Record<string, unknown>) => {
      const targetId = (props.target as { id?: string } | undefined)?.id ?? ''
      if (options.failConnectorFor && targetId === options.failConnectorFor) return undefined
      seq += 1
      const id = `el${seq}`
      connectors.push({ id, props })
      return id
    },
    removeElement: (id: string) => {
      removed.push(id)
    },
  }

  return { store, crud, blocks, added, connectors, removed }
}

test('layoutRelationCards：单列相对文档卡片垂直居中', () => {
  const rects = layoutRelationCards(ANCHOR, 2)
  assert.equal(rects.length, 2)
  // 左边界在文档右侧，间距 120：卡片不会压在文档上
  assert.equal(rects[0].x, DOC_W + 120)
  assert.equal(rects[0].w, RELATION_CARD_WIDTH)
  assert.equal(rects[0].h, RELATION_CARD_HEIGHT)
  // 两张卡片整体高度 = 168*2 + 24 = 360，居中后首张 y = 210 - 180 = 30
  assert.equal(rects[0].y, DOC_H / 2 - (RELATION_CARD_HEIGHT * 2 + 24) / 2)
  assert.equal(rects[1].y - rects[0].y, RELATION_CARD_HEIGHT + 24)
})

test('layoutRelationCards：超过 4 张换列，且每列各自居中', () => {
  const rects = layoutRelationCards(ANCHOR, 6)
  assert.equal(rects.length, 6)
  // 前 4 张第一列，第 5、6 张第二列
  assert.equal(rects[4].x, DOC_W + 120 + RELATION_CARD_WIDTH + 120)
  assert.equal(rects[4].x, rects[5].x)
  // 第一列 4 张：168*4 + 24*3 = 744，居中后首张 y = 210 - 372 = -162
  assert.equal(rects[0].y, DOC_H / 2 - (RELATION_CARD_HEIGHT * 4 + 24 * 3) / 2)
  // 第二列只有 2 张，按自己的高度居中，不与第一列同起点
  assert.equal(rects[4].y, DOC_H / 2 - (RELATION_CARD_HEIGHT * 2 + 24) / 2)
  assert.notEqual(rects[4].y, rects[0].y)
})

test('layoutRelationCards：startX 用于在已有卡片之后追加', () => {
  const rects = layoutRelationCards(ANCHOR, 2, 900)
  assert.equal(rects[0].x, 900)
  // 换列时照样按新起点累加
  const many = layoutRelationCards(ANCHOR, 5, 900)
  assert.equal(many[4].x, 900 + RELATION_CARD_WIDTH + 120)
})

test('relationLabelRect：取两端中点，超长标签封顶宽度', () => {
  const card = { x: 900, y: 0, w: 200, h: 100 }
  const rect = relationLabelRect(ANCHOR, card, '短')
  assert.equal(rect.x + rect.w / 2, (DOC_W / 2 + card.x + card.w / 2) / 2)
  assert.equal(rect.y + rect.h / 2, (DOC_H / 2 + card.y + card.h / 2) / 2)

  const wide = relationLabelRect(ANCHOR, card, '一'.repeat(40))
  assert.equal(wide.w, 180)
})

test('readDocAnchor：取 root 下非 edgeless 的 note 作锚点', () => {
  const { store } = makeCanvas()
  const anchor = readDocAnchor(store)
  assert.equal(anchor?.id, 'doc-note')
  assert.deepEqual(anchor?.rect, ANCHOR)
})

test('readDocAnchor：没有文档卡片时返回 null', () => {
  const { store } = makeCanvas({ noDoc: true })
  assert.equal(readDocAnchor(store), null)
})

test('readRelationsState / writeRelationsState：往返一致，坏 JSON 返回 null', () => {
  const doc = new Y.Doc()
  assert.equal(readRelationsState(doc), null)

  const state = {
    version: 1 as const,
    anchorFrameId: 'doc-note',
    anchor: ANCHOR,
    cards: [],
    ignoredItemIds: ['x'],
    computedAt: 1,
  }
  writeRelationsState(doc, state)
  assert.deepEqual(readRelationsState(doc), state)

  // 写坏数据不该让画布打不开
  doc.transact(() => doc.getMap('wb-relations').set('state', '{oops'))
  assert.equal(readRelationsState(doc), null)
})

test('applyRelationPlan：只建卡片与连线，锚点是文档卡片本身', () => {
  const { store, crud, blocks, added, connectors } = makeCanvas()
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    relations: [relationOf('doc-1'), relationOf('doc-2', '同一接口')],
  })

  assert.equal(outcome.created, 2)
  assert.equal(outcome.failed, 0)
  assert.equal(outcome.skipped, 0)
  // 锚点复用文档 note，不新建占位框
  assert.equal(outcome.anchorId, 'doc-note')
  assert.deepEqual(outcome.anchor, ANCHOR)
  assert.equal(added.filter((item) => item.flavour === 'affine:frame').length, 2)
  // 卡片标题必须留空：非空会被 projection 拼进归档正文
  const frames = [...blocks.values()].filter((block) => block.flavour === 'affine:frame')
  assert.ok(frames.every((frame) => frame.props.title === undefined))

  assert.equal(connectors.length, 2)
  const first = connectors[0]
  // 线的起点是文档卡片，不是占位框 —— 这是「不再压住文档」的关键
  assert.equal((first.props.source as any).id, 'doc-note')
  assert.equal((first.props.target as any).id, outcome.cards[0].frameId)
  assert.equal(first.props.text, '同一口径')
  assert.equal(first.props.labelDisplay, true)
  assert.ok(typeof first.props.labelXYWH === 'string')
  assert.equal(first.props.rearEndpointStyle, 'None')
})

test('applyRelationPlan：已落盘的 itemId 跳过，新卡片排在已有卡片右侧', () => {
  const { store, crud, added, blocks } = makeCanvas()
  // 预置一张已存在的卡片 frame，用于验证追加起点
  blocks.set('old-frame', {
    id: 'old-frame',
    flavour: 'affine:frame',
    props: { xywh: `[700,0,${RELATION_CARD_WIDTH},${RELATION_CARD_HEIGHT}]` },
    children: [],
  })
  const existing: RelationCardState = {
    ...relationOf('doc-1'),
    frameId: 'old-frame',
    connectorId: 'old-connector',
    appliedAt: 1,
  }
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    relations: [relationOf('doc-1'), relationOf('doc-2')],
    existing: [existing],
  })

  assert.equal(outcome.skipped, 1)
  assert.equal(outcome.created, 1)
  assert.equal(added.length, 1)
  assert.equal(outcome.cards.length, 2)
  // 新卡片从旧卡片的右边界（700+280）再加 120 间距起排
  const created = blocks.get(added[0].id)
  assert.match(String(created?.props?.xywh), /^\[1100,/)
})

test('applyRelationPlan：迁移老状态时整批重建，旧卡片与占位框都清掉', () => {
  const { store, crud, removed, added, connectors } = makeCanvas()
  const existing: RelationCardState = {
    ...relationOf('doc-1'),
    frameId: 'old-frame',
    connectorId: 'old-connector',
    appliedAt: 1,
  }
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    // 老状态里的锚点是「占位 frame」，与文档卡片 id 不同
    prevAnchorId: 'legacy-anchor',
    relations: [relationOf('doc-1')],
    existing: [existing],
  })

  // 一条已存在的关联被重建（不是跳过），另加一个占位框清理
  assert.equal(outcome.created, 1)
  assert.equal(outcome.skipped, 0)
  assert.equal(outcome.anchorId, 'doc-note')
  assert.equal(added.length, 1)
  // 旧连线 → 旧卡片 frame → 占位框，顺序保证连线先撤销
  assert.deepEqual(removed, ['old-connector', 'legacy-anchor'])
  // 新连线从文档卡片出发
  assert.equal((connectors[0].props.source as any).id, 'doc-note')
  assert.equal(outcome.cards.length, 1)
  assert.notEqual(outcome.cards[0].frameId, 'old-frame')
})

test('applyRelationPlan：没有文档卡片时全部失败，不建任何块', () => {
  const { store, crud, added } = makeCanvas({ noDoc: true })
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    relations: [relationOf('doc-1')],
  })
  assert.equal(outcome.failed, 1)
  assert.equal(outcome.created, 0)
  assert.equal(outcome.anchorId, '')
  assert.equal(added.length, 0)
})

test('applyRelationPlan：只读时全部按失败返回，不写任何块', () => {
  const { store, crud, added } = makeCanvas({ readonly: true })
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    relations: [relationOf('doc-1')],
  })
  assert.equal(outcome.failed, 1)
  assert.equal(outcome.created, 0)
  assert.equal(added.length, 0)
})

test('applyRelationPlan：连线失败时撤掉孤立卡片', () => {
  // 现在只为卡片建 frame，第一张卡片就是 b1
  const { store, crud, blocks } = makeCanvas({ failConnectorFor: 'b1' })
  const outcome = applyRelationPlan({
    store,
    crud,
    surfaceId: 'surface',
    relations: [relationOf('doc-1')],
  })

  assert.equal(outcome.created, 0)
  assert.equal(outcome.failed, 1)
  // 卡片 frame 已被删掉，画布上不留空框
  assert.equal([...blocks.values()].filter((block) => block.flavour === 'affine:frame').length, 0)
})

test('applyRelationPlan：没有 crud（page 形态）时全部失败', () => {
  const { store } = makeCanvas()
  const outcome = applyRelationPlan({
    store,
    crud: null,
    surfaceId: 'surface',
    relations: [relationOf('doc-1')],
  })
  assert.equal(outcome.failed, 1)
  assert.equal(outcome.created, 0)
})
