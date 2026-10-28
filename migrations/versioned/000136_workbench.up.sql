-- Migration: 000136_workbench
-- Description: AFFiNE × Know 工作台基础表：知识库系统/私有标记 + 工作台条目的主数据
-- 本次只建表与索引，不含 000139（Journals 双形态）与 000140（文档模板）的增量。

-- 1) 知识库：系统 KB 与私有可见性
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS is_system boolean NOT NULL DEFAULT false;
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS system_key varchar(64);
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS visibility varchar(16) NOT NULL DEFAULT 'workspace';

-- 同一用户在同一租户下最多一个未删除的工作知识库（system_key = 'work'）
CREATE UNIQUE INDEX IF NOT EXISTS uq_kb_user_system
  ON knowledge_bases (tenant_id, creator_id, system_key)
  WHERE system_key = 'work' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_kb_visibility
  ON knowledge_bases (tenant_id, visibility, creator_id);

-- 2) 工作台条目：canvas | database
CREATE TABLE IF NOT EXISTS workbench_items (
  id               varchar(36) PRIMARY KEY,
  tenant_id        bigint      NOT NULL,
  owner_id         varchar(36) NOT NULL,
  kind             varchar(16) NOT NULL,
  title            text        NOT NULL,
  icon             text,
  folder_id        varchar(36),
  ydoc             bytea,
  text_content     text,
  snapshot_version integer     NOT NULL DEFAULT 0,
  editor_schema_version integer NOT NULL DEFAULT 1,
  visibility       varchar(16) NOT NULL DEFAULT 'private',
  is_favorite      boolean     NOT NULL DEFAULT false,
  last_opened_at   timestamptz,
  created_at       timestamptz NOT NULL,
  updated_at       timestamptz NOT NULL,
  deleted_at       timestamptz
);

CREATE INDEX IF NOT EXISTS idx_wi_owner_updated
  ON workbench_items (tenant_id, owner_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_wi_owner_kind_updated
  ON workbench_items (tenant_id, owner_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_wi_folder
  ON workbench_items (tenant_id, owner_id, folder_id);
CREATE INDEX IF NOT EXISTS idx_wi_favorite
  ON workbench_items (tenant_id, owner_id, is_favorite)
  WHERE is_favorite AND deleted_at IS NULL;

-- 3) 条目版本：显式快照 / 定时检查点
CREATE TABLE IF NOT EXISTS workbench_item_versions (
  id           varchar(36) PRIMARY KEY,
  item_id      varchar(36) NOT NULL,
  tenant_id    bigint      NOT NULL,
  owner_id     varchar(36) NOT NULL,
  version      integer     NOT NULL,
  editor_schema_version integer NOT NULL DEFAULT 1,
  ydoc         bytea,
  text_content text,
  created_by   varchar(36),
  created_at   timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wiv_item_version
  ON workbench_item_versions (item_id, version);
CREATE INDEX IF NOT EXISTS idx_wiv_item_created
  ON workbench_item_versions (item_id, created_at DESC);

-- 4) 文件夹
CREATE TABLE IF NOT EXISTS workbench_folders (
  id         varchar(36) PRIMARY KEY,
  tenant_id  bigint      NOT NULL,
  owner_id   varchar(36) NOT NULL,
  name       text        NOT NULL,
  parent_id  varchar(36),
  sort_order integer     NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_wf_owner_parent
  ON workbench_folders (tenant_id, owner_id, parent_id, sort_order);

-- 5) 条目引用：回链到条目/文档/知识（P4 使用）
CREATE TABLE IF NOT EXISTS workbench_item_links (
  id              varchar(36) PRIMARY KEY,
  tenant_id       bigint      NOT NULL,
  owner_id        varchar(36) NOT NULL,
  source_item_id  varchar(36) NOT NULL,
  target_type     varchar(16) NOT NULL, -- item | doc | knowledge
  target_id       varchar(36) NOT NULL,
  source_block_id varchar(64),
  created_at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_wil_source
  ON workbench_item_links (tenant_id, source_item_id);
CREATE INDEX IF NOT EXISTS idx_wil_target
  ON workbench_item_links (tenant_id, target_type, target_id);

-- 6) 归档映射：每个 source part 只保留一条活跃行
CREATE TABLE IF NOT EXISTS workbench_archives (
  id             varchar(36) PRIMARY KEY,
  tenant_id      bigint      NOT NULL,
  owner_id       varchar(36) NOT NULL,
  kb_id          varchar(36) NOT NULL,
  knowledge_id   varchar(36) NOT NULL,
  source_type    varchar(16) NOT NULL, -- doc | item
  source_id      varchar(36) NOT NULL,
  source_version integer     NOT NULL,
  part_no        integer     NOT NULL DEFAULT 1,
  part_count     integer     NOT NULL DEFAULT 1,
  title          text        NOT NULL,
  status         varchar(16) NOT NULL DEFAULT 'active', -- pending | active | deleted
  archived_at    timestamptz NOT NULL,
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wa_live_part
  ON workbench_archives (tenant_id, owner_id, source_type, source_id, part_no)
  WHERE status <> 'deleted';
CREATE INDEX IF NOT EXISTS idx_wa_source
  ON workbench_archives (tenant_id, owner_id, source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_wa_knowledge
  ON workbench_archives (knowledge_id);

-- 7) 孤儿：成员离开后的 30 天保留标记
CREATE TABLE IF NOT EXISTS workbench_orphans (
  id           varchar(36) PRIMARY KEY,
  tenant_id    bigint      NOT NULL,
  user_id      varchar(36) NOT NULL,
  reason       varchar(32) NOT NULL, -- left | removed | account_deleted
  suspended_at timestamptz NOT NULL,
  purge_after  timestamptz NOT NULL,
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wo_tenant_user
  ON workbench_orphans (tenant_id, user_id);
CREATE INDEX IF NOT EXISTS idx_wo_purge
  ON workbench_orphans (purge_after);

-- 8) 通知：用户维度，来源租户写在 payload 里；不含正文
CREATE TABLE IF NOT EXISTS workbench_notices (
  id         varchar(36) PRIMARY KEY,
  user_id    varchar(36) NOT NULL,
  tenant_id  bigint      NOT NULL, -- origin tenant
  kind       varchar(32) NOT NULL, -- membership_removed | membership_restored | cleanup_warning | archive_failed
  dedupe_key varchar(128) NOT NULL DEFAULT '', -- 同一事件只提醒一次
  payload    jsonb       NOT NULL DEFAULT '{}',
  read_at    timestamptz,
  created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_wn_user_unread
  ON workbench_notices (user_id, read_at, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wn_dedupe
  ON workbench_notices (user_id, dedupe_key)
  WHERE dedupe_key <> '';
