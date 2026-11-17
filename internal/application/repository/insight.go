package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// insightRepository 是洞察模块的 GORM 实现。
// 每个查询都同时带 tenant_id 与 owner_id，绝不提供“只按 id 查”的入口。
type insightRepository struct {
	db *gorm.DB
}

// NewInsightRepository 构造洞察仓储。
func NewInsightRepository(db *gorm.DB) interfaces.InsightRepository {
	return &insightRepository{db: db}
}

// ListThemes 列出当前用户全部主题（内置在前，同组按创建时间升序）。
func (r *insightRepository) ListThemes(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.InsightTheme, error) {
	var themes []*types.InsightTheme
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Order("builtin DESC, created_at ASC").
		Find(&themes).Error
	if err != nil {
		return nil, err
	}
	return themes, nil
}

// GetTheme 按 (tenant, owner, id) 取主题。
func (r *insightRepository) GetTheme(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.InsightTheme, error) {
	var theme types.InsightTheme
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		First(&theme).Error
	if err != nil {
		return nil, err
	}
	return &theme, nil
}

// CreateTheme 插入主题。
func (r *insightRepository) CreateTheme(
	ctx context.Context, tenantID uint64, ownerID string, theme *types.InsightTheme,
) error {
	theme.TenantID = tenantID
	theme.OwnerID = ownerID
	return r.db.WithContext(ctx).Create(theme).Error
}

// UpdateTheme 更新主题允许修改的列；builtin 标记不随请求变化。
func (r *insightRepository) UpdateTheme(
	ctx context.Context, tenantID uint64, ownerID string, theme *types.InsightTheme,
) error {
	return r.db.WithContext(ctx).
		Model(&types.InsightTheme{}).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, theme.ID).
		Updates(map[string]interface{}{
			"name":         theme.Name,
			"description":  theme.Description,
			"sensitive":    theme.Sensitive,
			"glyph":        theme.Glyph,
			"instructions": theme.Instructions,
			"dimensions":   theme.Dimensions,
			"updated_at":   time.Now(),
		}).Error
}

// DeleteTheme 软删主题。报告不级联删除，仍可按 id 读取。
func (r *insightRepository) DeleteTheme(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		Delete(&types.InsightTheme{}).Error
}

// CountReportsByTheme 统计某主题下的报告数量。
func (r *insightRepository) CountReportsByTheme(
	ctx context.Context, tenantID uint64, ownerID, themeID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.InsightReport{}).
		Where("tenant_id = ? AND owner_id = ? AND theme_id = ?", tenantID, ownerID, themeID).
		Count(&count).Error
	return count, err
}

// LatestReportByTheme 取主题最新一份报告（含正文），供自我洞悉聚合。
func (r *insightRepository) LatestReportByTheme(
	ctx context.Context, tenantID uint64, ownerID, themeID string,
) (*types.InsightReport, error) {
	var report types.InsightReport
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND theme_id = ?", tenantID, ownerID, themeID).
		Order("created_at DESC").
		First(&report).Error
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// CreateRun 插入运行记录。
func (r *insightRepository) CreateRun(
	ctx context.Context, tenantID uint64, ownerID string, run *types.InsightRun,
) error {
	run.TenantID = tenantID
	run.OwnerID = ownerID
	return r.db.WithContext(ctx).Create(run).Error
}

// GetRun 按 (tenant, owner, id) 取运行。
func (r *insightRepository) GetRun(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.InsightRun, error) {
	var run types.InsightRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// UpdateRunFields 局部更新运行；fields 由服务层构造，不接受外部输入。
func (r *insightRepository) UpdateRunFields(
	ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]interface{},
) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).
		Model(&types.InsightRun{}).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		Updates(fields).Error
}

