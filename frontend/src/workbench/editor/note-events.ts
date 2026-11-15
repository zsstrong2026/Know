/**
 * 画布文档卡片与宿主页之间的轻量事件名。
 *
 * 单独成文件是为了让宿主页不必静态 import `blocksuite.ts`
 * （那份模块会拉入整个 BlockSuite 模块图，破坏按需加载）。
 */
import { signal } from '@preact/signals-core'

export const WB_NOTE_DETAIL_EVENT = 'wb-note-detail'

/**
 * 画布文档卡片的标题（来自条目本身）。
 *
 * 条目标题与 Y.Doc 里的页面标题是两份数据；画布标题栏优先读这份，
 * 保证卡片标题始终与条目页顶部一致。SignalWatcher 渲染会订阅它。
 */
export const noteCardTitle = signal('')
