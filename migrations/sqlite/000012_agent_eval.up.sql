-- Mirrors versioned migration 000085_agent_eval:
-- frontend-triggered agent smoke-test runs and per-case results.

CREATE TABLE IF NOT EXISTS agent_eval_runs (
    id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'running',
    total INTEGER NOT NULL DEFAULT 0,
    passed INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    eval_trigger VARCHAR(16) NOT NULL DEFAULT 'manual',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP,
    PRIMARY KEY (id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_eval_runs_scope
    ON agent_eval_runs (tenant_id, agent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_eval_case_results (
    id VARCHAR(36) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    case_id VARCHAR(32) NOT NULL,
    case_title VARCHAR(255) NOT NULL DEFAULT '',
    query TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'failed',
    assertions TEXT,
    judge TEXT,
    tool_calls TEXT,
    answer TEXT NOT NULL DEFAULT '',
    reference_count INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_eval_cases_run
    ON agent_eval_case_results (run_id);
