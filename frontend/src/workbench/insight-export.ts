/**
 * 把 Markdown（洞察报告 / 自我说明书）存成墨匣文档。
 *
 * 服务端只存 Y.Doc 快照，Markdown → Y.Doc 必须在前端完成：
 * 沿用归档同步的「离屏会话」写法 —— 先建条目，再用 mountEditor 的 seed
 * 把段落写进空文档，最后立刻落盘并销毁编辑器。
 */
import {
  deleteWorkbenchItem,
  getWorkbenchContent,
  saveWorkbenchContent,
} from '@/api/workbench'
import { useWorkbenchStore } from '@/stores/workbench'
import { base64ToBytes, bytesToBase64 } from '@/workbench/editor/use-editor-session'
import { markdownToBlockSpecs, type MarkdownBlockSpec } from '@/workbench/markdown-blocks'
import { currentClientId } from '@/workbench/save-queue'

type BsStore = any

/**
 * 保存 Markdown 为墨匣文档，返回新条目。
 *
 * 失败时删除刚建的条目：否则用户会得到一个标题正确、正文为空的文档。
 */
export async function saveMarkdownAsWorkbenchDoc(options: {
  title: string
  markdown: string
}): Promise<{ itemId: string; title: string }> {
  const title = options.title.trim().slice(0, 80) || '洞察报告'
  const specs = markdownToBlockSpecs(options.markdown)
  // 用 store 建条目：列表会立即出现新文档，不需要刷新侧栏。
  const store = useWorkbenchStore()
  const item = await store.createItem('canvas', title, null, 'page')
  const content = await getWorkbenchContent(item.id)

  const host = document.createElement('div')
  // 必须离开视口但仍参与布局：BlockSuite 的渲染依赖尺寸探测。
  host.style.cssText = 'position:fixed;left:-20000px;top:0;width:1000px;height:800px;'
  document.body.appendChild(host)

  try {
    const { mountEditor } = await import('@/workbench/editor/blocksuite')
    const handle = await mountEditor({
      element: host,
      itemId: item.id,
      kind: 'canvas',
      mode: 'page',
      snapshot: content.ydoc ? base64ToBytes(content.ydoc) : null,
      editable: true,
      seed: ({ store }) => seedMarkdown(store, specs),
    })
    try {
      await saveWorkbenchContent(item.id, {
        base_version: content.snapshot_version,
        editor_schema_version: content.editor_schema_version,
        ydoc: bytesToBase64(handle.getSnapshot()),
        text_content: options.markdown,
        client_id: currentClientId(),
      })
      return { itemId: item.id, title }
    } finally {
      handle.destroy()
    }
  } catch (error) {
    await deleteWorkbenchItem(item.id).catch(() => undefined)
    throw error
  } finally {
    host.remove()
  }
}

/** 把块规格写进空白文档；首块复用初始空段落，避免正文开头多一行空行。 */
function seedMarkdown(store: BsStore, specs: MarkdownBlockSpec[]): void {
  const noteId = store.root?.children?.find(
    (child: any) => child.flavour === 'affine:note',
  )?.id
  if (!noteId) return

  const first = store.getModelById(noteId)?.children?.[0]
  const blankParagraphId =
    first?.flavour === 'affine:paragraph' && !String(first.text?.toString() ?? '').trim()
      ? first.id
      : ''
  let reusedBlank = false

  for (const spec of specs) {
    if (!reusedBlank && blankParagraphId && spec.flavour === 'affine:paragraph') {
      store.updateBlock(blankParagraphId, { type: spec.props?.type ?? 'text' })
      if (spec.text) store.getModelById(blankParagraphId)?.text?.insert(spec.text, 0)
      reusedBlank = true
      continue
    }
    const id = store.addBlock(spec.flavour, spec.props ?? {}, noteId)
    if (spec.text) store.getModelById(id)?.text?.insert(spec.text, 0)
  }
}
