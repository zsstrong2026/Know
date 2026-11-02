-- 会议室结构性治理：议程拆解 + 按议题组织上下文 + 发言准入
-- 房间级开关：agenda_enabled 打开后主持人开场拆议程、讨论按议题取上下文
ALTER TABLE meeting_rooms ADD COLUMN agenda_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- 发言准入模式：off（默认，不判定）/ observe（只记录）/ enforce（不通过要求重写一次）
ALTER TABLE meeting_rooms ADD COLUMN admission_mode VARCHAR(16) NOT NULL DEFAULT 'off';

-- 消息归属议程项 + 准入判定结果（空串表示未归属/未判定，历史行天然兼容）
ALTER TABLE meeting_messages ADD COLUMN agenda_item_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE meeting_messages ADD COLUMN admission_state VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_messages ADD COLUMN admission_note TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_meeting_messages_room_agenda ON meeting_messages (meeting_room_id, agenda_item_id);

-- 议程项：主持人开场拆解出的子议题，按 seq 顺序推进
CREATE TABLE meeting_agenda_items (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL DEFAULT 0,
    title VARCHAR(255) NOT NULL,
    goal TEXT NOT NULL DEFAULT '',
    owner_agent_ids JSONB NOT NULL DEFAULT '[]',
    knowledge_base_ids JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_meeting_agenda_room_seq ON meeting_agenda_items (meeting_room_id, seq);
