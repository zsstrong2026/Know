import { fileURLToPath, URL } from 'node:url'
import { resolve, dirname } from 'node:path'
import { existsSync } from 'node:fs'
import { execSync } from 'node:child_process'
import { createRequire } from 'node:module'
import { defineConfig, transformWithEsbuild, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'
import { vanillaExtractPlugin } from '@vanilla-extract/vite-plugin'
import { blockSuiteExportMap, scanBlockSuitePackages } from './scripts/blocksuite-packages.mjs'

const __dirname = dirname(fileURLToPath(import.meta.url))
const require = createRequire(import.meta.url)

// vendored BlockSuite：specifier 映射与 dedupe/exclude 名单全部按上游 package.json 生成
const blockSuiteExports = blockSuiteExportMap()
const blockSuiteNames = [...scanBlockSuitePackages().keys()]
// 必须同时 dedupe 与 optimizeDeps.exclude，否则可能出现两份 lit / 两份 yjs
const blockSuiteSharedDeps = ['lit', 'lit-html', 'lit-element', '@lit/context', 'rxjs', 'yjs']

const pkg = require('./package.json') as { version?: string }
const FRONTEND_VERSION = pkg.version ?? 'unknown'

function resolveFrontendCommit(): string {
  const fromEnv = process.env.VITE_FRONTEND_COMMIT || process.env.GITHUB_SHA
  if (fromEnv) {
    return fromEnv.slice(0, 7)
  }
  try {
    return execSync('git rev-parse --short HEAD', { stdio: ['ignore', 'pipe', 'ignore'] })
      .toString()
      .trim()
  } catch {
    return 'unknown'
  }
}

const FRONTEND_COMMIT = resolveFrontendCommit()

/** Dev parity with nginx: serve embed.html for /embed/:channelId (not the main SPA). */
function embedHtmlDevFallback(): Plugin {
  return {
    name: 'embed-html-dev-fallback',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const raw = req.url ?? ''
        const qIdx = raw.indexOf('?')
        const path = qIdx >= 0 ? raw.slice(0, qIdx) : raw
        const qs = qIdx >= 0 ? raw.slice(qIdx) : ''
        if (path.startsWith('/embed/') && path !== '/embed.html' && !path.includes('.')) {
          req.url = `/embed.html${qs}`
        }
        next()
      })
    },
  }
}
const DEV_PROXY_TARGET =
  process.env.VITE_DEV_PROXY_TARGET ||
  process.env.FRONTEND_BACKEND_URL ||
  'http://localhost:8080'

// vendored 子树的 tsconfig.json 是 `extends: "../tsconfig.web.json"`（AFFiNE 仓库根配置，
// 未随 subtree 拷贝），Vite 内置 esbuild 插件会对每个 TS 文件回溯 tsconfig 并报
// `failed to resolve "extends"`。这里用「字符串 tsconfigRaw」走另一条分支：
// Vite 仅在 tsconfigRaw 为字符串时跳过 tsconfig 文件加载（见 vite 源码 transformWithEsbuild）。
const BLOCKSUITE_DIR_RE = /[\\/]third_party[\\/]blocksuite[\\/]/
const BLOCKSUITE_TS_RE = /[\\/]third_party[\\/]blocksuite[\\/].*\.(?:[cm]?ts|tsx)$/
const BARE_IMPORT_RE = /^[\w@][^:]/
const BLOCKSUITE_TSCONFIG_RAW = JSON.stringify({
  compilerOptions: {
    target: 'es2022',
    // 必须为 false（方案 §10.2.1 写的 true 与上游源码不兼容）：
    // framework/global/src/di/provider.ts:75 用参数属性初始化字段
    //   class ServiceResolver { container = this.provider.container; constructor(readonly provider) {...} }
    // define 语义下字段初始化器在 super() 返回后立即执行、早于参数属性赋值，
    // 运行时必抛 TypeError: Cannot read properties of undefined (reading 'container')。
    // 赋值语义则把初始化器下沉到构造函数里、排在参数属性赋值之后，与上游预期一致。
    useDefineForClassFields: false,
    // 标准装饰器（非 experimentalDecorators）：上游用 @property/@state + accessor 字段
    experimentalDecorators: false,
  },
})

/** 只接管 third_party/blocksuite 下的 TS，绕开缺失的上游 tsconfig 继承链。 */
function blockSuiteTsTransform(): Plugin {
  return {
    name: 'blocksuite-ts-transform',
    enforce: 'pre',
    async transform(code, id) {
      const file = id.split('?')[0]
      // .css.ts 交给 vanilla-extract 自己编译（它用独立 esbuild，不读 Vite 的 tsconfig）
      if (!BLOCKSUITE_TS_RE.test(file) || file.endsWith('.css.ts')) return null
      return transformWithEsbuild(code, file, {
        loader: file.endsWith('.tsx') ? 'tsx' : 'ts',
        target: 'es2022',
        tsconfigRaw: BLOCKSUITE_TSCONFIG_RAW,
        sourcemap: true,
      })
    },
  }
}

