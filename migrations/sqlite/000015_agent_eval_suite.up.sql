-- Mirrors versioned migration 000092_agent_eval_suite: agent_eval_runs 增加 suite 列与套件维度索引

ALTER TABLE agent_eval_runs
    ADD COLUMN suite VARCHAR(32) NOT NULL DEFAULT 'functional';

CREATE INDEX IF NOT EXISTS idx_agent_eval_runs_suite ON agent_eval_runs (tenant_id, agent_id, suite, created_at DESC);
