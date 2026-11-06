-- Migration: 000062_workbench_tags (down)
DROP INDEX IF EXISTS idx_wit_tag;
DROP TABLE IF EXISTS workbench_item_tags;
DROP INDEX IF EXISTS uq_wt_owner_name;
DROP TABLE IF EXISTS workbench_tags;
