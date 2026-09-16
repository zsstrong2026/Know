package container

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func recoverOrphanAgentEvalRuns(svc interfaces.AgentEvalService) {
	ctx := context.Background()
	if err := svc.RecoverOrphanRuns(ctx); err != nil {
		logger.Warnf(ctx, "[AgentEvalRecovery] failed to recover orphan runs: %v", err)
	}
}
