-- Migration 000091 down: drop agent smoke-test tables.

DROP TABLE IF EXISTS agent_eval_case_results;
DROP TABLE IF EXISTS agent_eval_runs;
