/**
 * 工作台错误码解析（纯函数，无依赖）。
 *
 * 单独成文件的原因：api/workbench.ts 依赖 axios 与 i18n，无法在
 * `tsx --test` 里直接加载；保存队列等纯逻辑模块需要判码，只能引这里。
 * api/workbench.ts 把这些函数再导出一次，业务侧仍然只从一处导入。
 */

/** 后端错误码（2400–2499，见 internal/errors/errors.go）。 */
export const WORKBENCH_ERROR = {
  notFound: 2400,
  versionConflict: 2401,
  payloadTooLarge: 2402,
  archiveBusy: 2404,
  archiveStale: 2405,
  noEmbeddingModel: 2406,
  noModel: 2408,
  schemaUnsupported: 2409,
  streamBroken: 2410,
  invalidField: 2411,
  systemKB: 2412,
} as const

/** request.ts 会把后端 error 对象原样带到 reject 值上。 */
export function workbenchErrorCode(err: unknown): number {
  const e = err as { error?: { code?: number }; code?: number }
  return Number(e?.error?.code ?? e?.code ?? 0)
}

/** 读取 2401 冲突响应里的当前版本，供“重载后重试”使用。 */
export function workbenchConflictVersion(err: unknown): number | null {
  const e = err as {
    error?: { details?: { current_version?: number } }
    details?: { current_version?: number }
  }
  const version = e?.error?.details?.current_version ?? e?.details?.current_version
  return typeof version === 'number' ? version : null
}
