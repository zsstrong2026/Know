/**
 * 画布智能关联的写入层（P2）。
 *
 * 分工：召回与判读在服务端（`POST /ai/relations`），这边只负责把「用户勾选的结果」
 * 落到画布上，并维护一份可复现的关联状态。
 *
 * 落盘形态：
 *   - 锚点：**文档卡片本身**（root 下 displayMode 非 edgeless 的 note），不额外建块；
 *   - 引用卡片 frame：每篇关联文档一个，标题留空；
 *   - connector：文档边框 → 卡片，曲线 + 短标签。
 *
 * 为什么不再单独建锚点 frame：占位框会压在文档卡片上（两者坐标系重叠），
 * 连线也从占位框引出，看起来像「连到了文档内部」。直接用文档 note 作端点，
 * 线就从卡片边框出发，位置也永远跟着文档走。
 *
 * 为什么 frame 标题全部留空：归档正文由 `projection.ts` 按块序拼接，frame 标题非空
 * 就会被写进 Markdown。卡片文字由 P3 的 DOM 覆盖层渲染，因此块里不需要任何文字，
 * 归档结果不会因为「发现关联」而变脏。
 *
 * 为什么用 frame/note 而不是 shape：`GfxController.getElementById` 会回退到
 * `doc.getBlock`，`NoteBlockModel` 与 `FrameBlockModel` 都继承 `GfxCompatible`
 * （connectable），所以 block 也能当连线端点；且 connector-watcher 订阅了 block
 * 的 xywh 变化，拖动文档或卡片时连线会自动重算。
 */
import * as Y from 'yjs'

import { formatXywh, parseXywh } from './canvas-ai'

/**
 * 与上游 `ConnectorMode.Curve` 同值。
 *
 * 这里不 import `@blocksuite/affine/model`：本文件被单测直接加载，
 * 而测试运行时不解析上游包的入口；保持纯逻辑依赖能让单测不依赖构建产物。
 */
const CONNECTOR_MODE_CURVE = 2

/** 引用卡片尺寸：竖向卡片，够放标题 + 两句关联原文。 */
export const RELATION_CARD_WIDTH = 280
export const RELATION_CARD_HEIGHT = 168

/** 锚点（文档卡片）与第一列卡片、以及相邻两列之间的水平间距。 */
const RELATION_GAP_X = 120
/** 同列卡片的垂直间距。 */
const RELATION_GAP_Y = 24
/** 单列最多放几张；超过就换列（上限 6 张，因此最多两列）。 */
const RELATION_MAX_PER_COLUMN = 4
/** 连线标签的最大宽度与单字宽度估算（不引测量库，按 14px 字号够用）。 */
const LABEL_MAX_WIDTH = 180
const LABEL_CHAR_WIDTH = 14
const LABEL_HEIGHT = 24

/** Y.Doc 顶层的关联状态容器名。 */
export const RELATIONS_STATE_MAP = 'wb-relations'
/** 容器内保存整包 JSON 的字段名。 */
const RELATIONS_STATE_FIELD = 'state'

export interface RelationRect {
  x: number
  y: number
  w: number
  h: number
}

/** 一条待落盘的关联（服务端结果的视图子集）。 */
export interface RelationView {
  itemId: string
  title: string
  reason: string
  strength: 'strong' | 'weak'
  quote: string
  targetQuote: string
  label: string
  source: string
}

/** 已经落到画布上的一张卡片。 */
export interface RelationCardState extends RelationView {
  frameId: string
  connectorId: string
  appliedAt: number
}

/**
 * 整份关联状态：足以在没有服务端的情况下重建画布上的卡片与连线。
 *
 * `anchorFrameId` 现在指向**文档卡片本身**（root 下那个非 edgeless 的 note），
 * 因此连线直接从文档边框引出，不再需要一个压在文档上的占位 frame。
 * 老数据里它可能是占位 frame 的 id，读取时按「与当前文档卡片不同」识别并迁移。
 */
