package workbench

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 模型正常返回补丁 JSON（带 Markdown 围栏与开场白）时能被解析并校验。
func TestDatabasePatch_ParsesFencedJSON(t *testing.T) {
	content := "好的，这是补丁：\n```json\n" +
		`{"ops":[{"op":"add_property","name":"风险等级","type":"select","options":["高","低"]},` +
		`{"op":"add_row","title":"登录页改版","cells":{"状态":"进行中"}}]}` + "\n```"
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1", chunks: []string{content}},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"item-1": true})

	result, err := svc.DatabasePatch(inlineCtx(), types.WorkbenchAIDatabaseRequest{
		ItemID: "item-1",
		Prompt: "加一列风险等级，并补一行登录页改版",
		Columns: []types.WorkbenchAIDatabaseColumn{
			{ID: "c-status", Name: "状态", Type: "select"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "m1", result.ModelID)
	require.Len(t, result.Patch.Ops, 2)
	assert.Equal(t, types.WorkbenchAIDatabaseOpAddProperty, result.Patch.Ops[0].Op)
	assert.Equal(t, "登录页改版", result.Patch.Ops[1].Title)
}

// 模型输出不是 JSON / 补丁非法：按 2410 返回，前端提示重新生成。
func TestDatabasePatch_RejectsInvalidModelOutput(t *testing.T) {
	cases := map[string]string{
		"非 JSON":  "我觉得你应该加一列风险等级。",
		"坏了":     `{"ops":[{"op":"drop_table"}]}`,
		"空 ops":  `{"ops":[]}`,
		"截断的 JSON": `{"ops":[{"op":"add_row"`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			models := &fakeModels{
				model:  &fakeChat{modelID: "m1", chunks: []string{content}},
				models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
			}
			svc := newInlineService(models, &fakeAgents{}, map[string]bool{"item-1": true})
			_, err := svc.DatabasePatch(inlineCtx(), types.WorkbenchAIDatabaseRequest{
				ItemID: "item-1", Prompt: "随便加点什么",
			})
			require.Error(t, err)
			assert.Equal(t, 2410, int(mustAppError(t, err).Code))
		})
	}
}

// item_id 必填且必须是自己的条目；别人的条目 2400。
func TestDatabasePatch_RequiresOwnedItem(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1"},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"mine": true})

	_, err := svc.DatabasePatch(inlineCtx(), types.WorkbenchAIDatabaseRequest{Prompt: "加一列"})
	require.Error(t, err)
	assert.Equal(t, 2400, int(mustAppError(t, err).Code))

	_, err = svc.DatabasePatch(inlineCtx(), types.WorkbenchAIDatabaseRequest{
		ItemID: "someone-else", Prompt: "加一列",
	})
	require.Error(t, err)
	assert.Equal(t, 2400, int(mustAppError(t, err).Code))
}

// 无可用模型 → 2408，不会等到模型输出才发现。
func TestDatabasePatch_NoModel(t *testing.T) {
	svc := newInlineService(&fakeModels{models: []*types.Model{}}, &fakeAgents{}, map[string]bool{"i": true})
	_, err := svc.DatabasePatch(inlineCtx(), types.WorkbenchAIDatabaseRequest{ItemID: "i", Prompt: "加一列"})
	require.Error(t, err)
	assert.Equal(t, 2408, int(mustAppError(t, err).Code))
}

// 提示词里必须带上列 id 与行标题：模型要能引用它们写 set_cell。
func TestBuildDatabaseMessages_IncludesSchemaAndRows(t *testing.T) {
	messages := buildDatabaseMessages(types.WorkbenchAIDatabaseRequest{
		Prompt: "把空的负责人补上",
		Columns: []types.WorkbenchAIDatabaseColumn{
			{ID: "c-owner", Name: "负责人", Type: "text"},
			{ID: "c-status", Name: "状态", Type: "select"},
		},
		Rows:      []types.WorkbenchAIDatabaseRow{{ID: "r1", Title: "登录页改版", Cells: map[string]string{"c-status": "进行中"}}},
		Truncated: true,
	}, nil)
	require.Len(t, messages, 2)
	assert.Equal(t, "system", messages[0].Role)
	user := messages[1].Content
	assert.Contains(t, user, "id=c-owner")
	assert.Contains(t, user, "标题=登录页改版")
	assert.Contains(t, user, "状态=进行中")
	assert.Contains(t, user, "只是部分行", "截断时必须显式提示")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(user), ">>>"))
}

// 空表也要给出明确说明，避免模型拿不到列结构。
func TestDescribeColumns_Empty(t *testing.T) {
	assert.Equal(t, "（空表，没有列）", describeColumns(nil))
}

func TestExtractJSONObject(t *testing.T) {
	assert.Equal(t, `{"ops":[]}`, extractJSONObject("前缀 {\"ops\":[]} 后缀"))
	assert.Equal(t, "", extractJSONObject("没有大括号"))
	assert.Equal(t, "", extractJSONObject(""))
}

// 上下文缺租户/用户时不进入模型调用。
func TestDatabasePatch_RequiresCaller(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.DatabasePatch(context.Background(), types.WorkbenchAIDatabaseRequest{ItemID: "i", Prompt: "x"})
	require.Error(t, err)
}
