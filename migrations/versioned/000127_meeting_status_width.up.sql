-- awaiting_confirmation（21 字符）超出 meeting_rooms.status 原 varchar(16) 宽度
ALTER TABLE meeting_rooms ALTER COLUMN status TYPE varchar(32);
