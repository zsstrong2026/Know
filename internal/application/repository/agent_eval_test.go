package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAgentEvalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.AgentEvalRun{}, &types.AgentEvalCaseResult{}))
	return db
}

func makeEvalRun(tenantID uint64, agentID, status string, createdAt time.Time) *types.AgentEvalRun {
	return &types.AgentEvalRun{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		AgentID:   agentID,
		Status:    status,
		Suite:     types.AgentEvalSuiteFunctional,
		Trigger:   types.AgentEvalTriggerManual,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: createdAt.UTC(),
	}
}

func makeEvalRunWithSuite(tenantID uint64, agentID, status, suite string, createdAt time.Time) *types.AgentEvalRun {
	run := makeEvalRun(tenantID, agentID, status, createdAt)
	run.Suite = suite
	return run
}

func makeEvalCaseResult(tenantID uint64, runID string, caseID string, createdAt time.Time) *types.AgentEvalCaseResult {
	return &types.AgentEvalCaseResult{
		ID:        uuid.New().String(),
		RunID:     runID,
		TenantID:  tenantID,
		CaseID:    caseID,
		CaseTitle: "title-" + caseID,
		Query:     "query-" + caseID,
		Status:    types.AgentEvalCaseStatusPassed,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: createdAt.UTC(),
	}
}

func TestAgentEvalRun_CRUD_AndTenantIsolation(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	run := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusRunning, time.Now())
	require.NoError(t, repo.CreateRun(ctx, run))

	got, err := repo.GetRun(ctx, run.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, run.ID, got.ID)
	assert.Equal(t, types.AgentEvalRunStatusRunning, got.Status)
	assert.Equal(t, types.AgentEvalTriggerManual, got.Trigger)

	_, err = repo.GetRun(ctx, run.ID, 2)
	require.True(t, errors.Is(err, ErrAgentEvalRunNotFound))

	got.Status = types.AgentEvalRunStatusCompleted
	finishedAt := time.Now().UTC()
	got.FinishedAt = &finishedAt
	got.DurationMS = 1234
	require.NoError(t, repo.UpdateRun(ctx, got))

	saved, err := repo.GetRun(ctx, run.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, types.AgentEvalRunStatusCompleted, saved.Status)
	assert.Equal(t, int64(1234), saved.DurationMS)
	require.NotNil(t, saved.FinishedAt)
	assert.WithinDuration(t, finishedAt, *saved.FinishedAt, time.Second)
}

func TestAgentEval_FindRunningByAgent(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	run, err := repo.FindRunningByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional)
	require.NoError(t, err)
	assert.Nil(t, run)

	completed := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, time.Now())
	require.NoError(t, repo.CreateRun(ctx, completed))
	run, err = repo.FindRunningByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional)
	require.NoError(t, err)
	assert.Nil(t, run)

	running := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusRunning, time.Now())
	require.NoError(t, repo.CreateRun(ctx, running))
	run, err = repo.FindRunningByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, running.ID, run.ID)

	run, err = repo.FindRunningByAgent(ctx, 1, "agent-2", types.AgentEvalSuiteFunctional)
	require.NoError(t, err)
	assert.Nil(t, run)
}

func TestAgentEval_ListRunsByAgent_OrderAndLimit(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	base := time.Now().UTC().Add(-10 * time.Minute)
	r1 := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, base)
	r2 := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, base.Add(2*time.Minute))
	r3 := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusFailed, base.Add(4*time.Minute))
	otherAgent := makeEvalRun(1, "agent-2", types.AgentEvalRunStatusCompleted, base.Add(6*time.Minute))
	otherTenant := makeEvalRun(2, "agent-1", types.AgentEvalRunStatusCompleted, base.Add(8*time.Minute))
	for _, r := range []*types.AgentEvalRun{r1, r2, r3, otherAgent, otherTenant} {
		require.NoError(t, repo.CreateRun(ctx, r))
	}

	runs, err := repo.ListRunsByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional, 0)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	assert.Equal(t, []string{r3.ID, r2.ID, r1.ID}, []string{runs[0].ID, runs[1].ID, runs[2].ID})

	runs, err = repo.ListRunsByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional, 2)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, r3.ID, runs[0].ID)
	assert.Equal(t, r2.ID, runs[1].ID)
}

