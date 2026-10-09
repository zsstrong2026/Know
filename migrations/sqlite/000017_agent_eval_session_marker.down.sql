-- Mirrors versioned migration 000094_agent_eval_session_marker (down)

UPDATE sessions
SET description = ''
WHERE description = 'agent_eval:'
  AND title = 'agent-eval'
  AND deleted_at IS NULL;
