/**
 * 从文档正文抽取待办事项（「收集事项」的规则路径，不依赖 AI）。
 *
 * `projection.ts` 输出 Markdown 时把 todo 统一写成 `- [ ] `，读不到勾选状态，
 * 因此这里直接遍历 store 里的 `affine:list` 块，取 `props.type` 与 `props.checked`。
 * 遍历从 root 递归下去即可覆盖 Page 正文与画布便签（便签挂在 surface 下）。
 */
import { WORKBENCH_DB_COLUMNS } from './database-seed'
import type { ResolvedCell, ResolvedOp, ResolvedPatch } from '../ai/database-patch'

type BsStore = any

export interface CollectedTodo {
  title: string
  done: boolean
}

export interface CollectStatusValues {
  /** 状态列在库里真实的列名（建库时按当时语言写入，不能硬编码） */
  columnName: string
  /** 「待开始」选项的真实 value（随建库语言变化） */
  todo: string
  /** 「已完成」选项的真实 value */
  done: string
}

/** 抽取文档里所有非空待办，按出现顺序返回。 */
export function collectTodos(store: BsStore): CollectedTodo[] {
  const result: CollectedTodo[] = []
  const seen = new Set<string>()
  const root = store?.root
  if (!root) return result

  walk(root.children ?? [])
  return result

  function walk(blocks: any[]) {
    for (const block of blocks) {
      if (!block) continue
      if (block.flavour === 'affine:list') {
        const title = block.text?.toString?.().trim() ?? ''
        // 同一个块可能既挂在 note 下又出现在 surface 上，按块 id 去重。
        const key = block.id ?? ''
        if (String(block.props?.type ?? '') === 'todo' && title && !seen.has(key)) {
          if (key) seen.add(key)
          result.push({ title, done: Boolean(block.props?.checked) })
        }
      }
      if (block.children?.length) walk(block.children)
    }
  }
}

/**
 * 离线构造写入补丁。
 *
 * 不走 `resolvePatch`：列 id 与选项值都由调用方在事项页按真实列配置解析好，
 * 无需再做列名查找；这里只负责把待办映射成 `add_row`。
 */
export function buildCollectPatch(
  todos: CollectedTodo[],
  status: CollectStatusValues,
): ResolvedPatch {
  const ops: ResolvedOp[] = []
  for (const todo of todos) {
    const title = todo.title.trim()
    if (!title) continue
    const cells: ResolvedCell[] = [
      {
        columnId: WORKBENCH_DB_COLUMNS.status,
        columnName: status.columnName,
        type: 'select',
        value: todo.done ? status.done : status.todo,
      },
    ]
    ops.push({ kind: 'add_row', title, cells })
  }
  return { ops, overwriteCount: 0, errors: [] }
}
