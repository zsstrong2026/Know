-- Migration 000091: frontend-triggered agent smoke tests.
--
-- One row per smoke-test run in agent_eval_runs; each of the six preset
-- cases produces one agent_eval_case_results row with its deterministic
-- assertions, AI judge output (display-only) and captured tool calls.
--
-- Runs are keyed by (id, tenant_id) like custom_agents; case rows carry
-- the owning tenant for direct scoping. Retention (50 runs per agent) is
-- enforced by the repository, not here.

CREATE TABLE IF NOT EXISTS agent_eval_runs (
    id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    -- running | completed | failed
    status VARCHAR(16) NOT NULL DEFAULT 'running',
    total INTEGER NOT NULL DEFAULT 0,
    passed INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    -- Run-level error when the run aborted mid-way; NULL on success.
    error TEXT,
    -- Who started it. Reserved word, hence eval_trigger.
    eval_trigger VARCHAR(16) NOT NULL DEFAULT 'manual',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_eval_runs_scope
    ON agent_eval_runs (tenant_id, agent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_eval_case_results (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    case_id VARCHAR(32) NOT NULL,
    case_title VARCHAR(255) NOT NULL DEFAULT '',
    query TEXT NOT NULL DEFAULT '',
    -- passed | failed
    status VARCHAR(16) NOT NULL DEFAULT 'failed',
    -- [{name,expected,actual,passed}]
    assertions JSONB,
    -- [{metric,score,reason,error}] (display-only)
    judge JSONB,
    -- [{tool_name,tool_call_id}]
    tool_calls JSONB,
    answer TEXT NOT NULL DEFAULT '',
    reference_count INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_eval_cases_run
    ON agent_eval_case_results (run_id);
