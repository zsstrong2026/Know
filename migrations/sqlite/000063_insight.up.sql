-- Migration: 000063_insight（PostgreSQL 000160 的 SQLite 对应实现）
-- 类型映射：timestamptz→DATETIME、jsonb→TEXT、boolean→INTEGER、bigint→INTEGER。

-- 内置主题 ID 固定（如 insight-builtin-thinking），主键带上归属避免跨用户撞键。
CREATE TABLE IF NOT EXISTS insight_themes (
  id           VARCHAR(36) NOT NULL,
  tenant_id    INTEGER     NOT NULL,
  owner_id     VARCHAR(36) NOT NULL,
  name         VARCHAR(80) NOT NULL,
  description  TEXT        NOT NULL DEFAULT '',
  builtin      INTEGER     NOT NULL DEFAULT 0,
  sensitive    INTEGER     NOT NULL DEFAULT 0,
  glyph        VARCHAR(32) NOT NULL DEFAULT '',
  instructions TEXT        NOT NULL DEFAULT '',
  dimensions   TEXT        NOT NULL DEFAULT '[]',
  created_at   DATETIME    NOT NULL,
  updated_at   DATETIME    NOT NULL,
  deleted_at   DATETIME,
  PRIMARY KEY (tenant_id, owner_id, id)
);

CREATE INDEX IF NOT EXISTS idx_insight_themes_owner
  ON insight_themes (tenant_id, owner_id);

CREATE TABLE IF NOT EXISTS insight_runs (
  id              VARCHAR(36) PRIMARY KEY,
  tenant_id       INTEGER     NOT NULL,
  owner_id        VARCHAR(36) NOT NULL,
  theme_id        VARCHAR(36) NOT NULL,
  status          VARCHAR(24) NOT NULL DEFAULT 'pending',
  stage           VARCHAR(24) NOT NULL DEFAULT '',
  progress        INTEGER     NOT NULL DEFAULT 0,
  docs_total      INTEGER     NOT NULL DEFAULT 0,
  docs_processed  INTEGER     NOT NULL DEFAULT 0,
  evidence_count  INTEGER     NOT NULL DEFAULT 0,
  options_json    TEXT,
  error           TEXT        NOT NULL DEFAULT '',
  message         TEXT        NOT NULL DEFAULT '',
  started_at      DATETIME,
  finished_at     DATETIME,
  created_at      DATETIME    NOT NULL,
  updated_at      DATETIME    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_runs_owner_status
  ON insight_runs (tenant_id, owner_id, status);
CREATE INDEX IF NOT EXISTS idx_insight_runs_owner_theme
  ON insight_runs (tenant_id, owner_id, theme_id);

CREATE TABLE IF NOT EXISTS insight_evidence (
  id              VARCHAR(36) PRIMARY KEY,
  tenant_id       INTEGER     NOT NULL,
  owner_id        VARCHAR(36) NOT NULL,
  run_id          VARCHAR(36) NOT NULL,
  theme_id        VARCHAR(36) NOT NULL,
  knowledge_id    VARCHAR(36) NOT NULL,
  knowledge_title VARCHAR(255) NOT NULL DEFAULT '',
  chunk_index     INTEGER     NOT NULL DEFAULT 0,
  dimension       VARCHAR(64) NOT NULL DEFAULT '',
  quote           TEXT        NOT NULL DEFAULT '',
  evidence_type   VARCHAR(16) NOT NULL DEFAULT 'evidence',
  confidence      VARCHAR(8)  NOT NULL DEFAULT '中',
  excluded        INTEGER     NOT NULL DEFAULT 0,
  created_at      DATETIME    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_evidence_owner_theme
  ON insight_evidence (tenant_id, owner_id, theme_id);
CREATE INDEX IF NOT EXISTS idx_insight_evidence_owner_run
  ON insight_evidence (tenant_id, owner_id, run_id);

CREATE TABLE IF NOT EXISTS insight_reports (
  id             VARCHAR(36) PRIMARY KEY,
  tenant_id      INTEGER     NOT NULL,
  owner_id       VARCHAR(36) NOT NULL,
  theme_id       VARCHAR(36) NOT NULL,
  run_id         VARCHAR(36) NOT NULL,
  title          VARCHAR(255) NOT NULL DEFAULT '',
  model_id       VARCHAR(64) NOT NULL DEFAULT '',
  docs_covered   INTEGER     NOT NULL DEFAULT 0,
  evidence_count INTEGER     NOT NULL DEFAULT 0,
  content_json   TEXT,
  created_at     DATETIME    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_reports_owner_theme
  ON insight_reports (tenant_id, owner_id, theme_id);
CREATE INDEX IF NOT EXISTS idx_insight_reports_owner_run
  ON insight_reports (tenant_id, owner_id, run_id);

CREATE TABLE IF NOT EXISTS insight_self (
  id           VARCHAR(36) PRIMARY KEY,
  tenant_id    INTEGER     NOT NULL,
  owner_id     VARCHAR(36) NOT NULL,
  status       VARCHAR(24) NOT NULL DEFAULT 'idle',
  content_json TEXT,
  error        TEXT        NOT NULL DEFAULT '',
  model_id     VARCHAR(64) NOT NULL DEFAULT '',
  created_at   DATETIME    NOT NULL,
  updated_at   DATETIME    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_insight_self_owner
  ON insight_self (tenant_id, owner_id);
