import { writeFileSync } from 'node:fs'
import { relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { blockSuiteRoot, scanBlockSuitePackages } from './blocksuite-packages.mjs'

const frontendRoot = fileURLToPath(new URL('..', import.meta.url))
const paths = { '@/*': ['./src/*'] }

// 按 package.json 的 name 生成 paths，路径统一成相对 frontend 的 posix 形式（Windows 反斜杠要替换）
const packages = scanBlockSuitePackages()
for (const [name, dir] of packages) {
  const rel = relative(frontendRoot, dir).split('\\').join('/')
  paths[name] = [rel]
  paths[`${name}/*`] = [`${rel}/*`]
}

writeFileSync(
  new URL('../tsconfig.blocksuite.json', import.meta.url),
  JSON.stringify({ compilerOptions: { paths } }, null, 2) + '\n',
)

console.log(`[blocksuite:paths] 已生成 tsconfig.blocksuite.json（${packages.size} 个 @blocksuite 包，根：${blockSuiteRoot}）`)
