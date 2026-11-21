-- Migration: 000073_sop_cli_trigger（PostgreSQL 000171 的 SQLite 对应实现）
-- 类型映射：JSONB→JSON、TIMESTAMPTZ→DATETIME、NOW()→CURRENT_TIMESTAMP、
-- BOOLEAN→BOOLEAN（默认写 0/1，照 000069）、BIGINT→INTEGER。
-- 编号 000073 在 T5 的 000072/000074 之间补齐（迁移按文件名顺序执行，最高号仍为 74）。
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤。
-- api_key 由模型 BeforeSave 以 AES-256-GCM 加密后写入，AfterFind 宽松解密
-- （兼容历史明文；密钥轮换后单行降级为空而不是拖垮整表读取）。
-- 凭据唯一性按 (owner_user_id, cli_kind) 收口（与 PostgreSQL 000171 同构）。
-- 触发配置每项目唯一（软删行不占位，重建配置不受已删行影响）。
-- sop_dispatch_reservations 是派发幂等的旁挂表：同一 (config, item, slot) 只能插入一次，
-- 重复派发在唯一索引上冲突即返回「本槽已派发」。
-- sop_trigger_logs 只增不改：触发历史不可变。

CREATE TABLE IF NOT EXISTS sop_cli_credentials (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL,
    cli_kind VARCHAR(32) NOT NULL,
    api_key TEXT NOT NULL DEFAULT '',
    base_url VARCHAR(500) NOT NULL DEFAULT '',
    default_model VARCHAR(200) NOT NULL DEFAULT '',
    extra JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_cli_cred_owner_kind ON sop_cli_credentials(owner_user_id, cli_kind) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sop_trigger_configs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'interval',
    interval_key VARCHAR(16) NOT NULL DEFAULT 'daily9',
    cron VARCHAR(100) NOT NULL DEFAULT '',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    default_template_id VARCHAR(36) NOT NULL DEFAULT '',
    prefer_item_template BOOLEAN NOT NULL DEFAULT 1,
    sources VARCHAR(128) NOT NULL DEFAULT 'all',
    statuses JSON NOT NULL DEFAULT '["backlog","todo"]',
    per_run_limit INTEGER NOT NULL DEFAULT 10,
    enabled BOOLEAN NOT NULL DEFAULT 0,
    last_run_at DATETIME,
    next_run_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_trigger_project ON sop_trigger_configs(project_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sop_trigger_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    config_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'auto',
    scanned INTEGER NOT NULL DEFAULT 0,
    fired INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    note TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS ix_sop_trigger_logs_config ON sop_trigger_logs(config_id, created_at);

CREATE TABLE IF NOT EXISTS sop_dispatch_reservations (
    id VARCHAR(36) PRIMARY KEY,
    config_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    slot DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_dispatch_slot ON sop_dispatch_reservations(config_id, item_id, slot);
