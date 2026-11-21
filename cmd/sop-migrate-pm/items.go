package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// migrateItems 逐项目迁需求（按 created_at ASC 分配事项编号）与附件，并补审计活动。
// mapping 是「PM 项目 id → 已落库的 SOP 项目」；缺映射说明源项目已被清理或软删，
// 该需求计入 skippedReqs 由调用方汇总成一条日志（不逐条刷屏、也不改退出码）。
func migrateItems(tx *gorm.DB, mapping map[string]*types.SopProject) (
	items int, attachments int, skippedReqs int, err error,
) {
	var reqs []pmRequirementSource
	if err := tx.Order("created_at ASC, id ASC").Find(&reqs).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("读取 pm_requirements 失败：%w", err)
	}
	existing, err := loadMigratedItemIDs(tx)
	if err != nil {
		return 0, 0, 0, err
	}
	atts, err := loadAttachments(tx)
	if err != nil {
		return 0, 0, 0, err
	}

	projectRepo := repository.NewSopProjectRepo(tx)
	itemRepo := repository.NewSopItemRepo(tx)
	extraRepo := repository.NewSopItemExtraRepo(tx)
	ctx := context.Background()

	for i := range reqs {
		src := &reqs[i]
		if existing[src.ID] {
			continue
		}
		sp, ok := mapping[src.ProjectID]
		if !ok {
			skippedReqs++
			continue
		}
		no, err := projectRepo.AllocItemNo(ctx, sp.TenantID, sp.ID)
		if err != nil {
			return items, attachments, skippedReqs, fmt.Errorf(
				"分配事项编号（源 pm_requirements/%s）失败：%w", src.ID, err)
		}
		item := buildSopItem(src, sp, no)
		if err := itemRepo.Create(ctx, item); err != nil {
			return items, attachments, skippedReqs, fmt.Errorf(
				"创建 SOP 事项（源 pm_requirements/%s）失败：%w", src.ID, err)
		}
		items++

		n, err := migrateAttachments(ctx, tx, atts[src.ID], item)
		if err != nil {
			return items, attachments, skippedReqs, err
		}
		attachments += n

		if err := extraRepo.AddActivity(ctx, &types.SopItemActivity{
			TenantID: item.TenantID,
			ItemID:   item.ID,
			Kind:     types.SopActivityKindAudit,
			Action:   "PM迁移",
			Detail:   fmt.Sprintf("由 pm_requirements/%s 迁移；项目来源 pm_projects/%s", src.ID, src.ProjectID),
		}); err != nil {
			return items, attachments, skippedReqs, fmt.Errorf("写入迁移审计（事项 %s）失败：%w", item.ID, err)
		}
	}
	return items, attachments, skippedReqs, nil
}

// loadMigratedItemIDs 收集已迁移事项的源需求 id（domain_data.pm_source_id）。
// 必须 Unscoped：工作台软删的事项仍是「已迁移」的事实，默认作用域下漏掉会导致重跑
// 重建出一个同源需求的新编号事项（附件也会被再拷一份），幂等即失效。
func loadMigratedItemIDs(tx *gorm.DB) (map[string]bool, error) {
	var rows []types.SopItem
	if err := tx.Unscoped().Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 sop_items 失败：%w", err)
	}
	out := make(map[string]bool, len(rows))
	for i := range rows {
		if src, ok := rows[i].DomainData[pmSourceIDKey].(string); ok && src != "" {
			out[src] = true
		}
	}
	return out, nil
}

// loadAttachments 按源需求 id 归集附件；软删附件不迁（列表已不可见）。
func loadAttachments(tx *gorm.DB) (map[string][]pmAttachmentSource, error) {
	var rows []pmAttachmentSource
	if err := tx.Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 pm_requirement_attachments 失败：%w", err)
	}
	out := make(map[string][]pmAttachmentSource, len(rows))
	for _, row := range rows {
		out[row.RequirementID] = append(out[row.RequirementID], row)
	}
	return out, nil
}

