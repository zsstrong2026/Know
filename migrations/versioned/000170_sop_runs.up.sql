-- Migration: 000170_sop_runs
-- Description: SOP 工作台：运行、节点运行与产物（租约/状态/门禁结果）
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤；sop_runs/sop_artifacts 软删 deleted_at。
-- workflow_snapshot 是发布 revision 的冻结快照（运行只读它，不读活模板，spec §3.3）。
-- 租约：queued 的 run 由调度器以 CAS（UPDATE … WHERE status='queued'）抢占为 running
-- 并写 lease_until；进程崩溃后由 ExpireLeases 按 lease_until 过期回收为 failed。
-- sop_node_runs 无 deleted_at：运行历史不可变，重跑走 ResetNodes 更新状态（spec §6）。

CREATE TABLE IF NOT EXISTS sop_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    template_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    workflow_snapshot JSONB NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    item_id VARCHAR(36) NOT NULL DEFAULT '',
    parent_run_id VARCHAR(36) NOT NULL DEFAULT '',
    trigger_kind VARCHAR(16) NOT NULL DEFAULT 'manual',
    trigger_ref_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'queued',
    input JSONB NOT NULL DEFAULT '{}',
    human_context JSONB NOT NULL DEFAULT '[]',
    project_context TEXT NOT NULL DEFAULT '',
    workspace_dir VARCHAR(500) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    pause_requested BOOLEAN NOT NULL DEFAULT FALSE,
    usage JSONB NOT NULL DEFAULT '{}',
    execution_duration_ms BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_runs_status ON sop_runs(tenant_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_item ON sop_runs(item_id, created_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_project ON sop_runs(project_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_lease ON sop_runs(status, lease_until);

CREATE TABLE IF NOT EXISTS sop_node_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    run_id VARCHAR(36) NOT NULL,
    node_id VARCHAR(36) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempt INTEGER NOT NULL DEFAULT 0,
    rework_count INTEGER NOT NULL DEFAULT 0,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    feedback TEXT NOT NULL DEFAULT '',
    gate_result VARCHAR(16) NOT NULL DEFAULT '',
    gate_reason TEXT NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    resume_session_id VARCHAR(128) NOT NULL DEFAULT '',
    child_run_id VARCHAR(36) NOT NULL DEFAULT '',
    usage JSONB NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id);
CREATE INDEX IF NOT EXISTS ix_sop_node_runs_status ON sop_node_runs(run_id, status);

CREATE TABLE IF NOT EXISTS sop_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    run_id VARCHAR(36) NOT NULL,
    node_id VARCHAR(36) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1,
    name VARCHAR(500) NOT NULL,
    path VARCHAR(500) NOT NULL,
    media_type VARCHAR(100) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    sha256 VARCHAR(64) NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    ingested_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_artifacts_run ON sop_artifacts(run_id, node_id, deleted_at);
