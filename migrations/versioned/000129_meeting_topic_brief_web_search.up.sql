-- 会议室主题聚焦与联网检索：主持人澄清收敛的一句话主题 + 会议级联网搜索总闸
ALTER TABLE meeting_rooms ADD COLUMN topic_brief TEXT NOT NULL DEFAULT '';
ALTER TABLE meeting_rooms ADD COLUMN web_search_enabled BOOLEAN NOT NULL DEFAULT FALSE;
