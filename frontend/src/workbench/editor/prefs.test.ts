import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  BORDER_WIDTH_MAX,
  DEFAULT_EDITOR_PREFS,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  autoTitleFor,
  editorPrefsKey,
  editorPrefsStyle,
  loadEditorPrefs,
  noteStyleFromPrefs,
  noteStyleSeedFromPrefs,
  parseEditorPrefs,
  resolveCreateTitle,
  saveEditorPrefs,
  type EditorPrefs,
} from './prefs.ts'

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

test('15 项默认值', () => {
  assert.deepEqual(Object.keys(DEFAULT_EDITOR_PREFS).sort(), [
    'autoNameWithDate',
    'canvasScheme',
    'codeLineNumbers',
    'defaultCreateMode',
    'fontFamily',
    'fontSize',
    'noteBackground',
    'noteBorderStyle',
    'noteBorderWidth',
    'noteRadius',
    'noteShadow',
    'pageWidth',
    'scrollZoom',
    'showAddIcon',
    'showDocInfo',
  ])
  assert.equal(DEFAULT_EDITOR_PREFS.fontSize, 16)
  assert.equal(DEFAULT_EDITOR_PREFS.defaultCreateMode, 'page')
  assert.equal(DEFAULT_EDITOR_PREFS.noteBorderStyle, 'none')
  assert.equal(DEFAULT_EDITOR_PREFS.noteBorderWidth, 1)
})

test('存储键带租户与用户', () => {
  assert.equal(editorPrefsKey(7, 'u1'), 'know.workbench.editor-prefs.v1:7:u1')
  assert.equal(editorPrefsKey(0, 'u1'), '')
  assert.equal(editorPrefsKey(7, ''), '')
})

test('字号越界钳制到 14–20 的整数', () => {
  assert.equal(parseEditorPrefs({ fontSize: 8 }).fontSize, FONT_SIZE_MIN)
  assert.equal(parseEditorPrefs({ fontSize: 99 }).fontSize, FONT_SIZE_MAX)
  assert.equal(parseEditorPrefs({ fontSize: 17.6 }).fontSize, 18)
  assert.equal(parseEditorPrefs({ fontSize: 'abc' }).fontSize, 16, '非数字回默认')
})

test('边框厚度钳制到 1–4 的整数', () => {
  assert.equal(parseEditorPrefs({ noteBorderWidth: 0 }).noteBorderWidth, 1)
  assert.equal(parseEditorPrefs({ noteBorderWidth: 10 }).noteBorderWidth, BORDER_WIDTH_MAX)
})

test('枚举字段只接受白名单，非法回默认', () => {
  const prefs = parseEditorPrefs({
    fontFamily: 'comic-sans',
    defaultCreateMode: 'canvas',
    pageWidth: 'narrow',
    canvasScheme: 'dark',
    noteBackground: 'pink',
    noteRadius: 'huge',
    noteShadow: 'glow',
    noteBorderStyle: 'dotted',
  })
  assert.equal(prefs.fontFamily, DEFAULT_EDITOR_PREFS.fontFamily)
  assert.equal(prefs.defaultCreateMode, DEFAULT_EDITOR_PREFS.defaultCreateMode)
  assert.equal(prefs.pageWidth, DEFAULT_EDITOR_PREFS.pageWidth)
  assert.equal(prefs.canvasScheme, DEFAULT_EDITOR_PREFS.canvasScheme)
  assert.equal(prefs.noteBackground, DEFAULT_EDITOR_PREFS.noteBackground)
  assert.equal(prefs.noteRadius, DEFAULT_EDITOR_PREFS.noteRadius)
  assert.equal(prefs.noteShadow, DEFAULT_EDITOR_PREFS.noteShadow)
  assert.equal(prefs.noteBorderStyle, DEFAULT_EDITOR_PREFS.noteBorderStyle)
})

test('布尔字段只接受真正的 boolean', () => {
  const prefs = parseEditorPrefs({ autoNameWithDate: 'false', showAddIcon: 0, scrollZoom: 'true' })
  assert.equal(prefs.autoNameWithDate, false, '字符串 "false" 不算 false，回默认')
  assert.equal(prefs.showAddIcon, true, '数字 0 不算 false，回默认')
  assert.equal(prefs.scrollZoom, false)
})

