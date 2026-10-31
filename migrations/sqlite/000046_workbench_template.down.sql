-- Migration: 000046_workbench_template (down)
-- 先删索引，再删表。

DROP INDEX IF EXISTS idx_wt_owner_updated;

DROP TABLE IF EXISTS workbench_templates;
