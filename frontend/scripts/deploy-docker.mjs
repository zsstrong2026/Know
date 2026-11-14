/**
 * 把本地 `dist` 同步到运行中的 WeKnora 前端容器（默认 80 端口预览用）。
 *
 * 为什么需要：`WeKnora-frontend` 容器没有挂载本地目录，80 端口读的是镜像里
 * 的旧构建产物，本地 `npm run build` 不会反映过去。这里只覆盖页面与静态资源，
 * **保留容器内 entrypoint 生成的 `config.js`**（运行时注入上传大小/默认语言，
 * 与构建产物里的同名文件内容不同，覆盖会让运行时配置丢失）。
 *
 * 用法：
 *   npm run build && npm run deploy:docker
 *   或一步：npm run build:deploy
 *
 * 可用环境变量覆盖容器名：WEKNORA_FRONTEND_CONTAINER
 */
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const dist = resolve(root, 'dist')
const container = process.env.WEKNORA_FRONTEND_CONTAINER || 'WeKnora-frontend'
const target = '/usr/share/nginx/html'

if (!existsSync(resolve(dist, 'index.html'))) {
  console.error('[deploy] 没找到 dist/index.html，先执行 npm run build')
  process.exit(1)
}

/** 跑一条 docker 命令；失败时打印原始 stderr，便于判断是容器没起还是路径不对。 */
function docker(args, { capture = false } = {}) {
  return execFileSync('docker', args, {
    encoding: 'utf8',
    stdio: capture ? ['ignore', 'pipe', 'pipe'] : 'inherit',
  })
}

try {
  const status = docker(['inspect', container, '--format', '{{.State.Status}}'], {
    capture: true,
  }).trim()
  if (status !== 'running') {
    console.error(`[deploy] 容器 ${container} 当前状态是 ${status}，先启动它`)
    process.exit(1)
  }
} catch {
  console.error(`[deploy] 找不到容器 ${container}，先启动 docker compose`)
  process.exit(1)
}

// 页面入口与静态资源：index.html 引用的 hash 文件名都在 assets 里。
for (const file of ['index.html', 'embed.html']) {
  docker(['cp', resolve(dist, file), `${container}:${target}/${file}`])
}
const assetsSource = `${resolve(dist, 'assets')}${process.platform === 'win32' ? '\\.' : '/.'}`
docker(['cp', assetsSource, `${container}:${target}/assets/`])

// public 下的媒体资源（如登录页背景视频）同样不会打进 assets，需单独同步，
// 否则容器内 404 会落进 SPA 回退返回 index.html，<video> 无法播放。
const mediaDir = resolve(dist, 'media')
if (existsSync(mediaDir)) {
  docker(['exec', container, 'mkdir', '-p', `${target}/media`])
  const mediaSource = `${mediaDir}${process.platform === 'win32' ? '\\.' : '/.'}`
  docker(['cp', mediaSource, `${container}:${target}/media/`])
}

console.log(`[deploy] dist 已同步到 ${container}:${target}（config.js 未覆盖）`)