test('损坏 JSON 回默认且不抛错', () => {
  assert.deepEqual(loadEditorPrefs(memStorage({ k: '{oops' }), 'k'), DEFAULT_EDITOR_PREFS)
  assert.deepEqual(loadEditorPrefs(null, 'k'), DEFAULT_EDITOR_PREFS)
  assert.deepEqual(loadEditorPrefs(memStorage(), ''), DEFAULT_EDITOR_PREFS)
})

test('读写往返保留全部字段', () => {
  const storage = memStorage()
  const prefs: EditorPrefs = {
    ...DEFAULT_EDITOR_PREFS,
    fontFamily: 'serif',
    fontSize: 18,
    autoNameWithDate: true,
    canvasScheme: 'contrast',
    noteBorderStyle: 'dashed',
    noteBorderWidth: 3,
  }
  saveEditorPrefs(storage, 'k', prefs)
  assert.deepEqual(loadEditorPrefs(storage, 'k'), prefs)
})

test('CSS 变量投影：字体、字号、宽度、圆角、边框', () => {
  const style = editorPrefsStyle({
    ...DEFAULT_EDITOR_PREFS,
    fontFamily: 'mono',
    fontSize: 18,
    pageWidth: 'wide',
    noteRadius: 'lg',
    noteBorderWidth: 2,
  })
  assert.equal(style['--wb-page-font-size'], '18px')
  assert.equal(style['--wb-page-max-width'], 'none')
  assert.equal(style['--wb-note-radius'], '12px')
  assert.equal(style['--wb-note-border-width'], '2px')
  assert.ok(style['--wb-page-font'].includes('monospace'))
  // BlockSuite 消费的变量必须同步，否则「改字号 Page 立即变化」不成立
  assert.equal(style['--affine-font-base'], '18px')
  assert.equal(style['--affine-editor-width'], '100%')
  assert.ok(style['--affine-font-family'].includes('monospace'))
})

test('字体选默认时不覆盖主题字体栈', () => {
  const style = editorPrefsStyle({ ...DEFAULT_EDITOR_PREFS, fontFamily: 'default' })
  assert.equal(style['--wb-page-font'], undefined)
  assert.equal(style['--affine-font-family'], undefined)
  assert.equal(style['--affine-font-base'], '16px', '字号仍然生效')
})

test('笔记样式投影成 BlockSuite 可直接吃到的值', () => {
  const seed = noteStyleSeedFromPrefs({
    ...DEFAULT_EDITOR_PREFS,
    noteBackground: 'cream',
    noteRadius: 'md',
    noteShadow: 'soft',
    noteBorderStyle: 'dashed',
    noteBorderWidth: 2,
  })
  assert.equal(seed.background, '#fbf6ec')
  assert.equal(seed.borderRadius, 8)
  assert.equal(seed.borderSize, 2)
  assert.equal(seed.borderStyle, 'dashed')
  assert.equal(seed.shadowType, '--affine-note-shadow-film')
  assert.equal(
    noteStyleSeedFromPrefs({ ...DEFAULT_EDITOR_PREFS, noteShadow: 'none' }).shadowType,
    '',
    '上游用空串表示无阴影',
  )
})

test('新建笔记样式：none 边框仍保留厚度', () => {
  const props = noteStyleFromPrefs({
    ...DEFAULT_EDITOR_PREFS,
    noteBorderStyle: 'none',
    noteBorderWidth: 3,
  })
  assert.equal(props.borderStyle, 'none')
  assert.equal(props.borderWidth, 3, '切回实线时要恢复上次的厚度')
})

test('自动命名用设备本地日期，不是 UTC', () => {
  // 本地时间 2026-10-28 23:30（UTC 会变成 29 日，这里断言用的是本地字段）
  const local = new Date(2026, 9, 28, 23, 30)
  assert.equal(autoTitleFor(local), '2026年10月28日')
})

test('自动命名只影响空标题；用户输入优先', () => {
  const on = { ...DEFAULT_EDITOR_PREFS, autoNameWithDate: true }
  const off = { ...DEFAULT_EDITOR_PREFS, autoNameWithDate: false }
  const now = new Date(2026, 9, 28)
  assert.equal(resolveCreateTitle(on, '', now), '2026年10月28日')
  assert.equal(resolveCreateTitle(on, '我的笔记', now), '我的笔记')
  assert.equal(resolveCreateTitle(off, '', now), '', '关闭时返回空串，沿用后端默认')
  assert.equal(resolveCreateTitle(on, '   ', now), '2026年10月28日', '全空白等同未填')
})
