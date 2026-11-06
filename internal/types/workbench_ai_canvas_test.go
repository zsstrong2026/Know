package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 请求校验 ----

func TestWorkbenchAICanvasRequest_Validate(t *testing.T) {
	req := &WorkbenchAICanvasRequest{ItemID: "i1", Prompt: "   "}
	assert.ErrorIs(t, req.Validate(), ErrWorkbenchAICanvasEmptyPrompt)

	req.Prompt = strings.Repeat("字", WorkbenchAICanvasPromptMaxRunes+1)
	err := req.Validate()
	require.Error(t, err)
	var lengthErr *WorkbenchAILengthError
	require.ErrorAs(t, err, &lengthErr)
	assert.Equal(t, "prompt", lengthErr.Field)
}

// 元素超过上限时截断并标记 truncated：只是参考材料，不拒绝请求。
func TestWorkbenchAICanvasRequest_NormalizeTruncatesElements(t *testing.T) {
	req := &WorkbenchAICanvasRequest{Prompt: " 按主题分组 ", AgentID: " a "}
	for index := 0; index < WorkbenchAICanvasMaxElements+5; index++ {
		req.Elements = append(req.Elements, WorkbenchAICanvasElement{ID: "e", Text: "x"})
	}
	req.Normalize()
	assert.Equal(t, "按主题分组", req.Prompt)
	assert.Len(t, req.Elements, WorkbenchAICanvasMaxElements)
	assert.True(t, req.Truncated)
}

// 元素文本按 rune 截断，中文不会被切成半个字节。
func TestWorkbenchAICanvasRequest_NormalizeTruncatesText(t *testing.T) {
	req := &WorkbenchAICanvasRequest{
		Prompt:   "整理",
		Elements: []WorkbenchAICanvasElement{{ID: "e1", Text: strings.Repeat("长", WorkbenchAICanvasMaxTextRunes+10)}},
	}
	req.Normalize()
	assert.Equal(t, WorkbenchAICanvasMaxTextRunes, len([]rune(req.Elements[0].Text)))
}

// ---- 补丁校验 ----

func TestWorkbenchAICanvasPatch_AcceptsWellFormedPatch(t *testing.T) {
	patch := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpCreateFrame, Title: "本周", X: 0, Y: 0, W: 420, H: 320, MemberIDs: []string{"e1", "e2"}},
		{Op: WorkbenchAICanvasOpMoveElement, ID: "e1", X: 20, Y: 20},
	}}
	require.NoError(t, patch.Validate())
}

// 空 ops 是合法结果：模型认为不需要动。
func TestWorkbenchAICanvasPatch_EmptyIsValid(t *testing.T) {
	require.NoError(t, WorkbenchAICanvasPatch{}.Validate())
}

func TestWorkbenchAICanvasPatch_RejectsUnknownOp(t *testing.T) {
	patch := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{{Op: "delete_element", ID: "e1"}}}
	err := patch.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkbenchAICanvasBadPatch)
}

// 破坏性字段没有入口：只有 create_frame / move_element 两种 op 能过校验。
func TestWorkbenchAICanvasPatch_RejectsTooManyOps(t *testing.T) {
	patch := WorkbenchAICanvasPatch{}
	for index := 0; index < WorkbenchAICanvasMaxOps+1; index++ {
		patch.Ops = append(patch.Ops, WorkbenchAICanvasPatchOp{Op: WorkbenchAICanvasOpMoveElement, ID: "e1"})
	}
	assert.ErrorIs(t, patch.Validate(), ErrWorkbenchAICanvasBadPatch)
}

func TestWorkbenchAICanvasPatch_RejectsBadFrame(t *testing.T) {
	// 尺寸过小
	small := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpCreateFrame, W: 10, H: 320},
	}}
	assert.ErrorIs(t, small.Validate(), ErrWorkbenchAICanvasBadPatch)

	// 坐标越界
	far := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpCreateFrame, X: 1e9, W: 420, H: 320},
	}}
	assert.ErrorIs(t, far.Validate(), ErrWorkbenchAICanvasBadPatch)

	// member id 非法（含空格/注入字符）
	badID := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpCreateFrame, W: 420, H: 320, MemberIDs: []string{"e1; drop"}},
	}}
	assert.ErrorIs(t, badID.Validate(), ErrWorkbenchAICanvasBadPatch)

	// 成员过多
	tooMany := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		func() WorkbenchAICanvasPatchOp {
			op := WorkbenchAICanvasPatchOp{Op: WorkbenchAICanvasOpCreateFrame, W: 420, H: 320}
			for index := 0; index < WorkbenchAICanvasMaxMembersPerOp+1; index++ {
				op.MemberIDs = append(op.MemberIDs, "e1")
			}
			return op
		}(),
	}}
	assert.ErrorIs(t, tooMany.Validate(), ErrWorkbenchAICanvasBadPatch)
}

func TestWorkbenchAICanvasPatch_RejectsBadMove(t *testing.T) {
	missingID := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpMoveElement, X: 10, Y: 10},
	}}
	assert.ErrorIs(t, missingID.Validate(), ErrWorkbenchAICanvasBadPatch)

	outOfRange := WorkbenchAICanvasPatch{Ops: []WorkbenchAICanvasPatchOp{
		{Op: WorkbenchAICanvasOpMoveElement, ID: "e1", X: 1e9, Y: 0},
	}}
	assert.ErrorIs(t, outOfRange.Validate(), ErrWorkbenchAICanvasBadPatch)
}
