import assert from 'node:assert/strict'
import { test } from 'node:test'

import JSZip from 'jszip'

import {
  BLOCKSUITE_VERSION,
  EXPORT_FORMAT,
  EXPORT_FORMAT_VERSION,
  PACKAGE_PATHS,
  assembleZip,
  assetEntryPath,
  buildManifest,
  collectBlockStats,
  packageFileName,
  sanitizeFileName,
  type ExportBlockLike,
} from './package'

test('collectBlockStats：按树统计块数，同一资产只收一次', () => {
  const root: ExportBlockLike = {
    flavour: 'affine:page',
    children: [
      {
        flavour: 'affine:note',
        children: [
          { flavour: 'affine:paragraph', props: {} },
          { flavour: 'affine:image', props: { sourceId: 'asset-1', caption: '封面' } },
          // 同一张图被两处引用：清单里只能有一条
          { flavour: 'affine:image', props: { sourceId: 'asset-1' } },
          { flavour: 'affine:attachment', props: { sourceId: 'asset-2', name: '报告.pdf' } },
        ],
      },
      {
        flavour: 'affine:surface',
        children: [
          { flavour: 'affine:image', props: {} },
          { flavour: 'affine:frame', children: [{ flavour: 'affine:note', children: [] }] },
        ],
      },
    ],
  }
  const stats = collectBlockStats(root)
  // root 自身不计入；note/surface 及其下所有块都算（含 frame 里的空 note）
  assert.equal(stats.blocks, 9)
  assert.deepEqual(
    stats.assets.map((asset) => [asset.assetId, asset.name, asset.flavour]),
    [
      ['asset-1', '封面', 'affine:image'],
      ['asset-2', '报告.pdf', 'affine:attachment'],
    ],
  )
})

test('collectBlockStats：空树与缺 sourceId 的图片都不产资产', () => {
  assert.deepEqual(collectBlockStats(null), { blocks: 0, assets: [] })
  const stats = collectBlockStats({
    children: [{ flavour: 'affine:image', props: { caption: '没有 sourceId' } }],
  })
  assert.equal(stats.blocks, 1)
  assert.deepEqual(stats.assets, [])
})

test('sanitizeFileName：挡住路径穿越与非法字符', () => {
  // 关键不是逐字相等，而是「解压时不会跑出目录」
  const traversal = sanitizeFileName('../../etc/passwd')
  assert.ok(!traversal.includes('/') && !traversal.includes('\\'), traversal)
  assert.ok(!traversal.startsWith('.'), traversal)
  assert.equal(sanitizeFileName('a/b\\c:d*e?f"g<h>i|j'), 'a-b-c-d-e-f-g-h-i-j')
  // 控制字符
  assert.equal(sanitizeFileName('line\nbreak'), 'line-break')
  // 首尾点与空格会被 Windows 吞掉
  assert.equal(sanitizeFileName('  ..名字..  '), '名字')
  assert.equal(sanitizeFileName(''), 'untitled')
  assert.equal(sanitizeFileName('   '), 'untitled')
})

test('sanitizeFileName：Windows 保留名与超长名', () => {
  assert.equal(sanitizeFileName('CON'), 'CON_')
  assert.equal(sanitizeFileName('nul.txt'), 'nul.txt_')
  assert.equal(sanitizeFileName('console'), 'console')
  assert.equal(sanitizeFileName('长'.repeat(200)).length, 80)
})

test('assetEntryPath：用 asset id 当文件名，保留扩展名', () => {
  assert.equal(assetEntryPath('b1a2c3', '封面.PNG'), 'assets/b1a2c3.png')
  assert.equal(assetEntryPath('b1a2c3', '报告.pdf'), 'assets/b1a2c3.pdf')
  assert.equal(assetEntryPath('b1a2c3', '没有扩展名'), 'assets/b1a2c3')
  // 粘贴进来的截图往往没名字，靠 mime 兜底扩展名
  assert.equal(assetEntryPath('b1a2c3', '', 'image/png'), 'assets/b1a2c3.png')
  assert.equal(assetEntryPath('b1a2c3', '封面', 'image/jpeg'), 'assets/b1a2c3.jpg')
  // 恶意名字不会影响路径
  assert.equal(assetEntryPath('b1a2c3', '../../x.sh'), 'assets/b1a2c3.sh')
})

