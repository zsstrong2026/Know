-- Migration: 000046_workbench_template
-- Description: 对齐 versioned 000140：工作台文档模板（P1-12）。
-- 只有类型不同：bytea → BLOB、timestamptz → DATETIME。

CREATE TABLE IF NOT EXISTS workbench_templates (
  id                    VARCHAR(36)  PRIMARY KEY,
  tenant_id             INTEGER      NOT NULL,
  owner_id              VARCHAR(36)  NOT NULL,
  name                  VARCHAR(60)  NOT NULL,
  description           VARCHAR(200) NOT NULL DEFAULT '',
  ydoc                  BLOB         NOT NULL,
  size_bytes            INTEGER      NOT NULL,
  editor_schema_version INTEGER      NOT NULL DEFAULT 1,
  -- 只做溯源，不建外键、不级联：源条目删除不影响模板
  source_item_id        VARCHAR(36),
  created_at            DATETIME     NOT NULL,
  updated_at            DATETIME     NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_wt_owner_updated
  ON workbench_templates (tenant_id, owner_id, updated_at DESC);
