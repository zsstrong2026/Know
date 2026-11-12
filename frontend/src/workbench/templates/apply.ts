/**
 * 模板套用（§9.16.5）。
 *
 * 只做三件事：取快照 → 校验 schema → 交给编辑器用快照建 session。
 * 刻意不直接 import 上游 BlockSuite 包：解码、图片/附件块替换都在
 * `editor/blocksuite.ts` 的适配器里完成，这一层保持可单测。
 */

/** 客户端支持的编辑器 schema 版本；与服务端 SupportedEditorSchemaVersion 一致。 */
export const SUPPORTED_TEMPLATE_SCHEMA_VERSION = 1

export type TemplateApplyErrorKind =
  | 'load-failed'
  | 'schema-unsupported'
  | 'empty'
  | 'deleted'

export class TemplateApplyError extends Error {
  constructor(
    readonly kind: TemplateApplyErrorKind,
    message: string,
  ) {
    super(message)
    this.name = 'TemplateApplyError'
  }
}

/** 套用前的快照校验结果：只放字节与 schema，不解析内容。 */
export interface PreparedTemplateSnapshot {
  name: string
  bytes: Uint8Array
  schemaVersion: number
}

/** base64 → 二进制；与 editor-session 里的同实现，避免重复 import 上游。 */
export function decodeTemplateBase64(text: string): Uint8Array {
  const binary = atob(text)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** 二进制 → base64；分块避免超长参数列表导致栈溢出（编辑模板后回写用）。 */
export function encodeTemplateBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

/**
 * 校验并准备模板快照。
 *
 * 校验顺序固定：空 → schema → 字节。任一失败都抛 TemplateApplyError，
 * 让调用方显示「模板无法套用，请从空白文档开始」，而不是挂一个半成品编辑器。
 */
export function prepareTemplateSnapshot(input: {
  name?: string
  ydoc: string
  editor_schema_version: number
}): PreparedTemplateSnapshot {
  const base64 = (input.ydoc ?? '').trim()
  if (!base64) {
    throw new TemplateApplyError('empty', 'template snapshot is empty')
  }
  if ((input.editor_schema_version ?? 0) > SUPPORTED_TEMPLATE_SCHEMA_VERSION) {
    throw new TemplateApplyError(
      'schema-unsupported',
      `template schema ${input.editor_schema_version} is newer than supported ${SUPPORTED_TEMPLATE_SCHEMA_VERSION}`,
    )
  }
  const bytes = decodeTemplateBase64(base64)
  // 2 字节是空 Y.Doc update 的长度；这种模板套用后只会得到一个空文档。
  if (bytes.byteLength <= 2) {
    throw new TemplateApplyError('empty', 'template snapshot has no content')
  }
  return {
    name: (input.name ?? '').trim(),
    bytes,
    schemaVersion: input.editor_schema_version || SUPPORTED_TEMPLATE_SCHEMA_VERSION,
  }
}

/**
 * 套用后是否应该改写标题。
 *
 * 只在标题仍是「未命名」这类占位值时才用模板名，避免覆盖用户已改的名字。
 */
export function shouldAdoptTemplateTitle(currentTitle: string, untitledPlaceholders: string[]): boolean {
  const trimmed = (currentTitle ?? '').trim()
  if (!trimmed) return true
  return untitledPlaceholders.includes(trimmed)
}

/** 「我的模板」配额；与服务端 WorkbenchTemplateMaxPerOwner 一致，UI 预检用。 */
export const TEMPLATE_QUOTA = 50

export function templateQuotaLeft(count: number): number {
  return Math.max(0, TEMPLATE_QUOTA - count)
}
