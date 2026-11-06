import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

/**
 * 统一入口：给 vite build 抬高 V8 堆上限。
 *
 * 原因：BlockSuite + 六语言包在 rendering chunks 阶段会超过 Node 默认堆
 * （约 4GB），直接跑 `vite build` 会随机 OOM。这里用 `node --max-old-space-size`
 * 显式抬高上限，跨平台生效（Windows cmd / PowerShell 不认 `NODE_OPTIONS=...` 前缀写法）。
 * 传参原样转发给 vite，因此 `node scripts/build.mjs --mode spike` 等价于
 * `vite build --mode spike`。
 */
const HEAP_MB = 8192
const viteBin = fileURLToPath(new URL('../node_modules/vite/bin/vite.js', import.meta.url))

const child = spawn(
  process.execPath,
  [`--max-old-space-size=${HEAP_MB}`, viteBin, 'build', ...process.argv.slice(2)],
  { stdio: 'inherit' },
)

child.on('error', (error) => {
  console.error('[build] 启动 vite 失败：', error)
  process.exit(1)
})

// 透传 vite 的退出码，让 CI / npm 能正确判断构建成败。
child.on('exit', (code, signal) => {
  if (signal) {
    console.error(`[build] vite 被信号 ${signal} 终止`)
    process.exit(1)
  }
  process.exit(code ?? 1)
})
