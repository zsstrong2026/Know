/**
 * 从文档正文抽取事项（规则路径，不依赖 AI）。
 *
 * `projection.ts` 输出 Markdown 时把 todo 统一写成 `- [ ] `，读不到勾选状态，
 * 因此这里直接遍历 store 里的 `affine:list` 块，取 `props.type` 与 `props.checked`。
 * 遍历从 root 递归下去即可覆盖 Page 正文与画布便签（便签挂在 surface 下）。
 *
 * 规则路径只提供「标题 + 是否已完成」；负责人/截止日等字段由 AI 路径补，
 * 两条路径最终都编译成同一个 `ItemCandidate`。
 */
import type { ItemCandidate } from '../ai/item-candidates'

type BsStore = any

export interface CollectedTodo {
  title: string
  done: boolean
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

/** 待办 → 事项候选；已完成的待办直接带上「已完成」状态。 */
export function todosToCandidates(
  todos: CollectedTodo[],
  status: { todo: string; done: string },
): ItemCandidate[] {
  return todos
    .filter((todo) => todo.title.trim())
    .map((todo) => ({
      title: todo.title,
      status: todo.done ? status.done : status.todo,
    }))
}
