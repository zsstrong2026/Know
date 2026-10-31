/**
 * 生成内置模板的 .ydoc 快照（§9.16.3）。
 *
 * 用法：`node scripts/gen-templates.mjs`
 *
 * 为什么用 esbuild 而不是 Vite SSR：BlockSuite 的源码在 third_party（TS 直出），
 * 且依赖里有 CJS 包（lodash.ismatch / rxjs）。Vite 的 SSR 运行器对内联的 CJS
 * 会报 `module is not defined`；esbuild 一次性 bundle 到单文件 ESM，
 * 自带 CJS 互操作，跑起来最省事。
 *
 * 升级 BlockSuite 后重跑一次并替换 assets 下的 8 个 .ydoc 即可。
 */
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL, fileURLToPath } from 'node:url'

import { build } from 'esbuild'

import { blockSuiteExportMap } from './blocksuite-packages.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const frontendRoot = join(here, '..')
const assetsDir = join(frontendRoot, 'src/workbench/templates/assets')
const outFile = join(frontendRoot, '.tmp-templates/generate-core.mjs')

// 把 @blocksuite/* 的 exports 映射成真实源码文件：third_party 的包没有 dist
const alias = {}
for (const [specifier, file] of blockSuiteExportMap()) {
  alias[specifier] = file
}

rmSync(join(frontendRoot, '.tmp-templates'), { recursive: true, force: true })
await build({
  entryPoints: [join(frontendRoot, 'src/workbench/templates/generate-core.ts')],
  bundle: true,
  platform: 'node',
  format: 'esm',
  target: 'node20',
  outfile: outFile,
  alias,
  // third_party/blocksuite 里没有 node_modules，裸导入必须回落到 frontend 的依赖
  nodePaths: [join(frontendRoot, 'node_modules')],
  // 必须 false：framework/global/src/di/provider.ts 用参数属性初始化字段，
  // define 语义下字段初始化器早于参数属性赋值，构造 ServiceResolver 必抛 TypeError。
  tsconfigRaw: { compilerOptions: { useDefineForClassFields: false } },
  external: ['yjs', 'rxjs', '@preact/signals-core', 'zod', 'lib0', 'nanoid'],
  logLevel: 'warning',
  // third_party 的 TS 里用 `?raw` / `.css.ts` 的写法在本入口不会出现；
  // 如果将来出现，构建会直接失败并提示，不会静默产出一个坏模板。
})

const mod = await import(pathToFileURL(outFile).href)
mkdirSync(assetsDir, { recursive: true })
let total = 0
for (const [id, lines] of Object.entries(mod.TEMPLATE_DOCS)) {
  const bytes = mod.buildTemplateYDoc(id, lines)
  writeFileSync(join(assetsDir, `${id}.ydoc`), bytes)
  total += bytes.byteLength
  console.log(`${id}: ${bytes.byteLength} bytes`)
}
console.log(`done: ${Object.keys(mod.TEMPLATE_DOCS).length} templates, ${total} bytes total`)
rmSync(join(frontendRoot, '.tmp-templates'), { recursive: true, force: true })
