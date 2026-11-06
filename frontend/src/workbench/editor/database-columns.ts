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
} as const
