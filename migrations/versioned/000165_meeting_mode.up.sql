-- 会议模式：hosted 主持模式（默认，主持人开场分析与控场）/ free 自由模式（无主持人，智能体自主讨论）
ALTER TABLE meeting_rooms
    ADD COLUMN mode VARCHAR(16) NOT NULL DEFAULT 'hosted';
