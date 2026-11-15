import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  PAGE_AI_ACTIONS,
  PAGE_AI_GROUPS,
  TRANSLATE_LANGS,
  TONE_OPTIONS,
  actionsByGroup,
  compilePageAIAction,
  submenuItems,
  translateLangName,
} from './page-actions.ts'
import { INLINE_AI_ACTIONS, INLINE_AI_INSTRUCTION_LIMIT } from './inline-ai-types.ts'

const ALLOWED = new Set<string>(INLINE_AI_ACTIONS)

test('20 个主项 + 11 个子项', () => {
  assert.equal(PAGE_AI_ACTIONS.length, 20)
  assert.equal(TRANSLATE_LANGS.length, 6)
  assert.equal(TONE_OPTIONS.length, 5)
  assert.equal(TRANSLATE_LANGS.length + TONE_OPTIONS.length, 11)
})

test('每个主项都映射到六个 action 之一（other.chat 除外）', () => {
  for (const action of PAGE_AI_ACTIONS) {
    if (action.id === 'other.chat') {
      assert.equal(action.action, undefined, 'other.chat 不发请求')
      continue
    }
    assert.ok(action.action, `${action.id} 缺少 action`)
    assert.ok(ALLOWED.has(action.action!), `${action.id} 的 action ${action.action} 不在白名单`)
  }
})

test('五个分组的动作数', () => {
  assert.equal(PAGE_AI_GROUPS.length, 5)
  assert.equal(actionsByGroup('review').length, 3)
  assert.equal(actionsByGroup('edit').length, 7)
  assert.equal(actionsByGroup('generate').length, 5)
  assert.equal(actionsByGroup('draft').length, 4)
  assert.equal(actionsByGroup('other').length, 1)
})

test('写入方式：解释类只出卡片，续写与生成类插到下方', () => {
  const byId = new Map(PAGE_AI_ACTIONS.map((action) => [action.id, action]))
  assert.equal(byId.get('review.explain')?.write, 'aside')
  assert.equal(byId.get('edit.continue')?.write, 'below')
  for (const id of ['generate.summarize', 'generate.outline', 'draft.article', 'draft.poem']) {
    assert.equal(byId.get(id)?.write, 'below', id)
  }
  for (const id of ['review.spelling', 'edit.polish', 'edit.humanize', 'edit.translate', 'edit.tone']) {
    assert.equal(byId.get(id)?.write, 'replace', id)
  }
})

test('翻译子菜单是六种语言，语气子菜单是五种', () => {
  const langs = submenuItems('translate')
  assert.deepEqual(
    langs.map((item) => item.targetLang),
    ['en', 'zh-Hans', 'zh-Hant', 'ja', 'ko', 'fr'],
  )
  assert.ok(langs.every((item) => item.action === 'translate' && item.write === 'replace'))

  const tones = submenuItems('tone')
  assert.equal(tones.length, 5)
  assert.ok(tones.every((item) => item.action === 'custom' && item.write === 'replace'))
  // 语气预设必须锁住事实与数字，避免改写时凭空变数字。
  assert.ok(tones.every((item) => item.instruction?.includes('数字与截止时间不变')))
})

test('未指定子菜单时不返回可用项', () => {
  assert.equal(submenuItems(undefined).length, 0)
  const translate = PAGE_AI_ACTIONS.find((action) => action.id === 'edit.translate')!
  assert.equal(compilePageAIAction(translate), null)
  assert.equal(compilePageAIAction(translate, 'edit.translate.xx'), null)
})

test('编译翻译动作带上 target_lang 与替代文案参数', () => {
  const translate = PAGE_AI_ACTIONS.find((action) => action.id === 'edit.translate')!
  const compiled = compilePageAIAction(translate, 'edit.translate.ja')!
  assert.equal(compiled.action, 'translate')
  assert.equal(compiled.targetLang, 'ja')
  assert.equal(compiled.write, 'replace')
  assert.deepEqual(compiled.labelArgs, { value: '日语' })
})

test('编译语气动作带上 instruction', () => {
  const tone = PAGE_AI_ACTIONS.find((action) => action.id === 'edit.tone')!
  const compiled = compilePageAIAction(tone, 'edit.tone.concise')!
  assert.equal(compiled.action, 'custom')
  assert.ok(compiled.instruction?.includes('简洁'))
})

test('other.chat 编译为 null（只打开侧栏）', () => {
  const chat = PAGE_AI_ACTIONS.find((action) => action.id === 'other.chat')!
  assert.equal(compilePageAIAction(chat), null)
})

test('去 AI 味：编译为 custom + replace，且指令不超长', () => {
  const humanize = PAGE_AI_ACTIONS.find((action) => action.id === 'edit.humanize')!
  const compiled = compilePageAIAction(humanize)!
  assert.equal(compiled.action, 'custom')
  assert.equal(compiled.write, 'replace')
  assert.ok((compiled.instruction?.length ?? 0) <= INLINE_AI_INSTRUCTION_LIMIT)
})

test('翻译语言名可回退', () => {
  assert.equal(translateLangName('en'), '英语')
  assert.equal(translateLangName('pt-BR'), 'pt-BR')
})

test('不含被明确排除的预设（图片/思维导图/演示文稿/Find actions）', () => {
  const ids = PAGE_AI_ACTIONS.map((action) => action.id)
  for (const forbidden of ['image', 'mindmap', 'slides', 'make-real', 'find-actions']) {
    assert.ok(!ids.some((id) => id.includes(forbidden)), `不应包含 ${forbidden}`)
  }
})