/**
 * vendored 包是 TS 源码直出，且大量使用 subpath exports
 * （如 @blocksuite/affine-gfx-turbo-renderer/painter → src/painter/painter.worker.ts）。
 * 只按包名 alias 到 src 目录会丢掉这层映射（会把 .../src/painter 当文件读，报 EISDIR），
 * 因此按 package.json exports 做精确映射，未声明的 deep import 交给 Vite 默认解析。
 */
function blockSuiteExportsResolve(): Plugin {
  return {
    name: 'blocksuite-exports-resolve',
    enforce: 'pre',
    resolveId(id) {
      if (!id.startsWith('@blocksuite/')) return null
      const [specifier, query] = id.split('?')
      const file = blockSuiteExports.get(specifier)
      if (!file) return null
      return query ? `${file}?${query}` : file
    },
  }
}

/**
 * vendored 子树在 frontend/ 之外且自身没有 node_modules，裸导入从 importer 向上查找
 * 到不了 frontend/node_modules（仓库根 node_modules 只有 @resvg）。
 * 这里把 third_party 里的裸导入改成以 frontend 根为基准解析，等价于 Vite dedupe 的做法，
 * 但只影响 third_party，不改变业务代码与 node_modules 内部的解析结果。
 */
function blockSuiteBareImportResolve(): Plugin {
  const frontendPkg = resolve(__dirname, 'package.json')
  const aliased = new Set(blockSuiteNames)
  return {
    name: 'blocksuite-bare-import-resolve',
    enforce: 'pre',
    async resolveId(id, importer) {
      if (!importer || !BLOCKSUITE_DIR_RE.test(importer)) return null
      if (!BARE_IMPORT_RE.test(id)) return null
      // 已被 alias 指向 third_party 源码的 @blocksuite/* 不再处理；
      // 未 vendored 的（如 @blocksuite/icons）仍需按 npm 包解析
      const pkgName = id.startsWith('@') ? id.split('/').slice(0, 2).join('/') : id.split('/')[0]
      if (aliased.has(pkgName)) return null
      return this.resolve(id, frontendPkg, { skipSelf: true })
    },
  }
}

function resolveVueOfficePptxEntry(): string {
  try {
    const pkgDir = dirname(require.resolve('@vue-office/pptx/package.json'))
    const candidates = [
      resolve(pkgDir, 'lib/v3/index.js'),
      resolve(pkgDir, 'lib/index.js'),
      resolve(pkgDir, 'lib/v3/vue-office-pptx.mjs'),
    ]
    const matched = candidates.find((candidate) => existsSync(candidate))
    return matched ?? '@vue-office/pptx'
  } catch {
    return '@vue-office/pptx'
  }
}

