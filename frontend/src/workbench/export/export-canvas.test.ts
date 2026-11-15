import assert from 'node:assert/strict'
import { test } from 'node:test'

import JSZip from 'jszip'

import { buildCanvasPackage, type CanvasExportItem, type CanvasExportSource } from './export-canvas'

const item: CanvasExportItem = {
  id: 'item-1',
  title: '项目 / 看板',
  kind: 'canvas',
  mode: 'edgeless',
  snapshotVersion: 7,
}

/** 造一棵带 3 个资产的块树；blobs 决定哪些能取到（null = 已被删除）。 */
function makeSource(blobs: Record<string, Blob | null>): CanvasExportSource {
  const block = (sourceId: string, name: string) => ({
    flavour: 'affine:image',
    props: { sourceId, caption: name },
  })
  return {
    root: {
      flavour: 'affine:page',
      children: [
        {
          flavour: 'affine:note',
          children: [block('a1', '封面.png'), block('a2', '截图.jpg'), block('a3', '')],
        },
      ],
    },
    snapshot: new Uint8Array([9, 8, 7]),
    markdown: '# 项目看板',
    schemaVersion: 3,
    resolveAsset: async (assetId: string) => blobs[assetId] ?? null,
  }
}

test('buildCanvasPackage：缺资产只记 missing，整包照常产出', async () => {
  const result = await buildCanvasPackage(item, makeSource({
    a1: new Blob([new Uint8Array([1, 2])], { type: 'image/png' }),
    a2: null,
    a3: new Blob([new Uint8Array([3])], { type: 'image/jpeg' }),
  }))
  assert.equal(result.blocks, 4) // note + 3 image
  assert.equal(result.assetCount, 3)
  assert.equal(result.missingCount, 1)
  assert.equal(result.fileName, '项目 - 看板-墨匣导出.zip')

  const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir).sort()
  assert.deepEqual(files, ['assets/a1.png', 'assets/a3.jpg', 'content.md', 'content.ydoc', 'manifest.json'])

  const manifest = JSON.parse(await zip.file('manifest.json')!.async('string'))
  assert.equal(manifest.item.id, 'item-1')
  assert.equal(manifest.editor.schema_version, 3)
  assert.equal(manifest.content.blocks, 4)
  assert.equal(manifest.content.assets.total, 3)
  assert.equal(manifest.content.assets.missing, 1)
  // 清单里保留缺失项，外部工具才知道“少的是哪一个”
  assert.deepEqual(
    manifest.assets.map((asset: any) => [asset.id, asset.missing]),
    [['a1', false], ['a2', true], ['a3', false]],
  )
})

test('buildCanvasPackage：资产顺序与块树顺序一致（并发不改变顺序）', async () => {
  const ids = ['b1', 'b2', 'b3', 'b4', 'b5']
  const root = {
    flavour: 'affine:page',
    children: ids.map((id) => ({ flavour: 'affine:attachment', props: { sourceId: id, name: `${id}.pdf` } })),
  }
  // 故意让后面的先返回，验证并发实现不会把顺序打乱
  const delays: Record<string, number> = { b1: 30, b2: 20, b3: 10, b4: 5, b5: 0 }
  const result = await buildCanvasPackage(
    item,
    {
      root,
      snapshot: new Uint8Array([1]),
      markdown: 'x',
      schemaVersion: 1,
      resolveAsset: async (assetId: string) => {
        await new Promise((resolve) => setTimeout(resolve, delays[assetId] ?? 0))
        return new Blob([new Uint8Array([1])], { type: 'application/pdf' })
      },
    },
  )
  const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
  const manifest = JSON.parse(await zip.file('manifest.json')!.async('string'))
  assert.deepEqual(
    manifest.assets.map((asset: any) => asset.id),
    ids,
  )
})

test('buildCanvasPackage：全部资产取不到时仍产出快照与正文', async () => {
  const result = await buildCanvasPackage(item, makeSource({ a1: null, a2: null, a3: null }))
  assert.equal(result.missingCount, 3)
  const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir).sort()
  assert.deepEqual(files, ['content.md', 'content.ydoc', 'manifest.json'])
})
