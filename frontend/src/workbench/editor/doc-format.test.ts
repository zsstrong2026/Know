import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  PARAGRAPH_INDENT,
  applyDocFormat,
  collectFormatTargets,
  planDocFormat,
  readDocFormatState,
  revertDocFormat,
  segmentParagraph,
} from './doc-format'

/** 造一个文本对象：行为与 BlockSuite 的 Text 对齐（按字符下标增删）。 */
function makeText(initial: string) {
  let value = initial
  return {
    toString: () => value,
    get length() {
      return value.length
    },
    delete(index: number, count: number) {
      value = value.slice(0, index) + value.slice(index + count)
    },
    insert(content: string, index: number) {
      value = value.slice(0, index) + content + value.slice(index)
    },
    clear() {
      value = ''
    },
    toDelta: () => (value ? [{ insert: value }] : []),
    applyDelta(delta: Array<{ insert?: unknown }>) {
      for (const op of delta) {
        if (typeof op.insert === 'string') value += op.insert
      }
    },
  }
}

/** 造一个正文块；parent 由 fakeStore 统一补。 */
function block(id: string, type: string, text: string, textAlign?: string) {
  return {
    id,
    flavour: 'affine:paragraph',
    props: { type, textAlign } as Record<string, unknown>,
    text: makeText(text),
    children: [] as any[],
    parent: null as any,
  }
}

function noteOf(children: any[]) {
  return { id: 'note1', flavour: 'affine:note', props: {}, children, parent: null as any }
}

/** 把测试 Text 包装成 Y.Text 的调用约定（insert 先下标后内容），供新建块使用。 */
function asYText(text: ReturnType<typeof makeText>) {
  return {
    get length() {
      return text.length
    },
    insert(index: number, content: string) {
      text.insert(content, index)
    },
    toString: () => text.toString(),
  }
}

/**
 * 模拟 BlockSuite 的 store：
 *   - `addBlock` 立即写 Y 层（`sys:children`、`prop:text`），但模型树要等事务结束才更新，
 *     与真实实现一致（Yjs 观察者是事务末回调）；
 *   - 这样「同一事务里连续分段多个段落」的插入位置才可复现。
 */
function fakeStore(children: any[], doc: any, registerText = true) {
  const root = { id: 'root', flavour: 'affine:page', props: {}, children, parent: null as any }
  const linkParent = (parent: any) => {
    for (const child of parent.children ?? []) {
      child.parent = parent
      linkParent(child)
    }
  }
  linkParent(root)

  /** Y 层结构：id → 子块 id 列表（即时更新） */
  const yChildren = new Map<string, string[]>()
  let pending: Array<{ parent: any; block: any; id: string }> = []
  let depth = 0
  let seq = 0

  const expose = (block: any) => {
    const ids = (block.children ?? []).map((child: any) => child.id)
    yChildren.set(block.id, ids)
    doc.getMap('blocks').set(block.id, {
      get: (key: string) => {
        if (key === 'prop:text') return registerText ? asYText(block.text) : undefined
        if (key === 'sys:children') {
          return {
            toArray: () => ids.slice(),
            insert: (index: number, values: string[]) => ids.splice(index, 0, ...values),
            delete: (index: number, count: number) => ids.splice(index, count),
          }
        }
        return undefined
      },
    })
  }
  const register = (block: any) => {
    expose(block)
    for (const child of block.children ?? []) register(child)
  }
  register(root)

  const find = (id: string): any => {
    let found: any = null
    const walk = (blocks: any[]) => {
      for (const item of blocks) {
        if (item.id === id) found = item
        if (item.children) walk(item.children)
      }
    }
    walk(root.children)
    return found
  }

  /** 事务结束：把挂起的新块按 Y.Array 的当前顺序挂进模型树。 */
  const flush = () => {
    const queue = pending
    pending = []
    for (const item of queue) {
      const ids = yChildren.get(item.parent.id) ?? []
      const at = ids.indexOf(item.id)
      if (at >= 0) item.parent.children.splice(at, 0, item.block)
    }
  }

  const store: any = {
    readonly: false,
    root,
    transacts: 0,
    updateBlock(id: string, patch: Record<string, unknown>) {
      Object.assign(find(id)!.props, patch)
    },
    getModelById(id: string) {
      return find(id)
    },
    addBlock(flavour: string, props: Record<string, unknown>, parentId: string, index: number) {
      const parent = find(parentId)
      seq += 1
      const created = {
        id: `new-${seq}`,
        flavour,
        props: { ...props },
        text: makeText(''),
        children: [] as any[],
        parent,
      }
      const ids = yChildren.get(parentId)
      if (ids) ids.splice(index, 0, created.id)
      expose(created)
      pending.push({ parent, block: created, id: created.id })
      return created.id
    },
    deleteBlock(id: string) {
      for (const ids of yChildren.values()) {
        const index = ids.indexOf(id)
        if (index >= 0) ids.splice(index, 1)
      }
      const pendingIndex = pending.findIndex((item) => item.id === id)
      if (pendingIndex >= 0) {
        pending.splice(pendingIndex, 1)
        return
      }
      const target = find(id)
      const siblings = target?.parent?.children as any[] | undefined
      if (!siblings) return
      const index = siblings.findIndex((child) => child.id === id)
      if (index >= 0) siblings.splice(index, 1)
    },
    transact(fn: () => void) {
      store.transacts += 1
      depth += 1
      try {
        fn()
      } finally {
        depth -= 1
        if (depth === 0) flush()
      }
    },
  }
  return store
}

