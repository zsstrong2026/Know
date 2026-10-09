-- Migration: 000094_agent_eval_session_marker
-- Description: 给存量智能体冒烟测试会话打上 agent_eval: 标记，使其从控制台会话列表中隐藏
-- 智能体"运行测试"（agent eval）的每个用例都会创建一个 title='agent-eval' 的真实会话。
-- 新会话由服务端写入 description 前缀 agent_eval:；此迁移把修复前创建的存量会话补上同一
-- 标记（eval 会话从不设置 description，所以 description 为空是安全的前提条件）。

UPDATE sessions
SET description = 'agent_eval:'
WHERE title = 'agent-eval'
  AND (description IS NULL OR description = '')
  AND deleted_at IS NULL;
