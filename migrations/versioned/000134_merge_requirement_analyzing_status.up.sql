-- 「分析中」状态已下线：AI 分析仅作辅助，不再推进需求状态。
-- 存量数据回退为「待分析」，由用户确认后转为「已确认」。
UPDATE pm_requirements SET status = 'pending_analysis' WHERE status = 'analyzing';
