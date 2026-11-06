package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 请求校验 ----

func TestWorkbenchAIDatabaseRequest_Validate(t *testing.T) {
	req := &WorkbenchAIDatabaseRequest{ItemID: "i1", Prompt: "   "}
	assert.ErrorIs(t, req.Validate(), ErrWorkbenchAIDatabaseEmptyPrompt)

	req.Prompt = strings.Repeat("字", WorkbenchAIDatabasePromptMaxRunes+1)
	err := req.Validate()
	require.Error(t, err)
	var lengthErr *WorkbenchAILengthError
	require.ErrorAs(t, err, &lengthErr)
	assert.Equal(t, "prompt", lengthErr.Field)
}

// 行快照超过上限时截断并标记 truncated：只是参考材料，不拒绝请求。
func TestWorkbenchAIDatabaseRequest_NormalizeTruncatesRows(t *testing.T) {
	req := &WorkbenchAIDatabaseRequest{Prompt: " 填充 ", AgentID: " a "}
	for index := 0; index < WorkbenchAIDatabaseMaxRows+5; index++ {
		req.Rows = append(req.Rows, WorkbenchAIDatabaseRow{ID: "r"})
	}
	req.Normalize()
	assert.Equal(t, "填充", req.Prompt)
	assert.Equal(t, "a", req.AgentID)
	assert.Len(t, req.Rows, WorkbenchAIDatabaseMaxRows)
	assert.True(t, req.Truncated)
}

// ---- 补丁校验 ----

func TestWorkbenchAIDatabasePatch_ValidateAcceptsThreeOps(t *testing.T) {
	patch := &WorkbenchAIDatabasePatch{Ops: []WorkbenchAIDatabasePatchOp{
		{Op: WorkbenchAIDatabaseOpAddProperty, Name: "风险等级", Type: "select", Options: []string{"高", "低"}},
		{Op: WorkbenchAIDatabaseOpAddRow, Title: "登录页改版", Cells: map[string]string{"状态": "进行中"}},
		{Op: WorkbenchAIDatabaseOpSetCell, RowID: "r1", Column: "负责人", Value: "张三"},
	}}
	assert.NoError(t, patch.Validate())
}

func TestWorkbenchAIDatabasePatch_ValidateRejects(t *testing.T) {
	cases := map[string]*WorkbenchAIDatabasePatch{
		"空补丁": {},
		"未知 op": {Ops: []WorkbenchAIDatabasePatchOp{{Op: "delete_column", Name: "x"}}},
		"列类型不支持": {Ops: []WorkbenchAIDatabasePatchOp{
			{Op: WorkbenchAIDatabaseOpAddProperty, Name: "附件", Type: "attachment"},
		}},
		"列重名": {Ops: []WorkbenchAIDatabasePatchOp{
			{Op: WorkbenchAIDatabaseOpAddProperty, Name: "备注", Type: "text"},
			{Op: WorkbenchAIDatabaseOpAddProperty, Name: " 备注 ", Type: "text"},
		}},
		"set_cell 缺 row_id": {Ops: []WorkbenchAIDatabasePatchOp{
			{Op: WorkbenchAIDatabaseOpSetCell, Column: "负责人", Value: "张三"},
		}},
		"set_cell 缺列": {Ops: []WorkbenchAIDatabasePatchOp{
			{Op: WorkbenchAIDatabaseOpSetCell, RowID: "r1", Value: "张三"},
		}},
		"单元格超长": {Ops: []WorkbenchAIDatabasePatchOp{
			{Op: WorkbenchAIDatabaseOpAddRow, Title: strings.Repeat("字", WorkbenchAIDatabaseMaxCellRunes+1)},
		}},
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			err := patch.Validate()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrWorkbenchAIDatabaseBadPatch)
		})
	}
}

// 新建行上限 100 与 §11.7 一致；超过即整包拒绝。
func TestWorkbenchAIDatabasePatch_ValidateRowLimit(t *testing.T) {
	patch := &WorkbenchAIDatabasePatch{}
	for index := 0; index < 101; index++ {
		patch.Ops = append(patch.Ops, WorkbenchAIDatabasePatchOp{Op: WorkbenchAIDatabaseOpAddRow})
	}
	err := patch.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many new rows")
}
