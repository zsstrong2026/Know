-- Migration: 000094_agent_eval_session_marker (down)
-- 移除本迁移写入的标记。仅清理由此迁移补写的"纯标记"行；
-- 新版本服务端写入的 agent_eval:<run_id> 不会被误删。

UPDATE sessions
SET description = ''
WHERE description = 'agent_eval:'
  AND title = 'agent-eval'
  AND deleted_at IS NULL;
