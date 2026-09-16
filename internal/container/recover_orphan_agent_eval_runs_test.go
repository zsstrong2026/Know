package container

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAgentEvalService struct {
	recoverCalls int
	recoverErr   error
}

func (f *fakeAgentEvalService) TriggerRun(ctx context.Context, tenantID uint64, agentID, suite string, creds types.AgentEvalCredentials) (*types.AgentEvalRun, error) {
	return nil, nil
}

func (f *fakeAgentEvalService) ListRuns(ctx context.Context, tenantID uint64, agentID, suite string) ([]*types.AgentEvalRun, error) {
	return nil, nil
}

func (f *fakeAgentEvalService) GetRunDetail(ctx context.Context, tenantID uint64, agentID, suite, runID string) (*types.AgentEvalRun, []*types.AgentEvalCaseResult, error) {
	return nil, nil, nil
}

func (f *fakeAgentEvalService) DeleteRun(ctx context.Context, tenantID uint64, agentID, suite, runID string) error {
	return nil
}

func (f *fakeAgentEvalService) RecoverOrphanRuns(ctx context.Context) error {
	f.recoverCalls++
	return f.recoverErr
}

func TestRecoverOrphanAgentEvalRunsInvokesService(t *testing.T) {
	svc := &fakeAgentEvalService{}
	recoverOrphanAgentEvalRuns(svc)
	if svc.recoverCalls != 1 {
		t.Fatalf("expected RecoverOrphanRuns to be called once, got %d", svc.recoverCalls)
	}
}

func TestRecoverOrphanAgentEvalRunsSwallowsError(t *testing.T) {
	svc := &fakeAgentEvalService{recoverErr: errors.New("db down")}
	recoverOrphanAgentEvalRuns(svc)
	if svc.recoverCalls != 1 {
		t.Fatalf("expected RecoverOrphanRuns to be called once, got %d", svc.recoverCalls)
	}
}
