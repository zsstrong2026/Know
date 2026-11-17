-- Migration: 000160_insight
-- Description: 工作台/墨匣「洞察」模块：主题、运行、证据、报告、自我洞悉
--
-- 归属口径与工作台一致：tenant_id + owner_id，同 workspace 其他成员（含 Admin）
-- 既看不到也用不了。删除主题不级联删除报告，报告保留可读。

-- 内置主题 ID 固定（如 insight-builtin-thinking），因此主键必须带上归属，
-- 否则第二个用户种入同一内置主题会撞主键。
CREATE TABLE IF NOT EXISTS insight_themes (
  id           varchar(36) NOT NULL,
  tenant_id    bigint      NOT NULL,
  owner_id     varchar(36) NOT NULL,
  name         varchar(80) NOT NULL,
  description  text        NOT NULL DEFAULT '',
  builtin      boolean     NOT NULL DEFAULT false,
  sensitive    boolean     NOT NULL DEFAULT false,
  glyph        varchar(32) NOT NULL DEFAULT '',
  instructions text        NOT NULL DEFAULT '',
  dimensions   jsonb       NOT NULL DEFAULT '[]'::jsonb,
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL,
  deleted_at   timestamptz,
  PRIMARY KEY (tenant_id, owner_id, id)
);

CREATE INDEX IF NOT EXISTS idx_insight_themes_owner
  ON insight_themes (tenant_id, owner_id);

CREATE TABLE IF NOT EXISTS insight_runs (
  id              varchar(36) PRIMARY KEY,
  tenant_id       bigint      NOT NULL,
  owner_id        varchar(36) NOT NULL,
  theme_id        varchar(36) NOT NULL,
  status          varchar(24) NOT NULL DEFAULT 'pending',
  stage           varchar(24) NOT NULL DEFAULT '',
  progress        int         NOT NULL DEFAULT 0,
  docs_total      int         NOT NULL DEFAULT 0,
  docs_processed  int         NOT NULL DEFAULT 0,
  evidence_count  int         NOT NULL DEFAULT 0,
  options_json    jsonb,
  error           text        NOT NULL DEFAULT '',
  message         text        NOT NULL DEFAULT '',
  started_at      timestamptz,
  finished_at     timestamptz,
  created_at      timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_runs_owner_status
  ON insight_runs (tenant_id, owner_id, status);
CREATE INDEX IF NOT EXISTS idx_insight_runs_owner_theme
  ON insight_runs (tenant_id, owner_id, theme_id);

CREATE TABLE IF NOT EXISTS insight_evidence (
  id              varchar(36) PRIMARY KEY,
  tenant_id       bigint      NOT NULL,
  owner_id        varchar(36) NOT NULL,
  run_id          varchar(36) NOT NULL,
  theme_id        varchar(36) NOT NULL,
  knowledge_id    varchar(36) NOT NULL,
  knowledge_title varchar(255) NOT NULL DEFAULT '',
  chunk_index     int         NOT NULL DEFAULT 0,
  dimension       varchar(64) NOT NULL DEFAULT '',
  quote           text        NOT NULL DEFAULT '',
  evidence_type   varchar(16) NOT NULL DEFAULT 'evidence',
  confidence      varchar(8)  NOT NULL DEFAULT '中',
  excluded        boolean     NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_evidence_owner_theme
  ON insight_evidence (tenant_id, owner_id, theme_id);
CREATE INDEX IF NOT EXISTS idx_insight_evidence_owner_run
  ON insight_evidence (tenant_id, owner_id, run_id);

CREATE TABLE IF NOT EXISTS insight_reports (
  id             varchar(36) PRIMARY KEY,
  tenant_id      bigint      NOT NULL,
  owner_id       varchar(36) NOT NULL,
  theme_id       varchar(36) NOT NULL,
  run_id         varchar(36) NOT NULL,
  title          varchar(255) NOT NULL DEFAULT '',
  model_id       varchar(64) NOT NULL DEFAULT '',
  docs_covered   int         NOT NULL DEFAULT 0,
  evidence_count int         NOT NULL DEFAULT 0,
  content_json   jsonb,
  created_at     timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_reports_owner_theme
  ON insight_reports (tenant_id, owner_id, theme_id);
CREATE INDEX IF NOT EXISTS idx_insight_reports_owner_run
  ON insight_reports (tenant_id, owner_id, run_id);

CREATE TABLE IF NOT EXISTS insight_self (
  id           varchar(36) PRIMARY KEY,
  tenant_id    bigint      NOT NULL,
  owner_id     varchar(36) NOT NULL,
  status       varchar(24) NOT NULL DEFAULT 'idle',
  content_json jsonb,
  error        text        NOT NULL DEFAULT '',
  model_id     varchar(64) NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL
);

-- 同一用户只有一份自我洞察，覆盖更新，不保留历史版本
CREATE UNIQUE INDEX IF NOT EXISTS uq_insight_self_owner
  ON insight_self (tenant_id, owner_id);
