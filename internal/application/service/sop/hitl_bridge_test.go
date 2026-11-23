package sop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// newBridgeBroker 建内存 broker（无 redis、自动过 kind 校验）。
func newBridgeBroker(t *testing.T) *hitl.Broker {
	t.Helper()
	b := hitl.NewBroker(nil, nil)
	b.RegisterKind(types.AgentPromptKindAskUser, types.ValidateAskUserResponse)
	return b
}

func TestPromptBridgeAnswersAskUserByOption(t *testing.T) {
	broker := newBridgeBroker(t)
	ctx := context.Background()
	prompt := &types.AgentPrompt{
		ID:       "p1",
		TenantID: 1, SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		Request: types.JSON(`{"question":"用哪个方案？","options":["方案A","方案B"]}`),
		Status:  types.AgentPromptSuspended,
	}
	require.NoError(t, broker.Store().Create(ctx, prompt))

	bridge := NewPromptBridge(broker)
	require.NoError(t, bridge.Answer(ctx, engine.PromptAnswer{
		TenantID: 1, SessionID: "s1", PromptID: prompt.ID, Text: "方案A",
	}))
	row, err := broker.Store().Get(ctx, prompt.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, row.Status)
	var resp types.AskUserPromptResponse
	require.NoError(t, json.Unmarshal(row.Response, &resp))
	require.Equal(t, []string{"方案A"}, resp.Selected)
}

func TestPromptBridgeRejectsUnknownOptionWhenNoFreeText(t *testing.T) {
	broker := newBridgeBroker(t)
	ctx := context.Background()
	prompt := &types.AgentPrompt{
		ID:       "p2",
		TenantID: 1, SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		Request: types.JSON(`{"question":"二选一","options":["甲","乙"]}`),
		Status:  types.AgentPromptSuspended,
	}
	require.NoError(t, broker.Store().Create(ctx, prompt))
	err := NewPromptBridge(broker).Answer(ctx, engine.PromptAnswer{
		TenantID: 1, SessionID: "s1", PromptID: prompt.ID, Text: "丙",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "只接受选项")
}

func TestPromptBridgeLocatesOpenPromptBySession(t *testing.T) {
	broker := newBridgeBroker(t)
	ctx := context.Background()
	require.NoError(t, broker.Store().Create(ctx, &types.AgentPrompt{
		ID: "p-old", TenantID: 1, SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		Request: types.JSON(`{"question":"q1","allow_free_text":true}`),
		Status:  types.AgentPromptAnswered, CreatedAt: time.Now().Add(-time.Hour),
	}))
	require.NoError(t, broker.Store().Create(ctx, &types.AgentPrompt{
		ID: "p-new", TenantID: 1, SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		Request: types.JSON(`{"question":"q2","allow_free_text":true}`),
		Status:  types.AgentPromptSuspended, CreatedAt: time.Now(),
	}))

	id, err := NewPromptBridge(broker).PromptIDBySession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "p-new", id, "取最新一条尚未答复的提示")
}

func TestPromptBridgeMissingPromptFails(t *testing.T) {
	broker := newBridgeBroker(t)
	err := NewPromptBridge(broker).Answer(context.Background(), engine.PromptAnswer{
		TenantID: 1, SessionID: "s-none", Text: "x",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "没有等待答复")
}

// 未装配 broker 时：读取返回空（不挂起续跑），答复报清楚地报错。
func TestBridgesWithoutBroker(t *testing.T) {
	got, err := NewSuspensionBridge(nil).GetBySession(context.Background(), "s1")
	require.NoError(t, err)
	require.Nil(t, got)

	err = NewPromptBridge(nil).Answer(context.Background(), engine.PromptAnswer{TenantID: 1, SessionID: "s1", Text: "x"})
	require.Error(t, err)
}

func TestSuspensionBridgeReturnsAnsweredSnapshot(t *testing.T) {
	broker := hitl.NewBroker(nil, nil)
	ctx := context.Background()
	require.NoError(t, broker.Store().Create(ctx, &types.AgentPrompt{
		ID: "p1", TenantID: 1, SessionID: "s1", AssistantMessageID: "m1",
		Kind: types.AgentPromptKindAskUser, Status: types.AgentPromptAnswered,
	}))
	require.NoError(t, broker.Suspensions().Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m1", TenantID: 1, SessionID: "s1", PromptID: "p1",
		Snapshot: types.JSON(`{"q":"hi"}`),
	}))

	got, err := NewSuspensionBridge(broker).GetBySession(ctx, "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "p1", got.Prompt.ID)
	require.JSONEq(t, `{"q":"hi"}`, string(got.Snapshot))
}
