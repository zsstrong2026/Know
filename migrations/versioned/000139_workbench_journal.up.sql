-- Journals 与双形态：日期与形态都挂在 workbench_items 上（P1-9）
--
-- 两列都可空，且不回填存量行：
--   journal_date IS NULL  = 不是 Journal（常态）
--   current_mode IS NULL  = 未显式设置，读取时按 kind 兜底为 edgeless
-- journal_date 用 date 而不是 timestamptz：它表示本地日历日，没有时刻与时区，
-- 用带时区类型会在东八区晚上把“今天”存成昨天。
ALTER TABLE workbench_items ADD COLUMN IF NOT EXISTS journal_date date;
ALTER TABLE workbench_items ADD COLUMN IF NOT EXISTS current_mode varchar(16);

-- 月视图只查“钉了日期且未删除”的行，部分索引避免把普通画布也纳入。
CREATE INDEX IF NOT EXISTS idx_wi_journal
  ON workbench_items (tenant_id, owner_id, journal_date)
  WHERE journal_date IS NOT NULL AND deleted_at IS NULL;
