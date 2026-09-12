package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrAgentEvalRunNotFound = errors.New("agent eval run not found")

type agentEvalRepository struct {
	db *gorm.DB
}

func NewAgentEvalRepository(db *gorm.DB) interfaces.AgentEvalRepository {
	return &agentEvalRepository{db: db}
}

func (r *agentEvalRepository) CreateRun(ctx context.Context, run *types.AgentEvalRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *agentEvalRepository) UpdateRun(ctx context.Context, run *types.AgentEvalRun) error {
	return r.db.WithContext(ctx).Save(run).Error
}

func (r *agentEvalRepository) GetRun(ctx context.Context, id string, tenantID uint64) (*types.AgentEvalRun, error) {
	var run types.AgentEvalRun
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentEvalRunNotFound
		}
		return nil, err
	}
	return &run, nil
}

func (r *agentEvalRepository) FindRunningByAgent(ctx context.Context, tenantID uint64, agentID string) (*types.AgentEvalRun, error) {
	var run types.AgentEvalRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ? AND status = ?", tenantID, agentID, types.AgentEvalRunStatusRunning).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (r *agentEvalRepository) ListRunsByAgent(ctx context.Context, tenantID uint64, agentID string, limit int) ([]*types.AgentEvalRun, error) {
	var runs []*types.AgentEvalRun
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ?", tenantID, agentID).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *agentEvalRepository) ListRunningRuns(ctx context.Context) ([]*types.AgentEvalRun, error) {
	var runs []*types.AgentEvalRun
	if err := r.db.WithContext(ctx).
		Where("status = ?", types.AgentEvalRunStatusRunning).
		Order("created_at ASC").
		Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *agentEvalRepository) DeleteRun(ctx context.Context, id string, tenantID uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("run_id = ? AND tenant_id = ?", id, tenantID).
			Delete(&types.AgentEvalCaseResult{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&types.AgentEvalRun{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrAgentEvalRunNotFound
		}
		return nil
	})
}

func (r *agentEvalRepository) CreateCaseResult(ctx context.Context, result *types.AgentEvalCaseResult) error {
	return r.db.WithContext(ctx).Create(result).Error
}

func (r *agentEvalRepository) ListCaseResultsByRun(ctx context.Context, runID string, tenantID uint64) ([]*types.AgentEvalCaseResult, error) {
	var results []*types.AgentEvalCaseResult
	if err := r.db.WithContext(ctx).
		Where("run_id = ? AND tenant_id = ?", runID, tenantID).
		Order("created_at ASC").
		Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}

func (r *agentEvalRepository) RecoverOrphanRuns(ctx context.Context, message string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&types.AgentEvalRun{}).
		Where("status = ?", types.AgentEvalRunStatusRunning).
		Updates(map[string]interface{}{
			"status":      types.AgentEvalRunStatusFailed,
			"error":       message,
			"finished_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

func (r *agentEvalRepository) PruneRunsByAgent(ctx context.Context, tenantID uint64, agentID string, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var cutoffs []time.Time
	if err := r.db.WithContext(ctx).Model(&types.AgentEvalRun{}).
		Where("tenant_id = ? AND agent_id = ?", tenantID, agentID).
		Order("created_at DESC").
		Offset(keep-1).Limit(1).
		Pluck("created_at", &cutoffs).Error; err != nil {
		return 0, err
	}
	if len(cutoffs) == 0 {
		return 0, nil
	}
	var pruned int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&types.AgentEvalRun{}).
			Where("tenant_id = ? AND agent_id = ? AND created_at < ?", tenantID, agentID, cutoffs[0]).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("tenant_id = ? AND run_id IN ?", tenantID, ids).
			Delete(&types.AgentEvalCaseResult{}).Error; err != nil {
			return err
		}
		res := tx.Where("tenant_id = ? AND id IN ?", tenantID, ids).Delete(&types.AgentEvalRun{})
		if res.Error != nil {
			return res.Error
		}
		pruned = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pruned, nil
}
