/**
 * 一键排版（参照 workbuddy-docx 的版式规范，面向中文长文阅读）。
 *
 * 排版动作（全部写在一个事务里，编辑器撤销即可整体回退）：
 *   - 标题层级不跳级（首个标题为 h1，其后最多比上一级深一层）；
 *   - 正文 / 标题 / 列表统一左对齐；
 *   - 正文段落按句子边界自动分段：过长的一段拆成多段，每段约 90 字，
 *     只在 `。！？!?…` 结束处断开，绝不切在句子中间；
 *   - 正文段落首行缩进两个全角空格（与 Word「首行缩进 2 字符」观感一致）；
 *   - 裁掉段落首尾多余的空白。
 *
 * 回退的设计：
 *   - 原块记下排版前的完整文本 delta（保留加粗等行内样式）与属性；
 *   - 分段新建的块记下 id 与写入的文本，回退时删除；
 *   - 如果排版后用户改过某段/某个新块（内容与排版结果不一致），回退不覆盖它，
 *     只去掉首行缩进，避免「回退」吞掉用户的新内容。
 */
import type * as Y from 'yjs'

type BsStore = any

/** Y.Doc 顶层的排版状态容器名（与 canvas-relations 的 wb-relations 同构）。 */
export const DOC_FORMAT_STATE_MAP = 'wb-doc-format'
/** 容器内保存整包 JSON 的字段名。 */
const DOC_FORMAT_STATE_FIELD = 'state'

/** 首行缩进：两个全角空格（U+3000），中文排版的两字符缩进。 */
export const PARAGRAPH_INDENT = '\u3000\u3000'

/** 超过这个字数的正文段落才考虑分段。 */
export const SPLIT_THRESHOLD = 120
/** 分段后每段的目标字数：攒够这个长度再遇到句号就断段。 */
const CHUNK_TARGET = 90
/** 尾段短于这个字数时并回上一段，避免出现一两句的孤立段落。 */
const TAIL_MIN = 40

/** 句末标点（含省略号）与其后的收尾引号，按此切句。 */
const SENTENCE_PATTERN = /[^。！？!?…]+[。！？!?…]+[”’」』）)\]]*|[^。！？!?…]+$/g

/** 只有正文块参与排版；数据库整棵子树跳过（行标题不是正文）。 */
const FORMATTABLE_FLAVOURS = new Set(['affine:paragraph', 'affine:list'])

/** 统一后的段落对齐。 */
const TARGET_TEXT_ALIGN = 'left'

/** Y.Text 的 delta：回退时原样写回；insert 可能是行内嵌入对象。 */
export type TextDelta = Array<{ insert: unknown; attributes?: Record<string, unknown> }>

export interface DocFormatBlockSnapshot {
  /** 改动前的段落类型（只有标题层级被规整时才记录） */
  type?: string
  /** 改动前的对齐；null 表示原本未设置 */
  textAlign: string | null
  /** 改动前的完整文本 delta：回退时写回，保留行内样式 */
  text?: TextDelta
  /** 排版后应有的文本：回退时据此判断用户是否又改过 */
  formatted?: string
}

/** 排版时新建的段落（分段产生），回退时按 text 是否被改动决定删不删。 */
export interface DocFormatCreatedBlock {
  id: string
  /** 由哪个原块分出来的：这段被改过时，原块的文字也不复原，避免内容重复 */
  sourceId: string
  text: string
}

export interface DocFormatState {
  version: 1
  /** true：文档处于排版后的状态，可以一键回退 */
  active: boolean
  appliedAt: number
  /** blockId → 排版前的快照 */
  blocks: Record<string, DocFormatBlockSnapshot>
  /** 分段新建的块 */
  created: DocFormatCreatedBlock[]
}

export interface DocFormatOutcome {
  /** 实际改写（或回退）的处数 */
  changed: number
  /** 参与排版的块数 */
  visited: number
  reason?: 'readonly' | 'no-state'
}

/** 排序后的正文块：顺序决定标题层级的规整结果。 */
export interface FormatTarget {
  id: string
  flavour: string
  /** paragraph 的 type（text/quote/h1..h6）或 list 的 type（bulleted/numbered/todo） */
  type: string
  textAlign: string | null
  text: string
}

export interface FormatEdit {
  id: string
  /** 计划时读到的原文：应用前要复核，内容已被改动就跳过文字操作 */
  source: string
  /** 新的段落类型（仅标题规整时出现） */
  type?: string
  /** 新的对齐（统一左对齐时出现） */
  textAlign?: string
  /** 需要从首部裁掉的空白字符数 */
  trimStart?: number
  /** 需要从尾部裁掉的字数（尾部空白 + 被分段切走的正文） */
  trimEnd?: number
  /** 是否在开头插入两字符首行缩进 */
  indent?: boolean
  /** 分段后的继任段落（已含缩进），按顺序插在原块之后 */
  segments?: string[]
}