// migrateAttachments 把源附件同构直迁 sop_item_attachments。
func migrateAttachments(
	ctx context.Context, tx *gorm.DB, rows []pmAttachmentSource, item *types.SopItem,
) (int, error) {
	n := 0
	for _, row := range rows {
		att := &types.SopItemAttachment{
			TenantID:    item.TenantID,
			ItemID:      item.ID,
			Name:        row.FileName,
			MediaType:   row.ContentType,
			Size:        row.Size,
			Data:        row.Data,
			TextContent: row.TextContent,
			CreatedAt:   row.CreatedAt,
		}
		if err := tx.WithContext(ctx).Create(att).Error; err != nil {
			return n, fmt.Errorf("迁移附件（源 pm_requirement_attachments/%s）失败：%w", row.ID, err)
		}
		n++
	}
	return n, nil
}

// buildSopItem 把一条 pm_requirements 行映射成 SOP 事项。
// 状态映射见 pmStatusToSopStatus；分析/方案/会话/知识/驳回原因进 domain_data（原样），
// 上线复盘四字段进 outcome。
func buildSopItem(src *pmRequirementSource, sp *types.SopProject, no int) *types.SopItem {
	priority := src.Priority
	if priority == "" {
		priority = "P1"
	}
	source := src.Source
	if source == "" {
		source = types.RequirementSourceOther
	}
	return &types.SopItem{
		TenantID:    src.TenantID,
		ProjectID:   sp.ID,
		No:          fmt.Sprintf("%s-%d", sp.Code, no),
		Title:       src.Title,
		Description: src.Description,
		Kind:        "requirement",
		Priority:    priority,
		Status:      pmStatusToSopStatus(src.Status),
		Source:      source,
		Proposer:    src.Proposer,
		Owner:       src.OwnerID,
		KnowledgeID: src.KnowledgeID,
		Outcome:     outcomeOf(src),
		DomainData:  itemDomainData(src),
		CreatedBy:   src.CreatedBy,
		CreatedAt:   src.CreatedAt,
		UpdatedAt:   src.UpdatedAt,
	}
}

// pmStatusToSopStatus 是 PM 需求状态到 SOP 事项 8 态的唯一映射口径。
// analyzing 是已并入 pending_analysis 的历史枚举（000134），一并落到 backlog。
func pmStatusToSopStatus(status string) string {
	switch status {
	case types.RequirementStatusPendingAnalysis, "analyzing":
		return types.SopItemStatusBacklog
	case types.RequirementStatusConfirmed:
		return types.SopItemStatusTodo
	case types.RequirementStatusScheduled, types.RequirementStatusDeveloping:
		return types.SopItemStatusInProgress
	case types.RequirementStatusLaunched:
		return types.SopItemStatusDone
	case types.RequirementStatusRejected, types.RequirementStatusClosed:
		return types.SopItemStatusClosed
	}
	return types.SopItemStatusBacklog
}

// itemDomainData 保留 PM 需求的分析与追溯字段；ai_analysis 解析后原样搬运。
func itemDomainData(src *pmRequirementSource) map[string]any {
	return map[string]any{
		pmSourceIDKey:   src.ID,
		"ai_analysis":   rawJSONValue(src.AIAnalysis),
		"design_plan":   src.DesignPlan,
		"session_id":    src.SessionID,
		"knowledge_id":  src.KnowledgeID,
		"reject_reason": src.RejectReason,
	}
}

// outcomeOf 搬运上线复盘结论；全空时返回空结构（不伪造回填）。
func outcomeOf(src *pmRequirementSource) map[string]any {
	outc := map[string]any{
		"effect_notes":   src.EffectNotes,
		"value_achieved": src.ValueAchieved,
		"retro_notes":    src.RetroNotes,
	}
	if src.ActualLaunchDate != nil {
		outc["actual_date"] = src.ActualLaunchDate.Format("2006-01-02")
	} else {
		outc["actual_date"] = ""
	}
	return outc
}

// rawJSONValue 把 PM 的 JSONB 列搬进 domain_data：能解析就用结构体，空值落 nil，
// 解析失败退化成原始字符串——迁移命令宁可留下原文也不能丢数据。
func rawJSONValue(j types.JSON) any {
	if len(j) == 0 || string(j) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(j, &v); err != nil {
		return string(j)
	}
	return v
}
