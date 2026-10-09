-- Mirrors versioned migration 000094_agent_eval_session_marker:
-- 给存量智能体冒烟测试会话打上 agent_eval: 标记，使其从控制台会话列表中隐藏

UPDATE sessions
SET description = 'agent_eval:'
WHERE title = 'agent-eval'
  AND (description IS NULL OR description = '')
  AND deleted_at IS NULL;