export interface DocFormatPlan {
  edits: FormatEdit[]
  /** 与 edits 对应的"改动前"快照 */
  snapshots: Record<string, DocFormatBlockSnapshot>
}

/** 一键排版的选项。 */
export interface DocFormatOptions {
  /**
   * AI 给出的语义分段：blockId → 段落文本数组。
   *
   * 只在这些分段「拼起来与原文（忽略空白）完全一致」时采用；否则该段回落到
   * 规则分段，绝不让模型改写正文。
   */
  segments?: Record<string, string[]>
}

const HEADING_PATTERN = /^h([1-6])$/

/**
 * 按阅读顺序收集正文块。
 *
 * 递归 root 的 children 即可覆盖 Page 正文（note → paragraph/list）与画布
 * （surface → frame / edgeless-text / note）。遇到数据库不往下走：
 * 它的行是表格数据，改对齐或层级只会污染数据视图。
 */
export function collectFormatTargets(root: any): FormatTarget[] {
  const targets: FormatTarget[] = []

  const walk = (blocks: any[]) => {
    for (const block of blocks) {
      if (!block?.id) continue
      if (block.flavour === 'affine:database') continue
      if (FORMATTABLE_FLAVOURS.has(block.flavour)) {
        targets.push({
          id: block.id,
          flavour: block.flavour,
          type: String(block.props?.type ?? ''),
          // 上游段落的对齐默认是 undefined，这里统一折成 null 便于比较与序列化。
          textAlign: typeof block.props?.textAlign === 'string' ? block.props.textAlign : null,
          text: block.text?.toString?.() ?? '',
        })
      }
      if (block.children?.length) walk(block.children)
    }
  }

  walk(root?.children ?? [])
  return targets
}

/** 按句号类标点切句；没有句末标点时整段算一句。 */
function splitSentences(text: string): string[] {
  return text.match(SENTENCE_PATTERN) ?? []
}

/** 去掉所有空白（含全角空格），用于校验「模型有没有改写正文」。 */
function compactText(text: string): string {
  return text.replace(/\s+/g, '')
}

/**
 * 校验 AI 分段：拼起来必须与原文一致（忽略空白差异）。
 *
 * 模型偶尔会改写、合并或漏句，这里一旦对不上就返回 null，由调用方回落到规则分段。
 */
export function validatedSegments(chunks: string[] | undefined, clean: string): string[] | null {
  if (!chunks?.length) return null
  const trimmed = chunks.map((chunk) => chunk.trim()).filter(Boolean)
  if (trimmed.length === 0) return null
  if (trimmed.map(compactText).join('') !== compactText(clean)) return null
  return trimmed
}

/**
 * 需要 AI 语义分段的正文段落（与规则分段的触发条件一致）。
 *
 * 只挑「正文 text 段落 + 超过长度阈值」的块，标题/引用/列表不参与语义分段。
 */
export function collectAiSegmentTargets(root: any): Array<{ id: string; text: string }> {
  return collectFormatTargets(root)
    .filter(
      (target) =>
        target.flavour === 'affine:paragraph' &&
        target.type === 'text' &&
        target.text.trim().length > SPLIT_THRESHOLD,
    )
    .map((target) => ({ id: target.id, text: target.text.trim() }))
}

/**
 * 把过长的正文段落按句子边界分成年读性更好的几段。
 *
 * 不足阈值、或整段只有一句时不拆，避免为了分段而制造大量碎片段落。
 */
export function segmentParagraph(text: string): string[] {
  if (text.length <= SPLIT_THRESHOLD) return [text]
  const sentences = splitSentences(text)
  if (sentences.length < 2) return [text]

  const chunks: string[] = []
  let current = ''
  for (const sentence of sentences) {
    // 一段已经够长，下一句就从新段开始
    if (current.length >= CHUNK_TARGET) {
      chunks.push(current)
      current = ''
    }
    current += sentence
  }
  if (current) chunks.push(current)
  // 尾段太短就并回上一段，避免出现只有半句话的段落
  if (chunks.length > 1 && chunks[chunks.length - 1].length < TAIL_MIN) {
    const tail = chunks.pop() as string
    chunks[chunks.length - 1] += tail
  }
  return chunks.length > 1 ? chunks : [text]
}

