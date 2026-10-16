-- 会议室主持人介入间隔：每 N 个子轮由主持人总结并重定向，防止讨论跑偏；0 表示不介入
ALTER TABLE meeting_rooms
    ADD COLUMN host_every_n_rounds INT NOT NULL DEFAULT 0;
