-- Migration: 000044_workbench_assets (down)
-- 先删索引，再删表。

DROP INDEX IF EXISTS uq_wia_item_hash;
DROP INDEX IF EXISTS idx_wia_item;

DROP TABLE IF EXISTS workbench_item_assets;
