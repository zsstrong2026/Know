package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSubmitNodeResultParsesFourVerdicts(t *testing.T) {
	for _, v := range []string{"pass", "rework", "fail", "human"} {
		res, err := ParseSubmitArgs(map[string]any{"result": v, "reason": "r", "output": "o"})
		require.NoError(t, err)
		require.Equal(t, v, res.GateResult)
		require.Equal(t, "r", res.GateReason)
		require.Equal(t, "o", res.Output)
	}
	_, err := ParseSubmitArgs(map[string]any{"result": "ok"})
	require.Error(t, err) // 白话：结果只能是 通过/打回/失败/转人工
	_, err = ParseSubmitArgs(map[string]any{"result": "rework"})
	require.Error(t, err) // rework 必须带 reason
	_, err = ParseSubmitArgs(map[string]any{"result": "human", "reason": " "})
	require.Error(t, err) // 空白 reason 视同缺失
}

func TestSubmitToolRejectsNonSOPSession(t *testing.T) {
	tool := NewSubmitTool(stubSessionGetter{desc: "普通会话"})
	_, err := tool.Invoke(context.Background(), map[string]any{"result": "pass"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SOP")
}

func TestSubmitToolRejectsSessionWithoutMarkerFromStore(t *testing.T) {
	tool := NewSubmitTool(stubSessionGetter{desc: "普通会话"})
	ctx := WithSubmitContext(context.Background(), &SubmitContext{SessionID: "s1"})
	_, err := tool.Invoke(ctx, map[string]any{"result": "pass"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SOP")
}

func TestSubmitToolStoresSlotAndWritesGateFile(t *testing.T) {
	root := t.TempDir()
	slots := NewResultSlots()
	tool := NewSubmitTool(nil)
	ctx := WithSubmitContext(context.Background(), &SubmitContext{
		SessionID:          "s1",
		SessionDescription: types.SOPSessionMarker + "run=r1 node=n1",
		Slots:              slots,
		SlotKey:            "r1/n1",
		WorkspaceRoot:      root,
		NodeID:             "n1",
		Attempt:            2,
	})
	receipt, err := tool.Invoke(ctx, map[string]any{
		"result": "rework", "reason": "缺少测试", "output": "正文",
	})
	require.NoError(t, err)
	require.Contains(t, receipt.Message, "rework")

	got, ok := slots.Take("r1/n1")
	require.True(t, ok)
	require.Equal(t, types.SopGateRework, got.GateResult)
	require.Equal(t, "缺少测试", got.GateReason)

	path := filepath.Join(root, ".sop", "gate-results", "n1", "attempt-2.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"rework"`)
}

func TestParseSubmitArgsRejectsNonStringValues(t *testing.T) {
	_, err := ParseSubmitArgs(map[string]any{"result": 42})
	require.Error(t, err)
	_, err = ParseSubmitArgs(nil)
	require.Error(t, err)
}

// stubSessionGetter 按 id 返回固定描述（测试用）。
type stubSessionGetter struct{ desc string }

func (s stubSessionGetter) GetSessionByID(_ context.Context, _ uint64, id string) (*types.Session, error) {
	return &types.Session{ID: id, Description: s.desc}, nil
}
