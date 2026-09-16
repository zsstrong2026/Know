-- Migration: 000092_agent_eval_suite
-- Description: agent_eval_runs 增加 suite 列区分功能/安全测试套件，并补充套件维度索引

ALTER TABLE agent_eval_runs
    ADD COLUMN IF NOT EXISTS suite VARCHAR(32) NOT NULL DEFAULT 'functional';

CREATE INDEX IF NOT EXISTS idx_agent_eval_runs_suite ON agent_eval_runs (tenant_id, agent_id, suite, created_at DESC);