function fakeDoc() {
  const maps = new Map<string, Map<string, unknown>>()
  return {
    getMap: (name: string) => {
      let map = maps.get(name)
      if (!map) {
        map = new Map<string, unknown>()
        maps.set(name, map)
      }
      return map
    },
    transact: (fn: () => void) => fn(),
  } as any
}

/** 8 句、152 字：超过分段阈值，按句号可拆成两段。 */
const LONG_TEXT = '这是用于测试自动分段功能的一句话内容。'.repeat(8)

test('planDocFormat：标题不跳级、正文左对齐、首行缩进与裁剪', () => {
  const plan = planDocFormat([
    { id: 'h1', flavour: 'affine:paragraph', type: 'h3', textAlign: null, text: '第一章' },
    { id: 'h2', flavour: 'affine:paragraph', type: 'h5', textAlign: 'center', text: '小节' },
    { id: 'p1', flavour: 'affine:paragraph', type: 'text', textAlign: 'right', text: '  正文  ' },
  ])

  assert.equal(plan.edits.length, 3)
  assert.deepEqual(plan.edits[0], { id: 'h1', source: '第一章', type: 'h1', textAlign: 'left' })
  // 上一级已被规整为 h1，这里最多深一层 → h2
  assert.deepEqual(plan.edits[1], { id: 'h2', source: '小节', type: 'h2', textAlign: 'left' })
  assert.deepEqual(plan.edits[2], {
    id: 'p1',
    source: '  正文  ',
    textAlign: 'left',
    trimStart: 2,
    trimEnd: 2,
    indent: true,
  })
  assert.equal(plan.snapshots.p1.formatted, `${PARAGRAPH_INDENT}正文`)
  assert.deepEqual(plan.snapshots.h2, { textAlign: 'center', type: 'h5', formatted: '小节' })
})

test('planDocFormat：列表只裁剪不对齐标题，空白段落不参与', () => {
  const plan = planDocFormat([
    { id: 'l1', flavour: 'affine:list', type: 'bulleted', textAlign: null, text: ' 要点 ' },
    { id: 'blank', flavour: 'affine:paragraph', type: 'text', textAlign: 'center', text: '   ' },
  ])
  assert.deepEqual(plan.edits, [
    { id: 'l1', source: ' 要点 ', textAlign: 'left', trimStart: 1, trimEnd: 1, indent: false },
  ])
})

