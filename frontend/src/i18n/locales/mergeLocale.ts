// 本地二开：聚合入口需要深合并各域模块与本地增量词条（local.ts）。
// 上游按域拆分后各模块键互不重叠，浅 spread 即可；本地增量会命中上游已有的
// 同名命名空间，必须递归合并，否则会整段覆盖上游新增词条。
type LocaleTree = Record<string, unknown>

function isPlainObject(value: unknown): value is LocaleTree {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function mergeLocale(...trees: unknown[]): LocaleTree {  const result: LocaleTree = {}
  for (const tree of trees) {
    if (!isPlainObject(tree)) continue
    for (const [key, value] of Object.entries(tree)) {
      const current = result[key]
      result[key] =
        isPlainObject(current) && isPlainObject(value) ? mergeLocale(current, value) : value
    }
  }
  return result
}
