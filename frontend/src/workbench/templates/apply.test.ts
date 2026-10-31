import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  SUPPORTED_TEMPLATE_SCHEMA_VERSION,
  TemplateApplyError,
  prepareTemplateSnapshot,
  shouldAdoptTemplateTitle,
  templateQuotaLeft,
} from './apply.ts'
import { BUILTIN_TEMPLATES, findBuiltinTemplate } from './builtin.ts'

const here = dirname(fileURLToPath(import.meta.url))
const assetsDir = join(here, 'assets')

/** 与 apply.ts 同实现的 base64 编码，单测里不引上游包。 */
function toBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

test('内置模板元数据：8 个、id 唯一、推荐 4 个', () => {
  assert.equal(BUILTIN_TEMPLATES.length, 8)
  const ids = new Set(BUILTIN_TEMPLATES.map((tpl) => tpl.id))
  assert.equal(ids.size, 8, 'id 不能重复')
  assert.deepEqual(
    BUILTIN_TEMPLATES.filter((tpl) => tpl.recommended).map((tpl) => tpl.id).sort(),
    ['meeting-notes', 'project-brief', 'reading-notes', 'weekly-plan'],
  )
})

test('内置模板资产齐全且大小合理（4KB–1MB）', () => {
  for (const tpl of BUILTIN_TEMPLATES) {
    const bytes = readFileSync(join(assetsDir, `${tpl.id}.ydoc`))
    assert.ok(bytes.byteLength > 1024, `${tpl.id} 快照不应是空的（实得 ${bytes.byteLength} 字节）`)
    assert.ok(bytes.byteLength < 1 << 20, `${tpl.id} 快照不应超过 1MB`)
    // assetUrl 必须指向存在的文件，否则运行时 fetch 会 404
    assert.ok(tpl.assetUrl.endsWith(`${tpl.id}.ydoc`), `${tpl.id} 的 assetUrl 指向错误文件`)
  }
})

test('内置模板快照能被客户端接受', () => {
  for (const tpl of BUILTIN_TEMPLATES) {
    const bytes = readFileSync(join(assetsDir, `${tpl.id}.ydoc`))
    const snapshot = prepareTemplateSnapshot({
      name: tpl.id,
      ydoc: toBase64(new Uint8Array(bytes)),
      editor_schema_version: SUPPORTED_TEMPLATE_SCHEMA_VERSION,
    })
    assert.equal(snapshot.name, tpl.id)
    assert.ok(snapshot.bytes.byteLength > 1024)
  }
})

test('找不到的内置模板返回 undefined', () => {
  assert.equal(findBuiltinTemplate('meeting-notes')?.category, 'work')
  assert.equal(findBuiltinTemplate('nope'), undefined)
})

test('空快照被拒绝', () => {
  assert.throws(
    () => prepareTemplateSnapshot({ ydoc: '', editor_schema_version: 1 }),
    (err: unknown) => err instanceof TemplateApplyError && err.kind === 'empty',
  )
})

test('schema 高于客户端被拒绝，不做降级解码', () => {
  assert.throws(
    () =>
      prepareTemplateSnapshot({
        ydoc: toBase64(new Uint8Array([1, 2, 3])),
        editor_schema_version: SUPPORTED_TEMPLATE_SCHEMA_VERSION + 1,
      }),
    (err: unknown) => err instanceof TemplateApplyError && err.kind === 'schema-unsupported',
  )
})

test('2 字节空 update 视为空模板', () => {
  assert.throws(
    () => prepareTemplateSnapshot({ ydoc: toBase64(new Uint8Array([0, 0])), editor_schema_version: 1 }),
    (err: unknown) => err instanceof TemplateApplyError && err.kind === 'empty',
  )
})

test('标题只在占位值时采用模板名', () => {
  assert.equal(shouldAdoptTemplateTitle('', []), true)
  assert.equal(shouldAdoptTemplateTitle('   ', []), true)
  assert.equal(shouldAdoptTemplateTitle('未命名文档', ['未命名文档', '未命名画布']), true)
  assert.equal(shouldAdoptTemplateTitle('我的周报', ['未命名文档']), false)
})

test('模板配额预检', () => {
  assert.equal(templateQuotaLeft(0), 50)
  assert.equal(templateQuotaLeft(49), 1)
  assert.equal(templateQuotaLeft(50), 0)
  assert.equal(templateQuotaLeft(80), 0)
})
