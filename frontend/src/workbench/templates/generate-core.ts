/**
 * 内置模板快照的内容与构建逻辑（§9.16.3）。
 *
 * 单独成文件是为了让 `scripts/gen-templates.mjs` 能用 Vite 的模块解析跑它：
 * BlockSuite 的源码在 third_party，直接用 Node 解析会找不到依赖。
 */
import { StoreExtensionManager } from '@blocksuite/affine/ext-loader'
import { getInternalStoreExtensions } from '@blocksuite/affine/extensions/store'
import { Text } from '@blocksuite/store'
import { TestWorkspace } from '@blocksuite/store/test'
import * as Y from 'yjs'

/**
 * store 扩展只注册一次。
 *
 * 不注册的话 workspace 的 schema 是空的，`addBlock('affine:page')` 会静默失败，
 * 最后 encode 出来是 2 字节的空 update —— 这正是第一次跑脚本得到的「成功但全空」。
 */
let storeExtensions: unknown[] | null = null
function ensureStoreExtensions(): unknown[] {
  if (!storeExtensions) {
    storeExtensions = new StoreExtensionManager(getInternalStoreExtensions()).get('store') as unknown[]
  }
  return storeExtensions
}

/** 段落/标题简写：h 表示 heading 级别，其余同理。 */
export type TemplateLine =
  | string
  | { h: 1 | 2 | 3; text: string }
  | { bullet: string }
  | { todo: string }

export const TEMPLATE_DOCS: Record<string, TemplateLine[]> = {
  'meeting-notes': [
    { h: 1, text: '会议纪要' },
    { h: 2, text: '会议信息' },
    '时间：',
    '地点：',
    '参会人：',
    '主持：',
    { h: 2, text: '议题' },
    { bullet: '议题一：' },
    { bullet: '议题二：' },
    { h: 2, text: '讨论与结论' },
    { bullet: '结论一：' },
    { bullet: '结论二：' },
    { h: 2, text: '行动项' },
    { bullet: '负责人 / 截止时间 / 交付物' },
    { bullet: '负责人 / 截止时间 / 交付物' },
    { h: 2, text: '下次会议' },
    '时间与议题待定。',
  ],
  'project-brief': [
    { h: 1, text: '项目简报' },
    { h: 2, text: '背景' },
    '为什么要做这件事，当前痛点是什么。',
    { h: 2, text: '目标' },
    { bullet: '目标一（可度量）' },
    { bullet: '目标二（可度量）' },
    { h: 2, text: '范围' },
    '包含：',
    '不包含：',
    { h: 2, text: '里程碑' },
    { bullet: '里程碑一：日期 / 交付物' },
    { bullet: '里程碑二：日期 / 交付物' },
    { h: 2, text: '风险与应对' },
    { bullet: '风险一：影响 / 应对' },
    { bullet: '风险二：影响 / 应对' },
  ],
  'weekly-plan': [
    { h: 1, text: '周计划' },
    { h: 2, text: '本周目标' },
    { bullet: '目标一' },
    { bullet: '目标二' },
    { h: 2, text: '每日安排' },
    { h: 3, text: '周一' },
    { todo: '待办' },
    { h: 3, text: '周二' },
    { todo: '待办' },
    { h: 3, text: '周三' },
    { todo: '待办' },
    { h: 3, text: '周四' },
    { todo: '待办' },
    { h: 3, text: '周五' },
    { todo: '待办' },
    { h: 2, text: '优先级' },
    'P0：',
    'P1：',
    'P2：',
    { h: 2, text: '周五复盘' },
    '完成了什么、卡在哪里、下周调整。',
  ],
  okr: [
    { h: 1, text: 'OKR' },
    { h: 2, text: '目标（O）' },
    '用一句话描述本季度最重要的方向。',
    { h: 2, text: '关键结果（KR）' },
    { bullet: 'KR1：从 X 到 Y（可度量）' },
    { bullet: 'KR2：从 X 到 Y（可度量）' },
    { bullet: 'KR3：从 X 到 Y（可度量）' },
    { h: 2, text: '信心指数' },
    '当前信心：5 / 10；理由：',
    { h: 2, text: '行动' },
    { todo: '支撑 KR1 的行动' },
    { todo: '支撑 KR2 的行动' },
    { todo: '支撑 KR3 的行动' },
  ],
  'weekly-report': [
    { h: 1, text: '周报' },
    { h: 2, text: '本周完成' },
    { bullet: '事项一：结果与影响' },
    { bullet: '事项二：结果与影响' },
    { h: 2, text: '数据' },
    { bullet: '指标一：数值 / 环比' },
    { bullet: '指标二：数值 / 环比' },
    { h: 2, text: '问题与风险' },
    { bullet: '问题一：影响 / 需要的支持' },
    { h: 2, text: '下周计划' },
    { bullet: '计划一' },
    { bullet: '计划二' },
  ],
  'interview-notes': [
    { h: 1, text: '访谈记录' },
    { h: 2, text: '受访者' },
    '姓名 / 角色 / 背景',
    { h: 2, text: '访谈背景' },
    '为什么找他、想验证什么。',
    { h: 2, text: '问题与回答' },
    { h: 3, text: '问题一' },
    '原话摘录：',
    { h: 3, text: '问题二' },
    '原话摘录：',
    { h: 2, text: '洞察' },
    { bullet: '洞察一' },
    { bullet: '洞察二' },
    { h: 2, text: '跟进' },
    { todo: '跟进事项' },
  ],
  'reading-notes': [
    { h: 1, text: '阅读笔记' },
    { h: 2, text: '书目' },
    '书名 / 作者 / 版本',
    { h: 2, text: '核心观点' },
    { bullet: '观点一' },
    { bullet: '观点二' },
    { h: 2, text: '摘录' },
    '页码与原文。',
    { h: 2, text: '我的思考' },
    '与我已有的认知哪里一致、哪里冲突。',
    { h: 2, text: '可以怎么用' },
    { todo: '落地行动' },
  ],
  'article-draft': [
    { h: 1, text: '文章草稿' },
    { h: 2, text: '导语' },
    '一句话说明读者为什么要读下去。',
    { h: 2, text: '大纲' },
    { bullet: '第一部分' },
    { bullet: '第二部分' },
    { bullet: '第三部分' },
    { h: 2, text: '正文' },
    '在这里展开。',
    { h: 2, text: '发布检查' },
    { todo: '事实与数据核对' },
    { todo: '标题与摘要打磨' },
    { todo: '配图与链接检查' },
  ],
}

