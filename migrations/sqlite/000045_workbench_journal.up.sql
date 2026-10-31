-- Journals 与双形态（P1-9，SQLite）
--
-- 与 PG 版逐列一致，只有类型不同：date → TEXT（存 YYYY-MM-DD）。
-- SQLite 不支持 ADD COLUMN IF NOT EXISTS，本文件按“只执行一次”编写。
ALTER TABLE workbench_items ADD COLUMN journal_date TEXT;
ALTER TABLE workbench_items ADD COLUMN current_mode TEXT;

-- 部分索引条件与 PG 版照抄；ISO 日期的字典序等于时间序，不需要 strftime。
CREATE INDEX IF NOT EXISTS idx_wi_journal
  ON workbench_items (tenant_id, owner_id, journal_date)
  WHERE journal_date IS NOT NULL AND deleted_at IS NULL;
