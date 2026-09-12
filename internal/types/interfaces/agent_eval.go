package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type AgentEvalRepository interface {
	CreateRun(ctx context.Context, run *types.AgentEvalRun) error
	UpdateRun(ctx context.Context, run *types.AgentEvalRun) error
	GetRun(ctx context.Context, id string, tenantID uint64) (*types.AgentEvalRun, error)
	FindRunningByAgent(ctx context.Context, tenantID uint64, agentID string) (*types.AgentEvalRun, error)
	ListRunsByAgent(ctx context.Context, tenantID uint64, agentID string, limit int) ([]*types.AgentEvalRun, error)
	ListRunningRuns(ctx context.Context) ([]*types.AgentEvalRun, error)
	DeleteRun(ctx context.Context, id string, tenantID uint64) error
	CreateCaseResult(ctx context.Context, result *types.AgentEvalCaseResult) error
	ListCaseResultsByRun(ctx context.Context, runID string, tenantID uint64) ([]*types.AgentEvalCaseResult, error)
	RecoverOrphanRuns(ctx context.Context, message string) (int64, error)
	PruneRunsByAgent(ctx context.Context, tenantID uint64, agentID string, keep int) (int64, error)
}

type AgentEvalService interface {
	TriggerRun(ctx context.Context, tenantID uint64, agentID string, creds types.AgentEvalCredentials) (*types.AgentEvalRun, error)
	ListRuns(ctx context.Context, tenantID uint64, agentID string) ([]*types.AgentEvalRun, error)
	GetRunDetail(ctx context.Context, tenantID uint64, agentID string, runID string) (*types.AgentEvalRun, []*types.AgentEvalCaseResult, error)
	DeleteRun(ctx context.Context, tenantID uint64, agentID string, runID string) error
	RecoverOrphanRuns(ctx context.Context) error
}