export interface RelationsState {
  version: 1
  anchorFrameId: string
  anchor: RelationRect
  cards: RelationCardState[]
  /** 用户明确忽略的条目：之后的召回应把它们排除。 */
  ignoredItemIds: string[]
  computedAt: number
}

export interface ApplyRelationInput {
  /** BlockSuite store（读写块）。 */
  store: any
  /** Edgeless CRUD（建 connector 只能走它，block 不在 surface element 表里）。 */
  crud: any
  surfaceId: string
  relations: RelationView[]
  /** 已落盘的卡片：按 itemId 去重，重复勾选不会叠加。 */
  existing?: RelationCardState[]
  /**
   * 上一次落盘状态里的锚点 id。
   *
   * 老数据里它指向「占位 frame」（压住文档的透明框），与文档卡片 id 不同；
   * 传入后会把连线重绑到文档卡片并删掉占位框。
   */
  prevAnchorId?: string
}

export interface ApplyRelationOutcome {
  /** 锚点 id：现在是文档卡片（note）本身的 id。 */
  anchorId: string
  /** 文档卡片的包围盒，随状态一起落盘，供下次布局参考。 */
  anchor: RelationRect
  cards: RelationCardState[]
  created: number
  /** 因为已存在而跳过的条数。 */
  skipped: number
  failed: number
}

/**
 * 读「当前文档卡片」：root 下 `displayMode` 不是 `edgeless` 的那个 note。
 *
 * 这就是用户在画布上看到的那张文档卡片，连线要直接从它的边框出发。
 * 找不到（Page 形态 / 空画布 / 结构异常）时返回 null，调用方据此放弃写入。
 */
export function readDocAnchor(store: any): { id: string; rect: RelationRect } | null {
  const children: any[] = store?.root?.children ?? []
  const note = children.find(
    (child) => child.flavour === 'affine:note' && child.props?.displayMode !== 'edgeless',
  )
  if (!note?.id) return null
  const rect = parseXywh(note.props?.xywh)
  if (!rect) return null
  return { id: String(note.id), rect }
}

/**
 * 按文档卡片右侧竖向排布卡片；超出单列上限换下一列。
 *
 * 整体相对文档卡片垂直居中，卡片永远排在它右边，既不会压住文档，
 * 也不受当前视口在哪影响。
 * `startX` 用于「已有卡片之后追加」：从既有卡片右边界起排，避免压在第一排上。
 */
export function layoutRelationCards(anchor: RelationRect, count: number, startX?: number): RelationRect[] {
  const rects: RelationRect[] = []
  const originX = startX ?? anchor.x + anchor.w + RELATION_GAP_X
  for (let index = 0; index < count; index += 1) {
    const column = Math.floor(index / RELATION_MAX_PER_COLUMN)
    const row = index % RELATION_MAX_PER_COLUMN
    const rowsInColumn = Math.min(RELATION_MAX_PER_COLUMN, count - column * RELATION_MAX_PER_COLUMN)
    const columnHeight = rowsInColumn * RELATION_CARD_HEIGHT + (rowsInColumn - 1) * RELATION_GAP_Y
    const startY = anchor.y + anchor.h / 2 - columnHeight / 2
    rects.push({
      x: originX + column * (RELATION_CARD_WIDTH + RELATION_GAP_X),
      y: startY + row * (RELATION_CARD_HEIGHT + RELATION_GAP_Y),
      w: RELATION_CARD_WIDTH,
      h: RELATION_CARD_HEIGHT,
    })
  }
  return rects
}

/**
 * 连线标签的摆放矩形：取两端中心连线的中点。
 *
 * 不读 connector 的实际路径——路径由上游在微任务里异步算出来，建线当下还没有；
 * 两端中心的中点对曲线模式已经足够接近。
 */
