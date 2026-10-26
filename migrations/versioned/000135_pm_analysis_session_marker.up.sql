-- Migration: 000135_pm_analysis_session_marker
-- Description: 给存量 PM 工作站需求分析会话打上 pm_analysis: 标记，使其从首页会话列表中隐藏
-- PM 工作站的 AI 分析会在后端创建一个真实会话承载分析上下文，但它不是用户对话。
-- 新会话由服务端在 description 上写入 pm_analysis: 前缀；此迁移把修复前创建的存量会话
-- 补上同一标记（存量 description 存的是分析提示词，需保留，故用拼接）。
-- 识别口径：
--   1) 需求表登记的 session_id（正常路径）；
--   2) description 含 pm-requirement-analysis 技能标识（会话被重新分析覆盖后，旧会话不再被登记）。
UPDATE sessions
SET description = 'pm_analysis:' || COALESCE(description, '')
WHERE (
    id IN (
      SELECT session_id FROM pm_requirements
      WHERE session_id IS NOT NULL AND session_id <> ''
    )
    OR description LIKE '%pm-requirement-analysis%'
  )
  AND (description IS NULL OR description NOT LIKE 'pm_analysis:%')
  AND deleted_at IS NULL;
