-- Migration: 000140_workbench_template
-- Description: 工作台文档模板（P1-12）。模板是个人资源，按 tenant_id + owner_id 隔离，
-- Admin 不可读；模板不是知识，不参与归档、检索与 AI 引用。
--
-- ydoc 存完整快照字节（不是增量 update），服务端只复制与存储、不解析。

CREATE TABLE IF NOT EXISTS workbench_templates (
  id                    varchar(36) PRIMARY KEY,
  tenant_id             bigint      NOT NULL,
  owner_id              varchar(36) NOT NULL,
  name                  varchar(60) NOT NULL,
  description           varchar(200) NOT NULL DEFAULT '',
  ydoc                  bytea       NOT NULL,
  size_bytes            integer     NOT NULL,
  editor_schema_version integer     NOT NULL DEFAULT 1,
  -- 只做溯源，不建外键、不级联：源条目删除不影响模板
  source_item_id        varchar(36),
  created_at            timestamptz NOT NULL,
  updated_at            timestamptz NOT NULL
);

-- 列表始终按“我的 + 最近更新”查，最多 50 行，不需要分页索引
CREATE INDEX IF NOT EXISTS idx_wt_owner_updated
  ON workbench_templates (tenant_id, owner_id, updated_at DESC);
