package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 编译期断言：黑板仓储实现必须满足接口契约（T61 简报服务与 T65 HTTP 层的消费面）。
var _ interfaces.SopProjectNoteRepository = (*SopProjectNoteRepo)(nil)

// sopNoteSeqRetries 是 seq 分配的唯一冲突重试次数。
//
// SQLite 的事务串行化已足够，但 PG 上两个并发写入可以同时读到同一个 max(seq)，
// 由部分唯一索引 ux_sop_notes_project_seq 拒掉后插入的一方，重试即可拿到新 max。
const sopNoteSeqRetries = 3

// sopNoteDefaultListLimit 是 List 不分页时的默认上限（黑板是时间线，避免一次拉全项目历史）。
const sopNoteDefaultListLimit = 200

// SopProjectNoteRepo 是项目黑板（共享上下文时间线）的 GORM 实现。
//
// 不变式：
//   - 每条读写都带 tenant_id（List/MaxSeq 额外带 project_id，不允许跨项目查询）；
//   - seq 由仓储分配（项目内自增、软删行不占位），调用方传入的 seq 一律被覆盖；
//   - Update 只写可编辑列（kind/title/body/pinned）并推进 seq，归属列取库内值。
type SopProjectNoteRepo struct{ db *gorm.DB }

// NewSopProjectNoteRepo 构造黑板仓储。
func NewSopProjectNoteRepo(db *gorm.DB) *SopProjectNoteRepo {
	return &SopProjectNoteRepo{db: db}
}

// sopNoteUpdateColumns 是 Update 允许改写的列白名单（seq/updated_at 由仓储计算）。
//
// project_id/item_id/run_id/source/created_by 是归属与来源列：编辑不能把一条人工记录
// 改成引擎自动记录，也不能把记录挪到别的项目/事项/运行下。
var sopNoteUpdateColumns = []string{"kind", "title", "body", "pinned", "seq", "updated_at"}

// Create 插入黑板条目：事务内取项目 max(seq)+1 写回入参，唯一索引冲突时重试。
func (r *SopProjectNoteRepo) Create(ctx context.Context, n *types.SopProjectNote) error {
	if n == nil {
		return fmt.Errorf("%w: note is required", types.ErrInvalidSopNote)
	}
	if err := types.ValidateSopNote(n.Kind); err != nil {
		return err
	}
	if err := types.ValidateSopNoteSource(n.Source); err != nil {
		return err
	}
	if n.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", types.ErrInvalidSopNote)
	}

	var lastErr error
	for attempt := 0; attempt < sopNoteSeqRetries; attempt++ {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			seq, err := nextSopNoteSeq(tx, n.TenantID, n.ProjectID)
			if err != nil {
				return err
			}
			n.Seq = seq
			return tx.Create(n).Error
		})
		if err == nil {
			return nil
		}
		if !isUniqueViolation(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// nextSopNoteSeq 读项目当前最大 seq 并加一（软删行不占位）。
func nextSopNoteSeq(tx *gorm.DB, tenantID uint64, projectID string) (int64, error) {
	var max int64
	err := tx.Model(&types.SopProjectNote{}).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&max).Error
	return max + 1, err
}

// MaxSeq 取项目当前最大 seq（只看活行；空项目返回 0）。
func (r *SopProjectNoteRepo) MaxSeq(ctx context.Context, tenantID uint64, projectID string) (int64, error) {
	var max int64
	err := r.db.WithContext(ctx).Model(&types.SopProjectNote{}).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&max).Error
	return max, err
}

// GetByID 按 (租户, id) 取条目，查不到返回 gorm.ErrRecordNotFound。
func (r *SopProjectNoteRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopProjectNote, error) {
	var n types.SopProjectNote
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&n).Error
	return &n, err
}

// List 列项目黑板：置顶优先，其次 seq 倒序。
//
// kind 为空表示不过滤；pinned 为 nil 表示不过滤；limit<=0 取默认上限。
func (r *SopProjectNoteRepo) List(
	ctx context.Context, tenantID uint64, projectID, kind string, pinned *bool, limit int,
) ([]types.SopProjectNote, error) {
	q := r.db.WithContext(ctx).Model(&types.SopProjectNote{}).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if pinned != nil {
		q = q.Where("pinned = ?", *pinned)
	}
	if limit <= 0 {
		limit = sopNoteDefaultListLimit
	}
	var list []types.SopProjectNote
	err := q.Order("pinned DESC").Order("seq DESC").Limit(limit).Find(&list).Error
	return list, err
}

// Update 保存可编辑列并推进 seq（编辑也是一次「动态」，需要触发简报重渲染）。
//
// 未被 (租户, id) 命中的行返回 gorm.ErrRecordNotFound；归属列一律取库内值。
func (r *SopProjectNoteRepo) Update(ctx context.Context, tenantID uint64, n *types.SopProjectNote) error {
	if n == nil {
		return fmt.Errorf("%w: note is required", types.ErrInvalidSopNote)
	}
	if err := types.ValidateSopNote(n.Kind); err != nil {
		return err
	}

	existing, err := r.GetByID(ctx, tenantID, n.ID)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < sopNoteSeqRetries; attempt++ {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			seq, err := nextSopNoteSeq(tx, existing.TenantID, existing.ProjectID)
			if err != nil {
				return err
			}
			patch := &types.SopProjectNote{
				Kind:      n.Kind,
				Title:     n.Title,
				Body:      n.Body,
				Pinned:    n.Pinned,
				Seq:       seq,
				UpdatedAt: time.Now(),
			}
			res := tx.Model(&types.SopProjectNote{}).
				Where("id = ? AND tenant_id = ?", existing.ID, tenantID).
				Select(sopNoteUpdateColumns).
				Updates(patch)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			n.Seq = seq
			return nil
		})
		if err == nil {
			return nil
		}
		if !isUniqueViolation(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// SoftDelete 软删条目；跨租户或不存在返回 gorm.ErrRecordNotFound。
func (r *SopProjectNoteRepo) SoftDelete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.SopProjectNote{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
