ALTER TABLE meeting_messages DROP COLUMN IF EXISTS is_consensus;
ALTER TABLE meeting_rooms DROP COLUMN IF EXISTS consensus_detection_enabled;
