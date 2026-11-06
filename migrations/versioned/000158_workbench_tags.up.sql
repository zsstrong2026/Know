-- Migration: 000158_workbench_tags
-- Description: 个人标签（P4-2）：标签本体 + 条目关联
--
-- 归属口径与条目一致：tenant_id + owner_id。同 workspace 的其他成员（含 Admin）
-- 既看不到也用不了；离开 workspace 时随个人数据一起硬删。

CREATE TABLE IF NOT EXISTS workbench_tags (
  id         varchar(36) PRIMARY KEY,
  tenant_id  bigint      NOT NULL,
  owner_id   varchar(36) NOT NULL,
  name       varchar(40) NOT NULL,
  color      varchar(16) NOT NULL DEFAULT 'gray',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

-- 同一用户下标签名唯一；软删后可重建同名（部分索引排除已删行）
CREATE UNIQUE INDEX IF NOT EXISTS uq_wt_owner_name
  ON workbench_tags (tenant_id, owner_id, name)
  WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS workbench_item_tags (
  tenant_id  bigint      NOT NULL,
  owner_id   varchar(36) NOT NULL,
  item_id    varchar(36) NOT NULL,
  tag_id     varchar(36) NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (item_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_wit_tag
  ON workbench_item_tags (tenant_id, owner_id, tag_id);
