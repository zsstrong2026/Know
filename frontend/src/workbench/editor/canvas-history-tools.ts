/**
 * 原生无限画布工具栏的撤销 / 重做按钮。
 *
 * 上游工具栏没有历史入口，这里用 QuickTool 扩展把两个按钮追加到快速工具组末尾：
 * 与原生按钮同容器、同样式、同一套窄屏折叠逻辑，不再另建悬浮工具条。
 * 点击直接走上游 `store.undo/redo`（内部是 Y.UndoManager），不自建事务栈。
 */
import { QuickToolExtension } from '@blocksuite/affine-widget-edgeless-toolbar'
import { UndoIcon, RedoIcon } from '@blocksuite/icons/lit'
import { html } from 'lit'
import i18n from '@/i18n'

/** 历史工具排在快速工具组最后（link 的优先级为 0），仍紧邻原生按钮。 */
const PRIORITY = { undo: -10, redo: -11 } as const

function historyQuickTool(kind: 'undo' | 'redo'): any {
  const icon = kind === 'undo' ? UndoIcon : RedoIcon
  return QuickToolExtension(`workbench-${kind}`, ({ block }: { block: any }) => ({
    priority: PRIORITY[kind],
    content: html`<icon-button .size=${36} @click=${() => block.store[kind]()}>
      ${icon({ width: '20px', height: '20px' })}
      <affine-tooltip tip-position="top">
        ${i18n.global.t(`workbench.canvasHistory.${kind}`)}
      </affine-tooltip>
    </icon-button>`,
  }))
}

export const canvasHistoryQuickTools: any[] = [
  historyQuickTool('undo'),
  historyQuickTool('redo'),
]
