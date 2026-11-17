-- Migration: 000163_insight_privacy
-- Description: 洞察导出层的脱敏设置（敏感词表 + 是否隐藏证据原文）
--
-- 只作用于复制 / 导出 / 存为文档；页面展示与报告追问仍使用原文。
-- 一人一行，与自我洞悉一样按 tenant_id + owner_id 归属。

CREATE TABLE IF NOT EXISTS insight_privacy (
  tenant_id     bigint      NOT NULL,
  owner_id      varchar(36) NOT NULL,
  settings_json text        NOT NULL DEFAULT '{}',
  updated_at    timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, owner_id)
);
