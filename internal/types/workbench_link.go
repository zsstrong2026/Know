package types

import (
	"time"
)

// 工作台反向链接（P4-2「页内提及」口径）。
//
// 决策记录：只有**正文里出现另一个条目的标题**才算引用。数据库关联列、
// @ 提及块都不算——前者只覆盖数据库条目，后者本项目尚未实现。
//
// 数据落在 000136 就建好的 workbench_item_links 表（此前只有建表、没有写入方）：
//   source_item_id = 引用方（正文里出现别人标题的条目）
//   target_type    = 'item'
//   target_id      = 被引用方
// 「反向链接」= 以 target_id 反查谁引用了我。
const (
	WorkbenchLinkTargetItem = "item"

	// WorkbenchBacklinksMaxLimit 反向链接单次返回上限。
	WorkbenchBacklinksMaxLimit = 50
	// WorkbenchItemMentionsLimit 一次保存最多记录的引用数，防长文刷爆关联表。
	WorkbenchItemMentionsLimit = 50
	// WorkbenchMentionTitleMinRunes 标题至少这么长才参与提及匹配：
	// 单字标题（如「日」「周」）几乎必然在正文里出现，会产生海量误报。
	WorkbenchMentionTitleMinRunes = 2
)

// WorkbenchLinkTarget 一次引用关系的目标端。
type WorkbenchLinkTarget struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// WorkbenchBacklink 一条反向链接：谁引用了我。
//
// 这里不带 source_block_id（页内提及无法定位到具体块，投影是纯文本）。
type WorkbenchBacklink struct {
	// ID 是引用方条目 id；点击可跳转。
	ID string `json:"id"`
	// Title / Kind / UpdatedAt 取自引用方条目快照，用于列表展示。
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"updated_at"`
}
