-- 回滚 000139：先删索引再删列（只加列的迁移不碰表结构）。
DROP INDEX IF EXISTS idx_wi_journal;
ALTER TABLE workbench_items DROP COLUMN IF EXISTS current_mode;
ALTER TABLE workbench_items DROP COLUMN IF EXISTS journal_date;