func TestAgentEval_ListRunningRuns(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	running1 := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusRunning, now)
	running2 := makeEvalRun(2, "agent-2", types.AgentEvalRunStatusRunning, now.Add(time.Minute))
	completed := makeEvalRun(1, "agent-3", types.AgentEvalRunStatusCompleted, now)
	for _, r := range []*types.AgentEvalRun{running1, running2, completed} {
		require.NoError(t, repo.CreateRun(ctx, r))
	}

	runs, err := repo.ListRunningRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, []string{running1.ID, running2.ID}, []string{runs[0].ID, runs[1].ID})
}

func TestAgentEval_DeleteRun_CascadeAndSentinel(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	run := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, now)
	require.NoError(t, repo.CreateRun(ctx, run))
	case1 := makeEvalCaseResult(1, run.ID, "smoke-01", now)
	case2 := makeEvalCaseResult(1, run.ID, "smoke-02", now.Add(time.Second))
	other := makeEvalRun(1, "agent-2", types.AgentEvalRunStatusCompleted, now)
	otherCase := makeEvalCaseResult(1, other.ID, "smoke-01", now)
	for _, c := range []*types.AgentEvalCaseResult{case1, case2, otherCase} {
		require.NoError(t, repo.CreateCaseResult(ctx, c))
	}
	require.NoError(t, repo.CreateRun(ctx, other))

	require.NoError(t, repo.DeleteRun(ctx, run.ID, 1))

	_, err := repo.GetRun(ctx, run.ID, 1)
	require.True(t, errors.Is(err, ErrAgentEvalRunNotFound))

	results, err := repo.ListCaseResultsByRun(ctx, run.ID, 1)
	require.NoError(t, err)
	assert.Empty(t, results)

	kept, err := repo.ListCaseResultsByRun(ctx, other.ID, 1)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, otherCase.ID, kept[0].ID)

	require.True(t, errors.Is(repo.DeleteRun(ctx, run.ID, 1), ErrAgentEvalRunNotFound))
	require.True(t, errors.Is(repo.DeleteRun(ctx, other.ID, 2), ErrAgentEvalRunNotFound))
}

