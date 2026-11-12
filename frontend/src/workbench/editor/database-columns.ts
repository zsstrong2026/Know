/**
 * 事项表的列 id（稳定契约）。
 *
 * 单独成文件而不是放在 `database-seed.ts` 里：seed 会静态引入
 * `@blocksuite/affine/blocks/database`，而纯逻辑单测（`tsx --test`）跑不了这个包。
 * 把「只有六个字符串」的常量拆出来，视图默认值、装饰层、AI 补丁都能在测试里引用同一份定义，
 * 不必各自硬编码字面量。
 *
 * **不要改这些值**：它们已经写进用户已有条目的 Y.Doc，改了等于换一套列。
 */
export const WORKBENCH_DB_COLUMNS = {
  title: 'wb-col-title',
  status: 'wb-col-status',
  date: 'wb-col-date',
  owner: 'wb-col-owner',
  priority: 'wb-col-priority',
  relation: 'wb-col-relation',
  /**
   * 来源：手动 / AI 识别 / 文档归档（选项在 seed 与补列时写入）。
   *
   * 刻意不复用 `wb-col-source`：那个 id 属于旧原型误加的「需求来源」列，
   * 会被 `removeObsoleteWorkbenchColumns` 清掉，复用会把自己的列删掉。
   */
  source: 'wb-col-origin',
  /** 来源文档：抽取自哪篇文档（条目名快照，便于筛选与追溯） */
  sourceDoc: 'wb-col-origin-doc',
} as const

/** 来源列的选项 id（稳定契约：写入行时按 id 反查真实 value）。 */
export const WORKBENCH_SOURCE_OPTIONS = {
  manual: 'wb-src-manual',
  ai: 'wb-src-ai',
  archive: 'wb-src-archive',
} as const

/** 后加的两列：老条目需要幂等补齐，且不参与原型六列的宽度归一。 */
export const WORKBENCH_DB_SOURCE_COLUMNS = [
  WORKBENCH_DB_COLUMNS.source,
  WORKBENCH_DB_COLUMNS.sourceDoc,
] as const