/**
 * 用 BlockSuite 自己的 Store 建一份最小 Page 文档并导出快照。
 *
 * 与编辑器保存出的字节同构：page > surface + note > (标题/段落/列表/待办)。
 * 正文写入必须走 `getModelById().text`；`getBlock()` 只返回视图，没有 text。
 */
export function buildTemplateYDoc(id: string, lines: TemplateLine[]): Uint8Array {
  const workspace = new TestWorkspace({ id })
  workspace.storeExtensions = ensureStoreExtensions() as never[]
  workspace.meta.initialize()
  const doc = workspace.createDoc(id)
  const store = doc.getStore()

  const writeText = (blockId: string, text: string) => {
    const model = store.getModelById(blockId) as { text?: { insert?: (t: string, i: number) => void } }
    model?.text?.insert?.(text, 0)
  }

  store.load(() => {
    const rootId = store.addBlock('affine:page', { title: new Text() })
    store.addBlock('affine:surface', {}, rootId)
    const noteId = store.addBlock('affine:note', {}, rootId)
    for (const line of lines) {
      if (typeof line === 'string') {
        writeText(store.addBlock('affine:paragraph', {}, noteId), line)
      } else if ('h' in line) {
        writeText(store.addBlock('affine:paragraph', { type: `h${line.h}` }, noteId), line.text)
      } else if ('bullet' in line) {
        writeText(store.addBlock('affine:paragraph', { type: 'bulleted-list' }, noteId), line.bullet)
      } else {
        writeText(store.addBlock('affine:paragraph', { type: 'todo', checked: false }, noteId), line.todo)
      }
    }
  })

  return Y.encodeStateAsUpdate(doc.spaceDoc)
}