func TestAgentEval_RecoverOrphanRuns(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	running := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusRunning, now)
	completed := makeEvalRun(1, "agent-2", types.AgentEvalRunStatusCompleted, now)
	require.NoError(t, repo.CreateRun(ctx, running))
	require.NoError(t, repo.CreateRun(ctx, completed))

	count, err := repo.RecoverOrphanRuns(ctx, "recovered after restart")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	got, err := repo.GetRun(ctx, running.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, types.AgentEvalRunStatusFailed, got.Status)
	assert.Equal(t, "recovered after restart", got.Error)
	require.NotNil(t, got.FinishedAt)

	untouched, err := repo.GetRun(ctx, completed.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, types.AgentEvalRunStatusCompleted, untouched.Status)
	assert.Empty(t, untouched.Error)
	assert.Nil(t, untouched.FinishedAt)

	count, err = repo.RecoverOrphanRuns(ctx, "again")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestAgentEval_PruneRunsByAgent(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	base := time.Now().UTC().Add(-30 * time.Minute)
	var kept, pruned []*types.AgentEvalRun
	for i := 0; i < 4; i++ {
		r := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, repo.CreateRun(ctx, r))
		require.NoError(t, repo.CreateCaseResult(ctx, makeEvalCaseResult(1, r.ID, "smoke-01", r.CreatedAt)))
		if i < 2 {
			pruned = append(pruned, r)
		} else {
			kept = append(kept, r)
		}
	}
	otherAgentOld := makeEvalRun(1, "agent-2", types.AgentEvalRunStatusCompleted, base)
	require.NoError(t, repo.CreateRun(ctx, otherAgentOld))

	count, err := repo.PruneRunsByAgent(ctx, 1, "agent-1", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	for _, r := range pruned {
		_, err := repo.GetRun(ctx, r.ID, 1)
		require.True(t, errors.Is(err, ErrAgentEvalRunNotFound))
		results, err := repo.ListCaseResultsByRun(ctx, r.ID, 1)
		require.NoError(t, err)
		assert.Empty(t, results)
	}
	for _, r := range kept {
		got, err := repo.GetRun(ctx, r.ID, 1)
		require.NoError(t, err)
		assert.Equal(t, r.ID, got.ID)
		results, err := repo.ListCaseResultsByRun(ctx, r.ID, 1)
		require.NoError(t, err)
		require.Len(t, results, 1)
	}

	otherGot, err := repo.GetRun(ctx, otherAgentOld.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, otherAgentOld.ID, otherGot.ID)

	count, err = repo.PruneRunsByAgent(ctx, 1, "agent-1", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestAgentEval_CaseResult_JSONRoundtripAndIsolation(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	run := makeEvalRun(1, "agent-1", types.AgentEvalRunStatusCompleted, now)
	require.NoError(t, repo.CreateRun(ctx, run))

	result := &types.AgentEvalCaseResult{
		ID:        uuid.New().String(),
		RunID:     run.ID,
		TenantID:  1,
		CaseID:    "smoke-02",
		CaseTitle: "知识库检索",
		Query:     "什么是 WeKnora？",
		Status:    types.AgentEvalCaseStatusPassed,
		Assertions: types.AgentEvalAssertionList{
			{Name: "contains_keyword", Expected: "WeKnora", Actual: "WeKnora 是知识库问答系统", Passed: true},
			{Name: "reference_count_gte", Expected: "1", Actual: "2", Passed: true},
		},
		Judge: types.AgentEvalJudgeList{
			{Metric: "answer_relevancy", Score: 0.92, Reason: "答案切题"},
		},
		ToolCalls: types.AgentEvalToolCallList{
			{ToolName: "knowledge_base_search", ToolCallID: "call-1"},
		},
		Answer:         "WeKnora 是知识库问答系统。",
		ReferenceCount: 2,
		DurationMS:     800,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, repo.CreateCaseResult(ctx, result))

	results, err := repo.ListCaseResultsByRun(ctx, run.ID, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	got := results[0]
	assert.Equal(t, "smoke-02", got.CaseID)
	assert.Equal(t, types.AgentEvalCaseStatusPassed, got.Status)
	require.Len(t, got.Assertions, 2)
	assert.Equal(t, "contains_keyword", got.Assertions[0].Name)
	assert.True(t, got.Assertions[0].Passed)
	assert.Equal(t, "WeKnora 是知识库问答系统", got.Assertions[0].Actual)
	require.Len(t, got.Judge, 1)
	assert.Equal(t, "answer_relevancy", got.Judge[0].Metric)
	assert.InDelta(t, 0.92, got.Judge[0].Score, 0.0001)
	assert.Equal(t, "答案切题", got.Judge[0].Reason)
	require.Len(t, got.ToolCalls, 1)
	assert.Equal(t, "knowledge_base_search", got.ToolCalls[0].ToolName)
	assert.Equal(t, 2, got.ReferenceCount)

	empty, err := repo.ListCaseResultsByRun(ctx, run.ID, 2)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestAgentEval_FindRunningByAgent_SuiteIsolation(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	// 同一 agent 下：功能套件有一条 running，安全套件也有一条 running
	require.NoError(t, repo.CreateRun(ctx, makeEvalRunWithSuite(1, "agent-1", types.AgentEvalRunStatusRunning, types.AgentEvalSuiteFunctional, time.Now())))
	require.NoError(t, repo.CreateRun(ctx, makeEvalRunWithSuite(1, "agent-1", types.AgentEvalRunStatusRunning, types.AgentEvalSuiteSecurity, time.Now())))

	// 查 functional 只命中 functional 那条
	run, err := repo.FindRunningByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.AgentEvalSuiteFunctional, run.Suite)

	// 查 security 只命中 security 那条
	run, err = repo.FindRunningByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteSecurity)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.AgentEvalSuiteSecurity, run.Suite)
}

func TestAgentEval_ListRunsByAgent_SuiteFilter(t *testing.T) {
	db := setupAgentEvalTestDB(t)
	repo := NewAgentEvalRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateRun(ctx, makeEvalRunWithSuite(1, "agent-1", types.AgentEvalRunStatusCompleted, types.AgentEvalSuiteFunctional, time.Now().Add(-time.Hour))))
	require.NoError(t, repo.CreateRun(ctx, makeEvalRunWithSuite(1, "agent-1", types.AgentEvalRunStatusCompleted, types.AgentEvalSuiteSecurity, time.Now())))

	runs, err := repo.ListRunsByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteSecurity, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, types.AgentEvalSuiteSecurity, runs[0].Suite)

	runs, err = repo.ListRunsByAgent(ctx, 1, "agent-1", types.AgentEvalSuiteFunctional, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, types.AgentEvalSuiteFunctional, runs[0].Suite)
}
