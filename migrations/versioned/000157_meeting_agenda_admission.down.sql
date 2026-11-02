DROP TABLE IF EXISTS meeting_agenda_items;

DROP INDEX IF EXISTS idx_meeting_messages_room_agenda;
ALTER TABLE meeting_messages DROP COLUMN IF EXISTS admission_note;
ALTER TABLE meeting_messages DROP COLUMN IF EXISTS admission_state;
ALTER TABLE meeting_messages DROP COLUMN IF EXISTS agenda_item_id;

ALTER TABLE meeting_rooms DROP COLUMN IF EXISTS admission_mode;
ALTER TABLE meeting_rooms DROP COLUMN IF EXISTS agenda_enabled;