// LatestRunByTheme 取主题最近一次运行，用于列表里的“上次运行/运行中”标记。
func (r *insightRepository) LatestRunByTheme(
	ctx context.Context, tenantID uint64, ownerID, themeID string,
) (*types.InsightRun, error) {
	var run types.InsightRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND theme_id = ?", tenantID, ownerID, themeID).
		Order("created_at DESC").
		First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// DeleteRun 硬删运行行；证据由服务层先删。
func (r *insightRepository) DeleteRun(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		Delete(&types.InsightRun{}).Error
}

// CreateEvidenceBatch 批量写入证据。
func (r *insightRepository) CreateEvidenceBatch(
	ctx context.Context, tenantID uint64, ownerID string, items []*types.InsightEvidence,
) error {
	if len(items) == 0 {
		return nil
	}
	for _, item := range items {
		item.TenantID = tenantID
		item.OwnerID = ownerID
	}
	return r.db.WithContext(ctx).CreateInBatches(items, 100).Error
}

// ListEvidence 列出一次运行的全部证据（含已排除项）。
func (r *insightRepository) ListEvidence(
	ctx context.Context, tenantID uint64, ownerID, runID string,
) ([]*types.InsightEvidence, error) {
	var items []*types.InsightEvidence
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND run_id = ?", tenantID, ownerID, runID).
		Order("chunk_index ASC, created_at ASC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ListEvidenceForReport 只取未排除证据，按抽取顺序限量。
func (r *insightRepository) ListEvidenceForReport(
	ctx context.Context, tenantID uint64, ownerID, runID string, limit int,
) ([]*types.InsightEvidence, error) {
	var items []*types.InsightEvidence
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND run_id = ? AND excluded = ?", tenantID, ownerID, runID, false).
		Order("chunk_index ASC, created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// SetEvidenceExcluded 批量设置排除标记，作用域包含 run_id。
func (r *insightRepository) SetEvidenceExcluded(
	ctx context.Context, tenantID uint64, ownerID, runID string, ids []string, excluded bool,
) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&types.InsightEvidence{}).
		Where("tenant_id = ? AND owner_id = ? AND run_id = ? AND id IN ?", tenantID, ownerID, runID, ids).
		Update("excluded", excluded).Error
}

// DeleteEvidenceByRun 删除某次运行的全部证据。
func (r *insightRepository) DeleteEvidenceByRun(
	ctx context.Context, tenantID uint64, ownerID, runID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND run_id = ?", tenantID, ownerID, runID).
		Delete(&types.InsightEvidence{}).Error
}

// CreateReport 插入报告。
func (r *insightRepository) CreateReport(
	ctx context.Context, tenantID uint64, ownerID string, report *types.InsightReport,
) error {
	report.TenantID = tenantID
	report.OwnerID = ownerID
	return r.db.WithContext(ctx).Create(report).Error
}

// GetReport 按 (tenant, owner, id) 取报告（含正文）。
func (r *insightRepository) GetReport(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.InsightReport, error) {
	var report types.InsightReport
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		First(&report).Error
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// ListReportsByTheme 列出主题报告，不加载正文，避免列表传输大 JSON。
func (r *insightRepository) ListReportsByTheme(
	ctx context.Context, tenantID uint64, ownerID, themeID string,
) ([]*types.InsightReport, error) {
	var reports []*types.InsightReport
	err := r.db.WithContext(ctx).
		Select("id, tenant_id, owner_id, theme_id, run_id, title, model_id, docs_covered, evidence_count, created_at").
		Where("tenant_id = ? AND owner_id = ? AND theme_id = ?", tenantID, ownerID, themeID).
		Order("created_at DESC").
		Find(&reports).Error
	if err != nil {
		return nil, err
	}
	return reports, nil
}

// DeleteReport hard delete 单份报告。
func (r *insightRepository) DeleteReport(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND id = ?", tenantID, ownerID, id).
		Delete(&types.InsightReport{}).Error
}

// CountReports 统计当前用户全部报告数量。
func (r *insightRepository) CountReports(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.InsightReport{}).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Count(&count).Error
	return count, err
}

// GetSelf 取用户的自我洞悉快照。
func (r *insightRepository) GetSelf(
	ctx context.Context, tenantID uint64, ownerID string,
) (*types.InsightSelf, error) {
	var self types.InsightSelf
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		First(&self).Error
	if err != nil {
		return nil, err
	}
	return &self, nil
}

// SaveSelf 覆盖写入自我洞悉；唯一索引 (tenant_id, owner_id) 保证一人一份。
func (r *insightRepository) SaveSelf(
	ctx context.Context, tenantID uint64, ownerID string, self *types.InsightSelf,
) error {
	self.TenantID = tenantID
	self.OwnerID = ownerID
	self.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "owner_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "content_json", "error", "model_id", "updated_at",
		}),
	}).Create(self).Error
}