export function relationLabelRect(anchor: RelationRect, card: RelationRect, label: string): RelationRect {
  const width = Math.min(LABEL_MAX_WIDTH, 24 + Math.max(0, label.length) * LABEL_CHAR_WIDTH)
  const centerX = (anchor.x + anchor.w / 2 + card.x + card.w / 2) / 2
  const centerY = (anchor.y + anchor.h / 2 + card.y + card.h / 2) / 2
  return { x: centerX - width / 2, y: centerY - LABEL_HEIGHT / 2, w: width, h: LABEL_HEIGHT }
}

/** 读关联状态；没有或结构非法时返回 null，调用方按「从未计算」处理。 */
export function readRelationsState(spaceDoc: Y.Doc | undefined): RelationsState | null {
  const raw = spaceDoc?.getMap(RELATIONS_STATE_MAP)?.get(RELATIONS_STATE_FIELD)
  if (typeof raw !== 'string' || !raw) return null
  try {
    const parsed = JSON.parse(raw) as RelationsState
    if (!parsed || !Array.isArray(parsed.cards)) return null
    return { ...parsed, ignoredItemIds: parsed.ignoredItemIds ?? [] }
  } catch {
    // 历史版本或个人手改导致 JSON 坏了：当作没有状态，不要因此阻塞画布打开
    return null
  }
}

/**
 * 写关联状态。
 *
 * 在 `spaceDoc.transact` 里写：顶层 Y.Map 不参与 block 树，不会被 projection 读到；
 * 但会触发 Y.Doc 的 update 事件，从而进入既有的自动保存队列。
 */
export function writeRelationsState(spaceDoc: Y.Doc | undefined, state: RelationsState): void {
  const map = spaceDoc?.getMap(RELATIONS_STATE_MAP)
  if (!spaceDoc || !map) return
  spaceDoc.transact(() => {
    map.set(RELATIONS_STATE_FIELD, JSON.stringify(state))
  })
}

/**
 * 把勾选的关联落到画布：一个事务内建卡片与连线。
 *
 * 锚点直接用文档卡片本身，不建任何块：占位框会压在文档上、连线也从框里冒出来，
 * 看起来像连进了文档内部。
 *
 * 任一卡片失败只影响它自己（失败时连残留的空 frame 一起删掉），
 * 不会出现「连线建了但卡片没建」这类半截状态。
 */
