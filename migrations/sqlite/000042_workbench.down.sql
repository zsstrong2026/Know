-- Migration: 000042_workbench (down)
-- 回滚顺序：先删索引，再按依赖倒序删表，最后移除 knowledge_bases 上新增的三列。

DROP INDEX IF EXISTS uq_wn_dedupe;
DROP INDEX IF EXISTS idx_wn_user_unread;
DROP INDEX IF EXISTS idx_wo_purge;
DROP INDEX IF EXISTS uq_wo_tenant_user;
DROP INDEX IF EXISTS idx_wa_knowledge;
DROP INDEX IF EXISTS idx_wa_source;
DROP INDEX IF EXISTS uq_wa_live_part;
DROP INDEX IF EXISTS idx_wil_target;
DROP INDEX IF EXISTS idx_wil_source;
DROP INDEX IF EXISTS idx_wf_owner_parent;
DROP INDEX IF EXISTS idx_wiv_item_created;
DROP INDEX IF EXISTS uq_wiv_item_version;
DROP INDEX IF EXISTS idx_wi_favorite;
DROP INDEX IF EXISTS idx_wi_folder;
DROP INDEX IF EXISTS idx_wi_owner_kind_updated;
DROP INDEX IF EXISTS idx_wi_owner_updated;
DROP INDEX IF EXISTS idx_kb_visibility;
DROP INDEX IF EXISTS uq_kb_user_system;

DROP TABLE IF EXISTS workbench_notices;
DROP TABLE IF EXISTS workbench_orphans;
DROP TABLE IF EXISTS workbench_archives;
DROP TABLE IF EXISTS workbench_item_links;
DROP TABLE IF EXISTS workbench_folders;
DROP TABLE IF EXISTS workbench_item_versions;
DROP TABLE IF EXISTS workbench_items;

ALTER TABLE knowledge_bases DROP COLUMN visibility;
ALTER TABLE knowledge_bases DROP COLUMN system_key;
ALTER TABLE knowledge_bases DROP COLUMN is_system;
