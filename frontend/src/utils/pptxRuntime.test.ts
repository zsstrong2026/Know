import assert from 'node:assert/strict'
import test from 'node:test'
import { loadConfigFromFile } from 'vite'
import { build } from 'esbuild'
import { fileURLToPath } from 'node:url'

test('PPTX preview bundles the ESM renderer with one shared Vue runtime', async () => {
  const root = fileURLToPath(new URL('../../', import.meta.url))
  const loaded = await loadConfigFromFile({ command: 'serve', mode: 'development' }, `${root}vite.config.ts`)
  // vite 的 alias 既可能是对象也可能是数组，这里统一按数组查找
  const aliases = loaded!.config.resolve!.alias as Array<{ find: string | RegExp; replacement: string }>
  const entry = aliases.find((item) => item.find === '@vue-office/pptx')!.replacement
  // 兼容 Windows 反斜杠路径
  assert.match(entry, /[/\\]v3[/\\]vue-office-pptx\.mjs$/)
  const result = await build({
    stdin: { contents: `export { createApp } from 'vue'; export { default as Pptx } from ${JSON.stringify(entry)}`,
      resolveDir: root },
    bundle: true, write: false, metafile: true, format: 'esm', platform: 'browser',
  })
  // Mixing require('vue') from the UMD renderer with the app's ESM import
  // previously detached template refs and threw during the mounted hook.
  const inputs = Object.keys(result.metafile!.inputs)
  assert.equal(inputs.filter(path => path.includes('@vue/runtime-core/dist/')).length, 1)
  assert.ok(!inputs.some(path => /vue\/(?:index\.js|dist\/vue\.cjs)/.test(path)))
})
