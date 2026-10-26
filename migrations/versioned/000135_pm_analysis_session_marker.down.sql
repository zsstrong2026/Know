-- 回滚：移除 PM 分析会话标记，保留原有的分析提示词内容。
UPDATE sessions
SET description = substr(description, length('pm_analysis:') + 1)
WHERE description LIKE 'pm_analysis:%pm-requirement-analysis%';
