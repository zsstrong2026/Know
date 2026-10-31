import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
// vendored BlockSuite 根目录（仓库根 /third_party/blocksuite）
export const blockSuiteRoot = resolve(here, '../../third_party/blocksuite')

/**
 * 递归扫描 @blocksuite/* 包，返回 name -> 包目录 的映射。
 */
export function scanBlockSuitePackageDirs(root = blockSuiteRoot) {
  const found = new Map()
  const walk = (dir, depth) => {
    if (depth > 6) return
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.isSymbolicLink()) continue
      if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name === '.git') continue
      const pkgPath = join(dir, entry.name, 'package.json')
      if (existsSync(pkgPath)) {
        const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'))
        if (typeof pkg.name === 'string' && pkg.name.startsWith('@blocksuite/')) {
          found.set(pkg.name, join(dir, entry.name))
        }
      }
      walk(join(dir, entry.name), depth + 1)
    }
  }
  walk(root, 0)
  return found
}

/**
 * 递归扫描 @blocksuite/* 包，返回 name -> 源码目录 的映射。
 * 优先指向包内 src（上游包是 TS 源码直出，没有 dist），避免依赖预打包产物。
 */
export function scanBlockSuitePackages(root = blockSuiteRoot) {
  return new Map(
    [...scanBlockSuitePackageDirs(root)].map(([name, pkgDir]) => {
      const src = join(pkgDir, 'src')
      return [name, existsSync(src) ? src : pkgDir]
    })
  )
}

/** 取 exports 条件对象里的运行时入口（跳过 types，否则会指向不存在的 dist/*.d.ts） */
function pickExportTarget(value) {
  if (typeof value === 'string') return value
  if (!value || typeof value !== 'object') return null
  for (const key of ['import', 'module', 'default']) {
    const picked = pickExportTarget(value[key])
    if (picked) return picked
  }
  return null
}

/**
 * 按各包 package.json 的 exports 生成 specifier -> 绝对文件路径 的映射。
 * 上游有大量 subpath exports（如 @blocksuite/affine-gfx-turbo-renderer/painter
 * 指向 src/painter/painter.worker.ts），只按包名 alias 到 src 目录会丢失这层映射。
 */
export function blockSuiteExportMap(root = blockSuiteRoot) {
  const map = new Map()
  for (const [name, pkgDir] of scanBlockSuitePackageDirs(root)) {
    const pkg = JSON.parse(readFileSync(join(pkgDir, 'package.json'), 'utf8'))
    const exportsField = pkg.exports
    if (exportsField && typeof exportsField === 'object' && !Array.isArray(exportsField)) {
      for (const [subpath, value] of Object.entries(exportsField)) {
        if (!subpath.startsWith('.')) continue
        const target = pickExportTarget(value)
        if (!target) continue
        const specifier = subpath === '.' ? name : `${name}/${subpath.slice(2)}`
        const absolute = resolve(pkgDir, target)
        if (existsSync(absolute)) map.set(specifier, absolute)
      }
    }
    // 没有 exports（或 "." 没解析出来）时回落到 src/index.ts
    if (!map.has(name)) {
      const fallback = resolve(pkgDir, pkg.module ?? pkg.main ?? './src/index.ts')
      if (existsSync(fallback)) map.set(name, fallback)
    }
  }
  return map
}