/**
 * 生成排版编辑计划（纯函数，便于单测）。
 *
 * 空白段落直接跳过：没有可排的版，也不该为它记快照。
 * 正文分段优先采用 options.segments 中 AI 给出的结果，校验不通过再回落到规则分段。
 */
export function planDocFormat(
  targets: FormatTarget[],
  options: DocFormatOptions = {},
): DocFormatPlan {
  const edits: FormatEdit[] = []
  const snapshots: Record<string, DocFormatBlockSnapshot> = {}
  /** 上一个标题的实际层级；0 表示还没遇到标题。 */
  let lastLevel = 0

  for (const target of targets) {
    const raw = target.text
    if (!raw.trim()) continue

    const edit: FormatEdit = { id: target.id, source: raw }
    const snapshot: DocFormatBlockSnapshot = { textAlign: target.textAlign }
    let changed = false

    // 标题层级：首个标题落在 h1，其后最多比上一级深一层（1→2→3 连续）。
    const heading = target.flavour === 'affine:paragraph' ? HEADING_PATTERN.exec(target.type) : null
    if (heading) {
      const level = Number(heading[1])
      const normalized = Math.min(level, lastLevel + 1)
      lastLevel = normalized
      if (normalized !== level) {
        edit.type = `h${normalized}`
        snapshot.type = target.type
        changed = true
      }
    }

    if (target.textAlign !== TARGET_TEXT_ALIGN) {
      edit.textAlign = TARGET_TEXT_ALIGN
      changed = true
    }

    // 正文（text 段落）参与分段与首行缩进；标题、引用、列表只做裁剪与对齐。
    const isBody = target.flavour === 'affine:paragraph' && target.type === 'text'
    const clean = raw.trim()
    const aiChunks = isBody ? validatedSegments(options.segments?.[target.id], clean) : null
    const chunks = isBody ? (aiChunks ?? segmentParagraph(clean)) : [clean]
    const first = chunks[0]
    // 收尾空白已在 trim 时去掉，这里补上首行缩进，得到该段排版后的文本
    const formatted = (isBody ? PARAGRAPH_INDENT : '') + first

    if (formatted !== raw) {
      // 原串 = 首部空白 + 首段正文 + 其余（尾部空白 + 被切走的句子）
      const trimStart = raw.length - raw.trimStart().length
      edit.trimStart = trimStart
      edit.trimEnd = raw.length - trimStart - first.length
      edit.indent = isBody
      snapshot.formatted = formatted
      changed = true
    }

    if (chunks.length > 1) {
      edit.segments = chunks.slice(1).map((chunk) => PARAGRAPH_INDENT + chunk)
      changed = true
    }

    if (!changed) continue
    snapshot.formatted = formatted
    snapshots[target.id] = snapshot
    edits.push(edit)
  }

  return { edits, snapshots }
}

/** 读排版状态；没有或结构非法时返回 null，调用方按「未排版」处理。 */
export function readDocFormatState(spaceDoc: Y.Doc | undefined): DocFormatState | null {
  const raw = spaceDoc?.getMap(DOC_FORMAT_STATE_MAP)?.get(DOC_FORMAT_STATE_FIELD)
  if (typeof raw !== 'string' || !raw) return null
  try {
    const parsed = JSON.parse(raw) as DocFormatState
    if (!parsed || parsed.version !== 1 || typeof parsed.active !== 'boolean') return null
    const blocks =
      parsed.blocks && typeof parsed.blocks === 'object' && !Array.isArray(parsed.blocks)
        ? parsed.blocks
        : {}
    const created = Array.isArray(parsed.created)
      ? parsed.created.filter((item) => typeof item?.id === 'string')
      : []
    return {
      version: 1,
      active: parsed.active,
      appliedAt: Number(parsed.appliedAt) || 0,
      blocks,
      created,
    }
  } catch {
    // JSON 坏了只当没排过版，不能让编辑页打不开。
    return null
  }
}

/** 写排版状态（与关联状态一致：顶层 Y.Map 不参与 projection，但会触发自动保存）。 */
export function writeDocFormatState(spaceDoc: Y.Doc | undefined, state: DocFormatState): void {
  const map = spaceDoc?.getMap(DOC_FORMAT_STATE_MAP)
  if (!spaceDoc || !map) return
  spaceDoc.transact(() => {
    map.set(DOC_FORMAT_STATE_FIELD, JSON.stringify(state))
  })
}

/** delta → 纯文本，用于判断回退时文本是否仍是排版结果。 */
function deltaText(delta: TextDelta): string {
  return delta.map((op) => (typeof op.insert === 'string' ? op.insert : '')).join('')
}

