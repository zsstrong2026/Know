-- 会议室发言权路由模式：host-assign（主持人指派）/ mention（提及驱动）/ free（自由抢麦）
ALTER TABLE meeting_rooms
    ADD COLUMN routing_mode VARCHAR(32) NOT NULL DEFAULT 'host-assign';
