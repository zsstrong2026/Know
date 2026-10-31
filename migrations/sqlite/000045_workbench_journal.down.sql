-- 回滚 000045：先删索引再删列。
DROP INDEX IF EXISTS idx_wi_journal;
ALTER TABLE workbench_items DROP COLUMN current_mode;
ALTER TABLE workbench_items DROP COLUMN journal_date;