/**
 * 取新建块的 Y.Text。
 *
 * `store.addBlock` 之后模型要等事务结束才创建（Yjs 观察者是事务末回调），
 * 在同一个事务里 `getModelById(newId)` 拿到的是 null。因此分段文字必须直接
 * 写进块自身的 Y.Text —— 它在 addBlock 时就已经由 schema 默认值建好了。
 */
function createdBlockYText(spaceDoc: Y.Doc | undefined, blockId: string): any {
  const blocks = (spaceDoc as any)?.getMap?.('blocks')
  const yBlock = blocks?.get?.(blockId)
  return yBlock?.get?.('prop:text') ?? null
}

/**
 * 读块在父块 Y.Array 里的实时下标。
 *
 * 模型上的 `children` 要等事务结束才更新（与上面同因），同一事务里前面的段落
 * 已经插入了新块时，用模型下标会把后面的段落插到错误位置——顺序就乱了。
 * Y.Array 是即时更新的，所以插入位置一律以它为准。
 */
function liveChildIndex(spaceDoc: Y.Doc | undefined, parentId: string, blockId: string): number {
  const blocks = (spaceDoc as any)?.getMap?.('blocks')
  const children = blocks?.get?.(parentId)?.get?.('sys:children')
  const ids = children?.toArray?.()
  return Array.isArray(ids) ? ids.indexOf(blockId) : -1
}

/**
 * 一键排版。
 *
 * 重复点击是安全的：已有快照里保存的是「最早一次排版前」的原值，再次排版只补充
 * 新出现的块，回退仍然能回到最初的样子。
 *
 * 防丢内容的顺序：先建分段块并写入文字，确认每个块都拿到了完整文字，才截断原块。
 * 任何一步失败都回滚这次分段，原块保持原样。
 */
export function applyDocFormat(
  store: BsStore,
  spaceDoc: Y.Doc | undefined,
  options: DocFormatOptions = {},
): DocFormatOutcome {
  if (!store?.root) return { changed: 0, visited: 0, reason: 'readonly' }
  if (store.readonly) return { changed: 0, visited: 0, reason: 'readonly' }

  const targets = collectFormatTargets(store.root)
  const plan = planDocFormat(targets, options)
  const created: DocFormatCreatedBlock[] = []

  if (plan.edits.length > 0) {
    // 一个事务包住所有改动：编辑器里按一次撤销就能整体回退。
    store.transact(() => {
      for (const edit of plan.edits) {
        const model = store.getModelById(edit.id)
        if (!model) continue

        const patch: Record<string, unknown> = {}
        if (edit.type) patch.type = edit.type
        if (edit.textAlign) patch.textAlign = edit.textAlign
        if (Object.keys(patch).length > 0) store.updateBlock(edit.id, patch)

        const touchesText = Boolean(
          edit.trimStart || edit.trimEnd || edit.indent || edit.segments?.length,
        )
        if (!touchesText) continue

        const text = model.text
        // 计划之后内容可能已被改动（协同编辑、重复命中）：与计划时的原文不一致就跳过文字操作
        if (!text || (text.toString?.() ?? '') !== edit.source) continue

        const length = text.length
        // 原文 delta 保留行内样式，回退时据此还原
        const delta = text.toDelta?.()

        // 1) 先建分段并写入文字，确认全部成功
        const localCreated: DocFormatCreatedBlock[] = []
        let segmentsOk = true
        if (edit.segments?.length) {
          const parent = model.parent
          const parentId = parent?.id
          // 优先用 Y.Array 的实时下标：模型 children 在本事务结束前看不到已插入的分段
          const liveIndex = parentId ? liveChildIndex(spaceDoc, parentId, edit.id) : -1
          const index =
            liveIndex >= 0
              ? liveIndex
              : parent
                ? parent.children.findIndex((child: any) => child.id === edit.id)
                : -1
          if (!parentId || index < 0) {
            segmentsOk = false
          } else {
            try {
              edit.segments.forEach((segment, offset) => {
                const id = store.addBlock(
                  'affine:paragraph',
                  { type: 'text' },
                  parentId,
                  index + 1 + offset,
                )
                const yText = createdBlockYText(spaceDoc, id)
                if (yText?.insert) yText.insert(0, segment)
                localCreated.push({ id, sourceId: edit.id, text: segment })
                // 新块没拿到完整文字就整体回滚，避免出现空段落
                if ((yText?.length ?? -1) !== segment.length) segmentsOk = false
              })
            } catch {
              // 建块或写文字抛错：按失败处理，原块保持不动
              segmentsOk = false
            }
          }
          if (!segmentsOk) {
            for (const item of localCreated) store.deleteBlock?.(item.id)
            localCreated.length = 0
          }
        }

        if (!segmentsOk) continue

        // 2) 分段确认成功后才截断原块
        if (delta) plan.snapshots[edit.id] = { ...plan.snapshots[edit.id], text: delta }
        // 先删尾部再删头部：先动尾部不会让头部的下标位移。
        if (edit.trimEnd) text.delete(length - edit.trimEnd, edit.trimEnd)
        if (edit.trimStart) text.delete(0, edit.trimStart)
        if (edit.indent) text.insert(PARAGRAPH_INDENT, 0)
        created.push(...localCreated)
      }
    })
  }

  // 兜底：事务结束后模型已就绪，仍有空的新块就补写一次（理论上不会发生）
  if (created.length > 0) {
    const missing = created.filter((item) => {
      const model = store.getModelById?.(item.id)
      return model && !(model.text?.toString?.() ?? '')
    })
    if (missing.length > 0) {
      store.transact(() => {
        for (const item of missing) store.getModelById?.(item.id)?.text?.insert?.(item.text, 0)
      })
    }
  }

  // 之前排过版又继续编辑：保留最早的原值，回退才能回到最初状态
  const previous = readDocFormatState(spaceDoc)
  const snapshots = previous?.active ? { ...plan.snapshots, ...previous.blocks } : plan.snapshots
  const createdAll = previous?.active ? [...(previous.created ?? []), ...created] : created
  if (Object.keys(snapshots).length > 0 || createdAll.length > 0) {
    writeDocFormatState(spaceDoc, {
      version: 1,
      active: true,
      appliedAt: Date.now(),
      blocks: snapshots,
      created: createdAll,
    })
  }

  return { changed: plan.edits.length, visited: targets.length }
}

