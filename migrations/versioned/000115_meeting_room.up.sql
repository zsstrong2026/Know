-- 会议室：4 张表。所有查询均以 tenant_id + created_by 双重过滤（仅创建者可见）。
CREATE TABLE meeting_rooms (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    created_by VARCHAR(36) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',        -- active / archived
    broadcast_mode VARCHAR(16) NOT NULL DEFAULT 'sequential', -- sequential / parallel
    guard_max_exchange INTEGER NOT NULL DEFAULT 3,       -- 护栏：互呛来回上限
    knowledge_base_ids JSONB NOT NULL DEFAULT '[]',      -- 会议级知识库范围
    current_solution_message_id VARCHAR(36) NOT NULL DEFAULT '',
    context_summary TEXT NOT NULL DEFAULT '',            -- 早期消息摘要缓存
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_meeting_rooms_tenant_creator_status ON meeting_rooms (tenant_id, created_by, status);
CREATE INDEX idx_meeting_rooms_tenant_creator_updated ON meeting_rooms (tenant_id, created_by, updated_at DESC);

CREATE TABLE meeting_participants (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    kind VARCHAR(8) NOT NULL,                            -- host / agent
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    avatar VARCHAR(64) NOT NULL DEFAULT '',
    order_index INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
-- 同一会议内同一智能体唯一；主持人唯一且不可移除
CREATE UNIQUE INDEX idx_meeting_participants_room_agent ON meeting_participants (meeting_room_id, agent_id) WHERE kind = 'agent';
CREATE UNIQUE INDEX idx_meeting_participants_room_host ON meeting_participants (meeting_room_id) WHERE kind = 'host';

CREATE TABLE meeting_messages (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    sender_kind VARCHAR(8) NOT NULL,                     -- user / agent / host / system
    sender_id VARCHAR(36) NOT NULL DEFAULT '',           -- agent_id 或用户 ID
    reply_to_message_id VARCHAR(36) NOT NULL DEFAULT '',
    mentioned_agent_ids JSONB NOT NULL DEFAULT '[]',     -- @ 点名（优先于智能筛选）
    content TEXT NOT NULL,
    payload JSONB,                                       -- 引用/思考过程等扩展
    review_state VARCHAR(16) NOT NULL DEFAULT 'none',    -- none / pending / adopted / rejected
    is_solution BOOLEAN NOT NULL DEFAULT false,
    solution_version INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_meeting_messages_room_created ON meeting_messages (meeting_room_id, created_at, id);
CREATE INDEX idx_meeting_messages_room_review ON meeting_messages (meeting_room_id, review_state);
CREATE INDEX idx_meeting_messages_room_solution ON meeting_messages (meeting_room_id, is_solution);

CREATE TABLE meeting_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    solution_message_id VARCHAR(36) NOT NULL,            -- 指向消息，不重复存正文
    version INTEGER NOT NULL,
    adopted_count INTEGER NOT NULL DEFAULT 0,            -- 快照：采纳数
    rejected_count INTEGER NOT NULL DEFAULT 0,           -- 快照：不采纳数
    exported_format VARCHAR(8) NOT NULL DEFAULT 'md',
    created_by VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_meeting_artifacts_room_version ON meeting_artifacts (meeting_room_id, version);