// insightPrivacyRow 是脱敏设置的存储行：整份设置以 JSON 存在一列里。
type insightPrivacyRow struct {
	TenantID     uint64    `gorm:"column:tenant_id;primaryKey"`
	OwnerID      string    `gorm:"column:owner_id;primaryKey"`
	SettingsJSON string    `gorm:"column:settings_json"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (insightPrivacyRow) TableName() string { return "insight_privacy" }

// GetPrivacy 取当前用户的脱敏设置；从未保存过时返回空设置而不是 NotFound。
func (r *insightRepository) GetPrivacy(
	ctx context.Context, tenantID uint64, ownerID string,
) (*types.InsightPrivacy, error) {
	privacy := &types.InsightPrivacy{}
	var row insightPrivacyRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return privacy, nil
		}
		return nil, err
	}
	if row.SettingsJSON != "" {
		if decodeErr := json.Unmarshal([]byte(row.SettingsJSON), privacy); decodeErr != nil {
			return nil, decodeErr
		}
	}
	return privacy, nil
}

// SavePrivacy 覆盖保存当前用户的脱敏设置。
func (r *insightRepository) SavePrivacy(
	ctx context.Context, tenantID uint64, ownerID string, privacy *types.InsightPrivacy,
) error {
	raw, err := json.Marshal(privacy)
	if err != nil {
		return err
	}
	row := &insightPrivacyRow{
		TenantID:     tenantID,
		OwnerID:      ownerID,
		SettingsJSON: string(raw),
		UpdatedAt:    time.Now(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "owner_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"settings_json", "updated_at",
		}),
	}).Create(row).Error
}

// ListRunsByTheme 列出主题的运行记录（倒序）。
func (r *insightRepository) ListRunsByTheme(
	ctx context.Context, tenantID uint64, ownerID, themeID string, limit int,
) ([]*types.InsightRun, error) {
	var runs []*types.InsightRun
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND theme_id = ?", tenantID, ownerID, themeID).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// ListReportMessages 取一份报告最近的会话消息（倒序，由服务层翻正）。
func (r *insightRepository) ListReportMessages(
	ctx context.Context, tenantID uint64, ownerID, reportID string, limit int,
) ([]*types.InsightReportMessage, error) {
	var items []*types.InsightReportMessage
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND report_id = ?", tenantID, ownerID, reportID).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// CreateReportMessage 追加一条会话消息。
func (r *insightRepository) CreateReportMessage(
	ctx context.Context, tenantID uint64, ownerID string, msg *types.InsightReportMessage,
) error {
	msg.TenantID = tenantID
	msg.OwnerID = ownerID
	return r.db.WithContext(ctx).Create(msg).Error
}

// DeleteReportMessages 删除一份报告的全部追问消息；报告删除时同步调用。
func (r *insightRepository) DeleteReportMessages(
	ctx context.Context, tenantID uint64, ownerID, reportID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ? AND report_id = ?", tenantID, ownerID, reportID).
		Delete(&types.InsightReportMessage{}).Error
}

// ListSelfMessages 取自我洞悉最近的会话消息（倒序，由服务层翻正）。
func (r *insightRepository) ListSelfMessages(
	ctx context.Context, tenantID uint64, ownerID string, limit int,
) ([]*types.InsightSelfMessage, error) {
	var items []*types.InsightSelfMessage
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// CreateSelfMessage 追加一条自我洞悉会话消息。
func (r *insightRepository) CreateSelfMessage(
	ctx context.Context, tenantID uint64, ownerID string, msg *types.InsightSelfMessage,
) error {
	msg.TenantID = tenantID
	msg.OwnerID = ownerID
	return r.db.WithContext(ctx).Create(msg).Error
}

// DeleteSelfMessages 清空用户的自我洞悉会话；重新生成说明书时同步调用。
func (r *insightRepository) DeleteSelfMessages(
	ctx context.Context, tenantID uint64, ownerID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Delete(&types.InsightSelfMessage{}).Error
}
