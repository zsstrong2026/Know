-- 会议产物生成链路：隐藏后台会话指针 + generated 产物元数据
ALTER TABLE meeting_rooms ADD COLUMN artifact_session_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE meeting_artifacts ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN kind_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN title VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN requirement TEXT NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_size BIGINT NOT NULL DEFAULT 0;
ALTER TABLE meeting_artifacts ADD COLUMN url VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN content_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN error TEXT NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN source_message_id VARCHAR(36) NOT NULL DEFAULT '';