test('collectFormatTargets：不下钻数据库子树', () => {
  const targets = collectFormatTargets({
    id: 'root',
    children: [
      {
        id: 'note1',
        flavour: 'affine:note',
        children: [
          block('p1', 'text', '正文'),
          {
            id: 'db1',
            flavour: 'affine:database',
            props: {},
            children: [block('row1', 'text', '行标题')],
          },
        ],
      },
    ],
  })
  assert.deepEqual(
    targets.map((target) => target.id),
    ['p1'],
  )
})

test('segmentParagraph：只在句末断开，短段与无标点长文不拆', () => {
  const chunks = segmentParagraph(LONG_TEXT)
  assert.equal(chunks.length, 2)
  assert.equal(chunks.join(''), LONG_TEXT)
  assert.ok(chunks.every((chunk) => chunk.endsWith('。')))

  assert.deepEqual(segmentParagraph('一句话。'), ['一句话。'])
  assert.deepEqual(segmentParagraph('没有标点'.repeat(60)), ['没有标点'.repeat(60)])
})

test('applyDocFormat / revertDocFormat：自动分段 + 首行缩进可完整还原', () => {
  const body = block('p1', 'text', LONG_TEXT)
  const note = noteOf([body])
  const doc = fakeDoc()
  const store = fakeStore([note], doc)

  const applied = applyDocFormat(store, doc)
  const chunks = segmentParagraph(LONG_TEXT)
  assert.equal(applied.changed, 1)
  assert.equal(store.transacts, 1)
  assert.equal(body.props.textAlign, 'left')
  assert.equal(body.text.toString(), PARAGRAPH_INDENT + chunks[0])
  // 分段新增的段落排在原块之后，且都带首行缩进
  assert.equal(note.children.length, chunks.length)
  for (const child of note.children.slice(1)) {
    assert.ok(child.text.toString().startsWith(PARAGRAPH_INDENT))
  }
  assert.equal(readDocFormatState(doc)!.created.length, chunks.length - 1)

  const reverted = revertDocFormat(store, doc)
  assert.ok(reverted.changed > 0)
  assert.equal(note.children.length, 1)
  assert.equal(body.text.toString(), LONG_TEXT)
  assert.equal(body.props.textAlign, null)
  assert.equal(readDocFormatState(doc)?.active, false)
})

test('revertDocFormat：用户改过的分段不删、对应原块不复原文字', () => {
  const body = block('p1', 'text', LONG_TEXT)
  const note = noteOf([body])
  const doc = fakeDoc()
  const store = fakeStore([note], doc)

  applyDocFormat(store, doc)
  const createdId = note.children[1].id
  store.getModelById(createdId).text.insert('（用户补充）', 0)

  revertDocFormat(store, doc)
  // 改过的分段保留，原块保持排版后的首段（不写回全文，避免内容重复）
  assert.equal(note.children.length, 2)
  assert.ok(note.children[1].text.toString().startsWith('（用户补充）'))
  assert.ok(body.text.toString().startsWith(PARAGRAPH_INDENT))
})

test('applyDocFormat：二次排版保留最早快照，回退仍回到最初状态', () => {
  const title = block('h1', 'h2', '标题')
  const note = noteOf([title])
  const doc = fakeDoc()
  const store = fakeStore([note], doc)

  applyDocFormat(store, doc)
  // 排版后用户又加了一段正文
  const added = block('p2', 'text', '新增正文')
  note.children.push(added)
  added.parent = note
  applyDocFormat(store, doc)

  assert.deepEqual(readDocFormatState(doc)!.blocks.h1, {
    textAlign: null,
    type: 'h2',
    formatted: '标题',
  })
  const reverted = revertDocFormat(store, doc)
  assert.ok(reverted.changed > 0)
  assert.equal(title.props.type, 'h2')
  assert.equal(added.text.toString(), '新增正文')
})