export function applyRelationPlan(input: ApplyRelationInput): ApplyRelationOutcome {
  const { store, crud, surfaceId, relations } = input
  const outcome: ApplyRelationOutcome = {
    anchorId: '',
    anchor: { x: 0, y: 0, w: 0, h: 0 },
    cards: [...(input.existing ?? [])],
    created: 0,
    skipped: 0,
    failed: 0,
  }
  if (!store || !crud || !surfaceId) {
    outcome.failed = relations.length
    return outcome
  }
  if (store.readonly || store.readOnly) {
    outcome.failed = relations.length
    return outcome
  }

  // 端点必须是真实存在的文档卡片：找不到就整体放弃，
  // 否则连线会指向不存在的块（上游会画到原点或直接抛错）。
  const docAnchor = readDocAnchor(store)
  if (!docAnchor) {
    outcome.failed = relations.length
    return outcome
  }
  outcome.anchorId = docAnchor.id
  outcome.anchor = docAnchor.rect

  const known = new Set((input.existing ?? []).map((card) => card.itemId))
  const fresh = relations.filter((relation) => !known.has(relation.itemId))

  // 老状态里的锚点是「占位 frame」：那些卡片当时按占位框定位，很可能压在文档上。
  // 与其把每张卡片逐个挪位（还要同步改连线标签），不如整批重建——
  // 状态里本来就存了每条关联的全部字段，重建不丢信息，位置也一次排正。
  const staleAnchor = Boolean(input.prevAnchorId && input.prevAnchorId !== docAnchor.id)
  const staleCards = staleAnchor ? (input.existing ?? []) : []
  const pending: RelationView[] = staleAnchor
    ? [...staleCards.map(toRelationView), ...fresh]
    : fresh

  outcome.skipped = staleAnchor ? 0 : relations.length - fresh.length
  if (staleAnchor) outcome.cards = []

  // 只剩「删掉占位框」这一件事：老状态可能一张卡片都没记（或卡片已被用户删掉），
  // 但那个压住文档的空框还在画布上，必须清掉。
  if (pending.length === 0) {
    if (staleAnchor) {
      store.transact(() => {
        for (const card of staleCards) {
          crud.removeElement?.(card.connectorId)
          removeFrame(store, card.frameId)
        }
        crud.removeElement?.(input.prevAnchorId as string)
      })
    }
    return outcome
  }

  // 追加时从「已有卡片的最右边界」往后排，否则第二批会压在第一排上。
  // 用实际位置而不是槽位序号：用户可能把卡片拖到别处，按序号算会撞上。
  const startX = !staleAnchor && input.existing?.length
    ? nextColumnX(store, input.existing, docAnchor.rect)
    : undefined
  const rects = layoutRelationCards(docAnchor.rect, pending.length, startX)

  store.transact(() => {
    // 迁移：先清旧卡片与旧连线，再删占位框，最后按文档卡片的位置重建。
    // 顺序不能反——先删框会让旧连线短暂指向不存在的块。
    for (const card of staleCards) {
      try {
        crud.removeElement?.(card.connectorId)
      } catch {
        // 连线删不掉不影响重建：旧 frame 一起删时它成为孤儿，只是外观残留
      }
      removeFrame(store, card.frameId)
    }
    if (staleAnchor) crud.removeElement?.(input.prevAnchorId as string)

    pending.forEach((relation, index) => {
      const rect = rects[index]
      const frameId = addFrame(store, surfaceId, rect)
      if (!frameId) {
        outcome.failed += 1
        return
      }
      const connectorId = addConnector(
        crud,
        docAnchor.id,
        frameId,
        relation.label,
        docAnchor.rect,
        rect,
      )
      if (!connectorId) {
        // 连线失败就撤掉孤立卡片：没有连线的空框只会让画布变乱
        removeFrame(store, frameId)
        outcome.failed += 1
        return
      }
      outcome.cards.push({ ...relation, frameId, connectorId, appliedAt: Date.now() })
      outcome.created += 1
    })
  })

  return outcome
}

/** 关联卡片 frame 的底色。
 *
 * 上游 frame 的命中规则是「锁定 / 非透明背景 / 有标题」三者之一才可选中，
 * 默认透明且不带标题的空 frame 点不中，自然也就拖不动。
 * 这里给一个与画布同色的不透明底色，卡片既能拖，外观也不变。
 */
export const RELATION_FRAME_BACKGROUND = '--affine-background-primary-color'

/** Color 类型的透明判断（背景可能是字符串，也可能是 dark/light 或 normal 对象）。 */
function isTransparentColor(color: unknown): boolean {
  if (!color) return true
  if (typeof color === 'string') return color.toLowerCase().endsWith('transparent')
  if (typeof color === 'object') {
    const values = 'normal' in color
      ? [(color as { normal?: unknown }).normal]
      : [(color as { dark?: unknown }).dark, (color as { light?: unknown }).light]
    return values.every((value) => typeof value !== 'string' || value.toLowerCase().endsWith('transparent'))
  }
  return false
}

/**
 * 修复旧卡片的命中属性：早期写入的 frame 背景是 transparent，用户拖不动。
 *
 * 在读位置时顺手补齐——这是唯一每页都会走到的入口；
 * 补过一次后背景非透明，不会再重复写。
 */
export function ensureRelationFramesSelectable(store: any, cards: RelationCardState[]): void {
  if (store?.readonly || store?.readOnly) return
  for (const card of cards) {
    const model = store.getModelById?.(card.frameId)
    if (!model || !isTransparentColor(model.props?.background)) continue
    try {
      store.updateBlock?.(card.frameId, { background: RELATION_FRAME_BACKGROUND })
    } catch {
      // 补属性失败不影响覆盖层渲染，下次读取还会再试
    }
  }
}