export default defineConfig(({ command, mode }) => {
  // 生产构建分两次：默认构建只产出首页/embed（与接入 BlockSuite 之前完全一致），
  // `--mode spike` 再单独构建编辑器入口（见 package.json 的 build 脚本）。
  // 两条 graph 分开后，首页不可能引用编辑器 chunk，也不会出现跨 entry 的循环 chunk。
  const isSpikeBuild = command === 'build' && mode === 'spike'
  const entries = {
    main: resolve(__dirname, 'index.html'),
    embed: resolve(__dirname, 'embed.html'),
    // BlockSuite spike 独立入口：单独 HTML，首页不会引用编辑器 chunk
    editorSpike: resolve(__dirname, 'editor-spike.html'),
  }
  // spike 构建只产出编辑器入口；dev 三个入口都可访问；默认构建只产出首页与 embed
  const buildInput: Record<string, string> = isSpikeBuild
    ? { editorSpike: entries.editorSpike }
    : command === 'serve'
      ? entries
      : { main: entries.main, embed: entries.embed }

  return {
    define: {
      __FRONTEND_VERSION__: JSON.stringify(FRONTEND_VERSION),
      __FRONTEND_COMMIT__: JSON.stringify(FRONTEND_COMMIT),
    },
    build: {
      target: 'es2022',
      // spike 构建写在默认构建的产物目录里，不能清空
      emptyOutDir: !isSpikeBuild,
      modulePreload: {
        resolveDependencies(_filename, deps, { hostId }) {
          // Embed iframe bootstraps with token exchange only; defer heavy chat chunks.
          if (hostId?.includes('embed')) {
            return deps.filter((dep) => !(
              dep.includes('vendor-mermaid')
              || dep.includes('vendor-highlight')
              || dep.includes('vendor-markdown')
              || dep.includes('vendor-tdesign')
              || dep.includes('botmsg')
              || dep.includes('usermsg')
              || dep.includes('EmbedBotMessage')
              || dep.includes('EmbedUserMessage')
              || dep.includes('AgentStreamDisplay')
              || dep.includes('EmbedChatCore')
              || dep.includes('vendor-markdown')
              || dep.includes('fonts-')
            ))
          }
          return deps
        },
      },
      rollupOptions: {
        // spike 构建只产出编辑器入口；dev 三个入口都可访问；默认构建只产出首页与 embed
        input: buildInput,
        output: {
          manualChunks(id) {
            // 只把 vendored BlockSuite 单独成块，避免和 node_modules 供应商混在一个大包里。
            // 首页构建里根本不存在 third_party，因此该分支不会污染首页产物；
            // spike 构建是独立 graph，也不存在跨 entry 的循环 chunk。
            if (id.includes('/third_party/blocksuite/')) return 'vendor-blocksuite'
            if (!id.includes('node_modules')) return
            if (id.includes('mermaid') || id.includes('/dagre') || id.includes('cytoscape')) {
              return 'vendor-mermaid'
            }
            if (id.includes('marked') || id.includes('katex')) {
              return 'vendor-markdown'
            }
            if (id.includes('highlight.js')) {
              return 'vendor-highlight'
            }
          },
        },
      },
    },
    plugins: [
      vue({
        template: {
          compilerOptions: {
            // BlockSuite/上游 Web Components 标签不做 Vue 组件解析
            isCustomElement: (tag) =>
              tag.startsWith('affine-') || tag.startsWith('blocksuite-') || tag === 'edgeless-editor',
          },
        },
      }),
      vueJsx(),
      // vendored BlockSuite 里的 *.css.ts 用 vanilla-extract 编译，默认只匹配 .css.ts/.css.js。
      // 该插件内部会另起一个 vite-node 实例执行 .css.ts，默认只保留 vite-tsconfig-paths，
      // 会把 third_party 的 TS 当 JS 解析（Parse failure: Expected ',', got 'ident'），
      // 因此把下面两个处理 vendored 子树的插件一并放行。
      vanillaExtractPlugin({
        unstable_pluginFilter: ({ name }) =>
          [
            'vite-tsconfig-paths',
            'blocksuite-ts-transform',
            'blocksuite-exports-resolve',
            'blocksuite-bare-import-resolve',
          ].includes(name),
      }),
      // 必须先于内置 esbuild 插件：两者都处理 TS，这里已产出 JS，内置插件不能再对 third_party 回溯 tsconfig
      blockSuiteTsTransform(),
      // 先按 exports 把 @blocksuite/* 映射到真实源码文件，再补 third_party 内部的裸导入
      blockSuiteExportsResolve(),
      // 同样要早于 Vite 的裸导入解析，把 third_party 的裸导入重定向到 frontend/node_modules
      blockSuiteBareImportResolve(),
      embedHtmlDevFallback(),
    ],
    resolve: {
      alias: [
        { find: '@', replacement: fileURLToPath(new URL('./src', import.meta.url)) },
        { find: '@vue-office/pptx', replacement: resolveVueOfficePptxEntry() },
      ],
      dedupe: [...blockSuiteSharedDeps, ...blockSuiteNames],
    },
    optimizeDeps: {
      // vendored 源码是 TS 直出，交给 Vite 全量处理，不做 esbuild 预打包
      exclude: [...blockSuiteSharedDeps, ...blockSuiteNames],
      esbuildOptions: { target: 'es2022' },
    },
    // third_party/blocksuite 的 TS 已由 blockSuiteTsTransform 处理，内置 esbuild 插件必须跳过，
    // 否则它会再次对同一文件回溯 tsconfig 并报 extends 解析失败
    esbuild: { target: 'es2022', exclude: [/third_party[\\/]blocksuite/] },
    server: {
      port: 5173,
      host: true,
      // 开发时需要读取仓库根的 third_party/blocksuite；生产构建不受影响
      fs: { allow: ['..'] },
      // 代理配置，用于开发环境
      proxy: {
        '/mcp/': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
          // Streamable HTTP may keep an SSE response open for long-running tools.
          timeout: 3_600_000,
          proxyTimeout: 3_600_000,
        },
        '/api': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
          // 沙箱终端等 WebSocket 升级请求也走 /api，必须开启 WS 转发，
          // 否则浏览器侧握手失败、前端表现为"一直正在连接"。
          ws: true,
          // Cube fork snapshots pause a live MicroVM; 30s axios/proxy defaults
          // abort the POST and the backend then 500s on a canceled persist.
          timeout: 180_000,
          proxyTimeout: 180_000,
        },
        '/files': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
        }
      }
    },
    // `vite preview` 用生产构建产物(dist)本地起服务，是最接近 release 镜像的环境：
    // 同样的压缩 / 拆包 / CSS 加载顺序，可提前暴露只在生产构建出现的问题
    // （如主题变量被打包顺序覆盖）。用法：npm run build && npm run preview
    preview: {
      port: 4173,
      host: true,
      proxy: {
        '/mcp/': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
          timeout: 3_600_000,
          proxyTimeout: 3_600_000,
        },
        '/api': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
          ws: true,
          timeout: 180_000,
          proxyTimeout: 180_000,
        },
        '/files': {
          target: DEV_PROXY_TARGET,
          changeOrigin: true,
          secure: false,
        }
      }
    },
  }
})