test('buildManifest：字段齐全，缺失资产单独计数', () => {
  const manifest = buildManifest({
    item: { id: 'item-1', title: '项目看板', kind: 'canvas', mode: 'edgeless', snapshotVersion: 12 },
    schemaVersion: 3,
    stats: { blocks: 8, assets: [] },
    ydocBytes: 2048,
    markdownCharacters: 120,
    exportedAt: new Date('2026-11-06T09:00:00.000Z'),
    assets: [
      { id: 'a1', path: 'assets/a1.png', name: '封面.png', mime: 'image/png', size: 10, missing: false },
      { id: 'a2', path: 'assets/a2.pdf', name: '报告.pdf', mime: 'application/pdf', size: 0, missing: true },
    ],
  })
  assert.equal(manifest.format, EXPORT_FORMAT)
  assert.equal(manifest.format_version, EXPORT_FORMAT_VERSION)
  assert.equal(manifest.exported_at, '2026-11-06T09:00:00.000Z')
  assert.deepEqual(manifest.item, {
    id: 'item-1',
    title: '项目看板',
    kind: 'canvas',
    mode: 'edgeless',
    snapshot_version: 12,
  })
  assert.deepEqual(manifest.editor, { schema_version: 3, blocksuite: BLOCKSUITE_VERSION })
  const content = manifest.content as any
  assert.equal(content.blocks, 8)
  assert.deepEqual(content.ydoc, { path: PACKAGE_PATHS.ydoc, bytes: 2048 })
  assert.deepEqual(content.markdown, { path: PACKAGE_PATHS.markdown, characters: 120 })
  assert.deepEqual(content.assets, { total: 2, missing: 1, dir: PACKAGE_PATHS.assetsDir })
  // PNG 结论要写进清单，外部工具才不会以为漏了文件
  assert.equal((manifest.capabilities as any).png, false)
})

test('assembleZip：三个固定文件 + 资产，清单可被重新解析', async () => {
  const ydoc = new Uint8Array([1, 2, 3, 4])
  const blob = await assembleZip({
    manifest: { format: EXPORT_FORMAT, marker: 'ok' },
    ydoc,
    markdown: '# 标题\n\n正文',
    assets: [{ path: 'assets/a1.txt', data: new TextEncoder().encode('hello') }],
  })
  const zip = await JSZip.loadAsync(await blob.arrayBuffer())
  // 目录条目（assets/）是 zip 结构的一部分，不算额外文件
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir)
  assert.deepEqual(files.sort(), ['assets/a1.txt', 'content.md', 'content.ydoc', 'manifest.json'])
  const manifest = JSON.parse(await zip.file('manifest.json')!.async('string'))
  assert.equal(manifest.marker, 'ok')
  assert.equal(await zip.file('content.md')!.async('string'), '# 标题\n\n正文')
  assert.deepEqual([...await zip.file('content.ydoc')!.async('uint8array')], [1, 2, 3, 4])
  assert.equal(await zip.file('assets/a1.txt')!.async('string'), 'hello')
})

test('assembleZip：空资产数据被跳过，不影响整包', async () => {
  const blob = await assembleZip({
    manifest: {},
    ydoc: new Uint8Array([0]),
    markdown: 'x',
    assets: [
      { path: 'assets/empty.png', data: new Uint8Array() },
      { path: '', data: new Uint8Array([1]) },
    ],
  })
  const zip = await JSZip.loadAsync(await blob.arrayBuffer())
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir)
  assert.deepEqual(files.sort(), ['content.md', 'content.ydoc', 'manifest.json'])
})

test('packageFileName：标题非法时退回条目 id', () => {
  assert.equal(packageFileName('项目 / 看板', 'item-1'), '项目 - 看板-工作台导出.zip')
  assert.equal(packageFileName('   ', 'item-1'), 'item-1-工作台导出.zip')
})
