-- Migration: 000174_meeting_phase_events
-- Description: 会议室显式阶段（phase）与会议事件日志（P0：流程可追踪、可回放）
--
-- 归属口径：两者均按 tenant_id + meeting_room_id 隔离；phase 与 status 正交：
--   status（active/awaiting_confirmation/archived）管读写权限，
--   phase（opening/confirming/discussing/converging/producing/archived）管讨论推进位置。
-- 事件表只追加不更新，作为状态机与审计的唯一事实来源。

ALTER TABLE meeting_rooms ADD COLUMN IF NOT EXISTS phase VARCHAR(32) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS meeting_events (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    meeting_room_id VARCHAR(36) NOT NULL,
    phase VARCHAR(32) NOT NULL DEFAULT '',
    type VARCHAR(48) NOT NULL,
    actor_kind VARCHAR(16) NOT NULL DEFAULT 'system',
    actor_id VARCHAR(36) NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_meeting_events_room_created ON meeting_events(meeting_room_id, created_at);
CREATE INDEX IF NOT EXISTS ix_meeting_events_tenant_created ON meeting_events(tenant_id, created_at);

-- 存量会议回填：按 status 推断一个合理阶段，避免历史房间 phase 为空导致前端/审计判空
UPDATE meeting_rooms
SET phase = CASE
    WHEN status = 'archived' THEN 'archived'
    WHEN status = 'awaiting_confirmation' THEN 'confirming'
    ELSE 'discussing'
END
WHERE phase = '';
