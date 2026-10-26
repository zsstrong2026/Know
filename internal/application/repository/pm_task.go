package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMTaskNotFound = errors.New("pm task not found")

type pmTaskRepository struct{ db *gorm.DB }

// NewPMTaskRepository 异步任务仓储（无软删）
func NewPMTaskRepository(db *gorm.DB) interfaces.PMTaskRepository {
	return &pmTaskRepository{db: db}
}

func (r *pmTaskRepository) Create(ctx context.Context, t *types.PMTask) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *pmTaskRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMTask, error) {
	var t types.PMTask
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateStatus worker 执行中逐步上报进度；id 即任务标识，租户由创建时写入
func (r *pmTaskRepository) UpdateStatus(ctx context.Context, id string, status string, progress int) error {
	res := r.db.WithContext(ctx).Model(&types.PMTask{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "progress": progress})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPMTaskNotFound
	}
	return nil
}

func (r *pmTaskRepository) Succeed(ctx context.Context, id string, result types.JSON) error {
	res := r.db.WithContext(ctx).Model(&types.PMTask{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      types.PMTaskStatusSucceeded,
			"progress":    100,
			"result":      result,
			"finished_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPMTaskNotFound
	}
	return nil
}

// Fail 进度停在失败点，便于前端展示「失败于第 N 步」
func (r *pmTaskRepository) Fail(ctx context.Context, id string, message string) error {
	res := r.db.WithContext(ctx).Model(&types.PMTask{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        types.PMTaskStatusFailed,
			"error_message": message,
			"finished_at":   time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPMTaskNotFound
	}
	return nil
}