test('revertDocFormat：没有排版状态时不动文档', () => {
  const body = block('p1', 'text', '正文', 'center')
  const doc = fakeDoc()
  const store = fakeStore([noteOf([body])], doc)
  const outcome = revertDocFormat(store, doc)
  assert.equal(outcome.reason, 'no-state')
  assert.equal(body.props.textAlign, 'center')
})

test('applyDocFormat：分段写入失败时回滚，绝不截断原文', () => {
  const body = block('p1', 'text', LONG_TEXT)
  const note = noteOf([body])
  const doc = fakeDoc()
  // 模拟真实环境里新块 Y.Text 拿不到的情况：不注册块的 prop:text
  const store = fakeStore([note], doc, false)

  applyDocFormat(store, doc)
  // 新建的分段被回滚，原块文字一个字符都不能少
  assert.equal(note.children.length, 1)
  assert.equal(body.text.toString(), LONG_TEXT)
})

test('planDocFormat：优先采用 AI 分段，内容对不上时回落规则分段', () => {
  const sentences = LONG_TEXT.match(/[^。]+。/g) as string[]
  const aiChunks = [sentences.slice(0, 4).join(''), sentences.slice(4).join('')]
  const targets = [
    { id: 'p1', flavour: 'affine:paragraph', type: 'text', textAlign: null, text: LONG_TEXT },
  ]

  const withAI = planDocFormat(targets, { segments: { p1: aiChunks } })
  assert.equal(withAI.snapshots.p1.formatted, PARAGRAPH_INDENT + aiChunks[0])
  assert.deepEqual(withAI.edits[0].segments, [PARAGRAPH_INDENT + aiChunks[1]])

  // 模型改写了正文：校验失败，回落到规则分段（8 句 → 2 段）
  const tampered = planDocFormat(targets, { segments: { p1: ['模型改写过的内容'] } })
  const ruleChunks = segmentParagraph(LONG_TEXT)
  assert.equal(tampered.snapshots.p1.formatted, PARAGRAPH_INDENT + ruleChunks[0])
  assert.deepEqual(
    tampered.edits[0].segments,
    ruleChunks.slice(1).map((chunk) => PARAGRAPH_INDENT + chunk),
  )
})

test('applyDocFormat：AI 分段写入新块', () => {
  const body = block('p1', 'text', LONG_TEXT)
  const note = noteOf([body])
  const doc = fakeDoc()
  const store = fakeStore([note], doc)
  const aiChunks = [
    '这是用于测试自动分段功能的一句话内容。',
    LONG_TEXT.slice('这是用于测试自动分段功能的一句话内容。'.length),
  ]

  applyDocFormat(store, doc, { segments: { p1: aiChunks } })
  assert.equal(body.text.toString(), PARAGRAPH_INDENT + aiChunks[0])
  assert.equal(note.children[1].text.toString(), PARAGRAPH_INDENT + aiChunks[1])
})

test('applyDocFormat：多个段落分段后仍按原顺序排列', () => {
  const first = block('a', 'text', LONG_TEXT)
  const second = block('b', 'text', LONG_TEXT)
  const note = noteOf([first, second])
  const doc = fakeDoc()
  const store = fakeStore([note], doc)
  const perParagraph = segmentParagraph(LONG_TEXT).length - 1

  applyDocFormat(store, doc)

  const shape = note.children.map((child) => (child.id.startsWith('new-') ? 'seg' : child.id))
  assert.deepEqual(shape, [
    'a',
    ...Array(perParagraph).fill('seg'),
    'b',
    ...Array(perParagraph).fill('seg'),
  ])
  // 两个原块都保留了自己的首段
  const head = PARAGRAPH_INDENT + segmentParagraph(LONG_TEXT)[0]
  assert.equal(first.text.toString(), head)
  assert.equal(second.text.toString(), head)
})