/** 读已存在 frame 的包围盒；块不存在或属性损坏时返回 null（视为需要新建）。 */
function existingRect(store: any, frameId?: string): RelationRect | null {
  if (!frameId) return null
  const model = store.getModelById?.(frameId)
  if (!model) return null
  return parseXywh(model.props?.xywh)
}

/**
 * 追加新卡片时的起始横坐标：已有卡片右边界 + 间距。
 *
 * 没有任何可定位的卡片时退回默认位置（锚点右侧一列）。
 */
function nextColumnX(store: any, existing: RelationCardState[], anchor: RelationRect): number {
  let rightEdge = anchor.x + anchor.w
  for (const card of existing) {
    const rect = existingRect(store, card.frameId)
    if (rect) rightEdge = Math.max(rightEdge, rect.x + rect.w)
  }
  return rightEdge + RELATION_GAP_X
}

/**
 * 读画布上一张卡片当前的包围盒。
 *
 * 覆盖层每帧都按这里的位置渲染，因此用户拖动 frame 后卡片文字会跟着走——
 * 不能沿用 `layoutRelationCards` 的初始值，那只在写入那一刻成立。
 * frame 已被用户删掉时返回 null，调用方据此跳过这张卡片。
 */
export function readRelationRects(
  store: any,
  cards: RelationCardState[],
): Map<string, RelationRect> {
  const rects = new Map<string, RelationRect>()
  for (const card of cards) {
    const rect = existingRect(store, card.frameId)
    if (rect) rects.set(card.frameId, rect)
  }
  // 顺手把历史卡片的透明背景补成可命中，否则老文档里的卡片依旧拖不动
  ensureRelationFramesSelectable(store, cards)
  return rects
}

/** 把已落盘卡片还原成「待写入的关联」，用于迁移时整批重建。 */
function toRelationView(card: RelationCardState): RelationView {
  return {
    itemId: card.itemId,
    title: card.title,
    reason: card.reason,
    strength: card.strength,
    quote: card.quote,
    targetQuote: card.targetQuote,
    label: card.label,
    source: card.source,
  }
}

function addFrame(store: any, surfaceId: string, rect: RelationRect): string {
  try {
    return store.addBlock(
      'affine:frame',
      {
        xywh: formatXywh(rect.x, rect.y, rect.w, rect.h),
        // 不透明背景才会被画布当作可选中元素，否则卡片拖不动
        background: RELATION_FRAME_BACKGROUND,
      },
      surfaceId,
    ) ?? ''
  } catch {
    return ''
  }
}

function removeFrame(store: any, frameId: string): void {
  try {
    store.deleteBlock?.(frameId)
  } catch {
    // 删不掉也只是留一个空框，不该让整次写入报错
  }
}

/**
 * 建一条带标签的连线。
 *
 * `text` 直接传字符串即可：`ConnectorElementModel.propsToY` 会转成 Y.Text。
 * 但标签必须同时给 `labelXYWH`，否则 `hasLabel()` 为假，标签不会渲染。
 */
function addConnector(
  crud: any,
  anchorFrameId: string,
  frameId: string,
  label: string,
  anchor: RelationRect,
  card: RelationRect,
): string {
  const text = (label ?? '').trim().slice(0, 24)
  const labelRect = relationLabelRect(anchor, card, text)
  try {
    return (
      crud.addElement('connector', {
        mode: CONNECTOR_MODE_CURVE,
        source: { id: anchorFrameId },
        target: { id: frameId },
        strokeWidth: 2,
        // 关联不是指向关系，两端都不画箭头
        frontEndpointStyle: 'None',
        rearEndpointStyle: 'None',
        ...(text
          ? {
              text,
              labelDisplay: true,
              labelXYWH: formatXywh(labelRect.x, labelRect.y, labelRect.w, labelRect.h),
            }
          : {}),
      }) ?? ''
    )
  } catch {
    return ''
  }
}
