-- Mirrors versioned migration 000092_agent_eval_suite rollback

DROP INDEX IF EXISTS idx_agent_eval_runs_suite;

ALTER TABLE agent_eval_runs
    DROP COLUMN suite;
