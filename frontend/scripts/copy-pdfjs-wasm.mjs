import { copyFileSync, existsSync, mkdirSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'

/**
 * Copy the installed pdfjs-dist decoder resources into the frontend build.
 *
 * 用逐文件 copyFileSync 而不是递归 cpSync：Node 24 + Windows 上 cpSync 复制
 * pdfjs wasm 目录时会以 0xC0000409（栈缓冲溢出）静默崩溃，导致 closeBundle
 * 中断、构建产物缺少 pdfjs/wasm 且进程退出码被误报为失败。
 */
export function copyPdfJsWasm(sourceDir, outputDir) {
  const jbig2Wasm = resolve(sourceDir, 'jbig2.wasm')
  if (!existsSync(jbig2Wasm)) {
    throw new Error(`PDF.js wasm resources are missing the required decoder: ${jbig2Wasm}`)
  }

  mkdirSync(outputDir, { recursive: true })
  for (const entry of readdirSync(sourceDir, { withFileTypes: true })) {
    if (!entry.isFile()) continue
    copyFileSync(resolve(sourceDir, entry.name), resolve(outputDir, entry.name))
  }
}