/**
 * 一键回退：删掉分段新建的段落，把原块的类型、对齐与文字写回去。
 *
 * 排版后被用户改过的块不会被覆盖：文字只去掉首行缩进，内容保持用户的最新版本。
 */
export function revertDocFormat(store: BsStore, spaceDoc: Y.Doc | undefined): DocFormatOutcome {
  if (store?.readonly) return { changed: 0, visited: 0, reason: 'readonly' }
  const state = readDocFormatState(spaceDoc)
  if (!state?.active) return { changed: 0, visited: 0, reason: 'no-state' }

  const editorCount = state.created.length + Object.keys(state.blocks).length
  let changed = 0

  if (editorCount > 0) {
    store.transact(() => {
      /** 被用户改过的分段所属的原块：这些原块不再复原文字，避免内容重复 */
      const keptSources = new Set<string>()

      // 1) 先删新建段落：它们的内容没被改过才删，改过就留给用户
      for (const item of state.created) {
        const model = store.getModelById?.(item.id)
        if (!model) continue
        const current = model.text?.toString?.() ?? ''
        if (current !== item.text) {
          keptSources.add(item.sourceId ?? '')
          continue
        }
        store.deleteBlock(item.id)
        changed += 1
      }

      // 2) 再还原原块
      for (const [id, snapshot] of Object.entries(state.blocks)) {
        const model = store.getModelById?.(id)
        if (!model) continue

        const patch: Record<string, unknown> = {}
        if (snapshot.type && model.props?.type !== snapshot.type) patch.type = snapshot.type
        const currentAlign =
          typeof model.props?.textAlign === 'string' ? model.props.textAlign : null
        if (currentAlign !== snapshot.textAlign) patch.textAlign = snapshot.textAlign
        if (Object.keys(patch).length > 0) {
          store.updateBlock(id, patch)
          changed += 1
        }

        // 该原块分出去的段落被用户改过：保留现在的分段结构，只还原属性
        if (!snapshot.text || keptSources.has(id)) continue
        const current = model.text?.toString?.() ?? ''
        if (!snapshot.formatted || current === snapshot.formatted) {
          // 文本仍是排版结果：原样写回（delta 保留加粗等行内样式）
          if (current !== deltaText(snapshot.text)) {
            model.text.clear()
            model.text.applyDelta(snapshot.text)
            changed += 1
          }
        } else if (current.startsWith(PARAGRAPH_INDENT)) {
          // 用户排版后又编辑过：只撤销我们加的首行缩进，不覆盖他的内容
          model.text.delete(0, PARAGRAPH_INDENT.length)
          changed += 1
        }
      }
    })
  }

  writeDocFormatState(spaceDoc, {
    version: 1,
    active: false,
    appliedAt: 0,
    blocks: {},
    created: [],
  })
  return { changed, visited: editorCount }
}
