-- 回滚说明：若已存在 awaiting_confirmation 行，缩窄会失败，需先把此类行置回 active
ALTER TABLE meeting_rooms ALTER COLUMN status TYPE varchar(16);
