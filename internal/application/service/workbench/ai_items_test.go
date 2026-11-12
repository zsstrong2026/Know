package workbench

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 模型正常返回候选 JSON（带代码围栏与开场白）时能被解析与归一。
func TestExtractItems_ParsesFencedJSON(t *testing.T) {
	content := "好的：\n```json\n" +
		`{"items":[{"title":"  补充部署文档  ","owner":"李昀","due":"2026-11-06","status":"待开始","priority":"P1",` +
		`"evidence":"需要在周五前补充部署文档","confidence":1.4}]}` + "\n```"
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1", chunks: []string{content}},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"doc-1": true})

	result, err := svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{
		ItemID:        "doc-1",
		Markdown:      "## 计划\n需要在周五前补充部署文档",
		StatusOptions: []string{"待开始", "已完成"},
		Limit:         5,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "补充部署文档", result.Items[0].Title, "标题必须去空白")
	assert.Equal(t, "李昀", result.Items[0].Owner)
	assert.Equal(t, "2026-11-06", result.Items[0].Due)
	assert.Equal(t, 1.0, result.Items[0].Confidence, "置信度必须夹到 0–1")
	assert.Equal(t, "m1", result.ModelID)
}

// 模型输出非法 / 空候选：按 2410 返回，前端提示重新识别。
func TestExtractItems_RejectsInvalidOutput(t *testing.T) {
	cases := map[string]string{
		"非 JSON":   "我识别到三件事。",
		"空 items":  `{"items":[]}`,
		"标题为空":     `{"items":[{"title":"  "}]}`,
		"缺 items 键": `{"foo":1}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			models := &fakeModels{
				model:  &fakeChat{modelID: "m1", chunks: []string{content}},
				models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
			}
			svc := newInlineService(models, &fakeAgents{}, map[string]bool{"doc-1": true})
			_, err := svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{
				ItemID: "doc-1", Markdown: "正文",
			})
			require.Error(t, err)
			assert.Equal(t, 2410, int(mustAppError(t, err).Code))
		})
	}
}

// 枚举值不在允许集合内时清空而不是整批作废；日期格式非法也清空。
func TestExtractItems_NormalizesEnumsAndDates(t *testing.T) {
	content := `{"items":[{"title":"补充文档","status":"紧急","priority":"最高","due":"11/06"}]}`
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1", chunks: []string{content}},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"doc-1": true})
	result, err := svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{
		ItemID:          "doc-1",
		Markdown:        "正文",
		StatusOptions:   []string{"待开始", "已完成"},
		PriorityOptions: []string{"P0", "P1"},
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "", result.Items[0].Status, "不在候选值里的状态要清空")
	assert.Equal(t, "", result.Items[0].Priority, "不在候选值里的优先级要清空")
	assert.Equal(t, "", result.Items[0].Due, "非 YYYY-MM-DD 的日期要清空")
}

// 来源文档必须是自己的条目；没传正文直接拒绝。
func TestExtractItems_RequiresOwnedItemAndMarkdown(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1"},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"mine": true})

	_, err := svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{ItemID: "mine"})
	require.Error(t, err)
	// 缺正文走通用参数错误（400），不是条目不存在。
	assert.NotEqual(t, 2400, int(mustAppError(t, err).Code))

	_, err = svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{
		ItemID: "someone-else", Markdown: "正文",
	})
	require.Error(t, err)
	assert.Equal(t, 2400, int(mustAppError(t, err).Code))
}

// 候选超过 limit 时截断，避免一次写入过多行。
func TestExtractItems_TruncatesToLimit(t *testing.T) {
	content := `{"items":[{"title":"A"},{"title":"B"},{"title":"C"}]}`
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1", chunks: []string{content}},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"doc-1": true})
	result, err := svc.ExtractItems(inlineCtx(), types.WorkbenchAIItemsRequest{
		ItemID: "doc-1", Markdown: "正文", Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, result.Items, 2)
}

// 提示词里必须带上正文、候选值与已有标题：模型靠这些做判断与去重。
func TestBuildItemsMessages_IncludesContext(t *testing.T) {
	messages := buildItemsMessages(types.WorkbenchAIItemsRequest{
		SourceTitle:     "季度计划",
		Markdown:        "## 目标\n月底前完成上线",
		StatusOptions:   []string{"待开始"},
		PriorityOptions: []string{"P0"},
		ExistingTitles:  []string{"旧事项"},
		Limit:           7,
	}, nil)
	require.Len(t, messages, 2)
	assert.Equal(t, "system", messages[0].Role)
	user := messages[1].Content
	assert.Contains(t, user, "季度计划")
	assert.Contains(t, user, "月底前完成上线")
	assert.Contains(t, user, "待开始")
	assert.Contains(t, user, "P0")
	assert.Contains(t, user, "旧事项")
	assert.Contains(t, user, "7")
}

// 缺身份时不进入模型调用。
func TestExtractItems_RequiresCaller(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.ExtractItems(context.Background(), types.WorkbenchAIItemsRequest{
		ItemID: "i", Markdown: "正文",
	})
	require.Error(t, err)
}
