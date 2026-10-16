-- 会议室裁判 + 共识检测：开启后每个子轮结束由独立裁判判定是否收敛，收敛即落共识并终止讨论
ALTER TABLE meeting_rooms
    ADD COLUMN consensus_detection_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- 共识消息标记：裁判判定的收敛结论落为 is_consensus=true 的主持人消息
ALTER TABLE meeting_messages
    ADD COLUMN is_consensus BOOLEAN NOT NULL DEFAULT FALSE;
