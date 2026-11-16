package workbench

import (
	"context"
	"strconv"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relationsRepo 为智能关联提供三类召回所需的最小仓储面。
//
// 内嵌组合接口：未实现的方法保持 nil，一旦被误调用就会 panic，
// 正好暴露「关联车道悄悄用了别的仓储方法」这种越界。
type relationsRepo struct {
	interfaces.WorkbenchRepository
	source     *types.WorkbenchItem
	candidates []*types.WorkbenchRelationCandidate
	mentions   []string
	sharedTags []string
	backlinks  []*types.WorkbenchBacklink
}

func (r *relationsRepo) GetItemMeta(
	_ context.Context, _ uint64, _ string, id string,
) (*types.WorkbenchItem, error) {
	if r.source == nil || r.source.ID != id {
		return nil, assert.AnError
	}
	return r.source, nil
}

func (r *relationsRepo) ListRelationCandidates(
	_ context.Context, _ uint64, _ string, _ string, _ int,
) ([]*types.WorkbenchRelationCandidate, error) {
	return r.candidates, nil
}

func (r *relationsRepo) ListItemIDsMentioning(
	_ context.Context, _ uint64, _, _, _ string, _ int,
) ([]string, error) {
	return r.mentions, nil
}

func (r *relationsRepo) ListItemIDsBySharedTag(
	_ context.Context, _ uint64, _, _ string, _ int,
) ([]string, error) {
	return r.sharedTags, nil
}

func (r *relationsRepo) ListBacklinks(
	_ context.Context, _ uint64, _, _ string, _ int,
) ([]*types.WorkbenchBacklink, error) {
	return r.backlinks, nil
}

func newRelationsService(
	repo *relationsRepo, models aiModelProvider,
) *AIService {
	return NewAIService(repo, models, &fakeAgents{}, nil)
}

func relationCandidate(id, title, digest string) *types.WorkbenchRelationCandidate {
	return &types.WorkbenchRelationCandidate{
		ID:        id,
		Title:     title,
		Digest:    digest,
		UpdatedAt: time.Now().UTC(),
		TagIDs:    []string{},
	}
}

// ---- 召回打分 ----

// 提及 > 标签 > 关键词：三类信号同时命中时分数叠加，且来源按优先级记录主来源。
func TestRankRelationCandidates_ScoresAndPriority(t *testing.T) {
	source := &types.WorkbenchItem{
		ID:          "doc-1",
		Title:       "接口超时口径",
		TextContent: "接口超时统一按 3 秒计算，网关层兜底重试两次。",
	}
	candidates := []*types.WorkbenchRelationCandidate{
		relationCandidate("doc-2", "网关重试方案", "网关层重试与超时口径说明"),
		relationCandidate("doc-3", "完全无关的排版规范", "字号与行距"),
		relationCandidate("doc-4", "周报", "本周进展"),
	}
	mentions := map[string]struct{}{"doc-4": {}}
	sharedTags := map[string]struct{}{"doc-2": {}}

	ranked, truncated := rankRelationCandidates(
		source, candidates, mentions, sharedTags, map[string]struct{}{"doc-1": {}})

	require.False(t, truncated)
	require.Len(t, ranked, 2, "零分候选必须被丢弃")
	assert.Equal(t, "doc-4", ranked[0].ID, "标题提及权重最高，应排第一")
	assert.Equal(t, relationScoreMention, ranked[0].MatchScore)
	assert.Equal(t, "doc-2", ranked[1].ID)
	assert.GreaterOrEqual(t, ranked[1].MatchScore, relationScoreTag, "共享标签至少 5 分")
	assert.Contains(t, ranked[1].MatchedBy, types.WorkbenchRelationSourceKeyword)
}

// 关键词命中再多也不能压过标题提及：这是「用户主动引用」优先于「用词重合」的保证。
func TestRankRelationCandidates_KeywordNeverOutranksMention(t *testing.T) {
	source := &types.WorkbenchItem{
		ID:          "doc-1",
		Title:       "接口超时口径",
		TextContent: "接口超时统一按 3 秒计算，网关层兜底重试两次，监控告警阈值另定。",
	}
	candidates := []*types.WorkbenchRelationCandidate{
		// 摘要几乎复述整段正文，关键词命中会非常多
		relationCandidate("doc-2", "接口超时口径与网关层兜底重试",
			"接口超时统一按 3 秒计算，网关层兜底重试两次，监控告警阈值另定"),
		relationCandidate("doc-3", "周会记录", "本周进展"),
	}
	ranked, _ := rankRelationCandidates(
		source, candidates,
		map[string]struct{}{"doc-3": {}},
		nil,
		map[string]struct{}{"doc-1": {}})
	require.Len(t, ranked, 2)
	assert.Equal(t, "doc-3", ranked[0].ID, "提及信号必须压过大量关键词重合")
	assert.Equal(t, "doc-2", ranked[1].ID)
}

// 排除项（已应用 / 已忽略）不参与打分；主来源按提及 > 标签 > 关键词取。
func TestRankRelationCandidates_RespectsExcludeAndPrimarySource(t *testing.T) {
	source := &types.WorkbenchItem{ID: "doc-1", Title: "方案 A", TextContent: "方案 A 的约束"}
	candidates := []*types.WorkbenchRelationCandidate{
		relationCandidate("doc-2", "方案 A 补充", "补充说明"),
	}
	ranked, _ := rankRelationCandidates(
		source, candidates,
		map[string]struct{}{"doc-2": {}},
		map[string]struct{}{"doc-2": {}},
		map[string]struct{}{"doc-1": {}, "doc-2": {}})
	assert.Empty(t, ranked, "已排除的条目不得进入短名单")

	ranked, _ = rankRelationCandidates(
		source, candidates,
		map[string]struct{}{"doc-2": {}},
		map[string]struct{}{"doc-2": {}},
		map[string]struct{}{"doc-1": {}})
	require.Len(t, ranked, 1)
	assert.Equal(t, types.WorkbenchRelationSourceMention,
		primaryRecallSource(ranked[0].MatchedBy, ""), "提及优先于标签与关键词")
}

// meetingTemplateDocs 构造两篇同一模板产出的会议纪要：骨架一致，填写项略有差异。
func meetingTemplateDocs() (*types.WorkbenchItem, *types.WorkbenchRelationCandidate) {
	source := &types.WorkbenchItem{
		ID:    "doc-1",
		Title: "会议纪要",
		TextContent: "# 会议纪要\n## 会议信息\n时间：\n地点：\n参会人：\n主持：\n" +
			"## 议题\n- 议题一：\n- 议题二：\n## 讨论与结论\n- 结论一：\n" +
			"## 行动项\n- 负责人：戴格明（2026-11-16）\n## 下次会议\n时间与议题待定。",
	}
	candidate := relationCandidate("doc-2", "会议纪要",
		"# 会议纪要\n## 会议信息\n时间：\n地点：\n## 议题\n- 议题一：\n"+
			"## 行动项\n- 负责人：戴格明（2026-11-16）\n## 下次会议\n时间与议题待定。")
	return source, candidate
}

// 同一模板产出的文档：骨架与占位内容高度重合，关键词重合不再作为关联依据。
func TestRankRelationCandidates_IgnoresSameTemplateKeywordOverlap(t *testing.T) {
	source, candidate := meetingTemplateDocs()

	ranked, _ := rankRelationCandidates(
		source, []*types.WorkbenchRelationCandidate{candidate},
		nil, nil, map[string]struct{}{"doc-1": {}})

	assert.Empty(t, ranked, "只有模板相似时不得产生关联候选")
}

// 当前文档多出画布帧标题时，骨架判定仍要认得同模板候选（骨架取重合度，不受额外章节影响）。
func TestRankRelationCandidates_SameTemplateWithExtraHeadings(t *testing.T) {
	source, candidate := meetingTemplateDocs()
	source.TextContent += "\n## 1231651651\n\n## 150515656\n"

	ranked, _ := rankRelationCandidates(
		source, []*types.WorkbenchRelationCandidate{candidate},
		nil, nil, map[string]struct{}{"doc-1": {}})

	assert.Empty(t, ranked)
}

// 同模板但存在用户主动信号（提及 / 标签）时仍然保留，只是不再叠加关键词分。
func TestRankRelationCandidates_SameTemplateKeepsExplicitSignals(t *testing.T) {
	source, candidate := meetingTemplateDocs()
	mentions := map[string]struct{}{"doc-2": {}}

	ranked, _ := rankRelationCandidates(
		source, []*types.WorkbenchRelationCandidate{candidate},
		mentions, nil, map[string]struct{}{"doc-1": {}})

	require.Len(t, ranked, 1)
	assert.Equal(t, relationScoreMention, ranked[0].MatchScore)
	assert.NotContains(t, ranked[0].MatchedBy, types.WorkbenchRelationSourceKeyword,
		"模板骨架带来的关键词重合不参与打分")
}

// 短名单超限时按分数截断，并标记 truncated。
func TestRankRelationCandidates_TruncatesShortlist(t *testing.T) {
	source := &types.WorkbenchItem{ID: "doc-0", Title: "主题", TextContent: "主题正文"}
	candidates := make([]*types.WorkbenchRelationCandidate, 0, types.WorkbenchAIRelationsShortlist+5)
	mentions := make(map[string]struct{})
	for i := 0; i < types.WorkbenchAIRelationsShortlist+5; i++ {
		id := "doc-" + strconv.Itoa(i+1)
		candidates = append(candidates, relationCandidate(id, "主题相关", "主题正文片段"))
		mentions[id] = struct{}{}
	}
	ranked, truncated := rankRelationCandidates(
		source, candidates, mentions, nil, map[string]struct{}{"doc-0": {}})
	assert.True(t, truncated)
	assert.Len(t, ranked, types.WorkbenchAIRelationsShortlist)
}

// ---- 模型输出解析 ----

// 正常 JSON 被清洗：缺失强度按弱关联、编造 id 被丢弃、同一 id 去重、标题回填。
func TestParseRelationsPayload_NormalizesAndDropsUnknownIDs(t *testing.T) {
	shortlist := []*types.WorkbenchRelationCandidate{
		relationCandidate("doc-2", "网关重试方案", "摘要"),
		relationCandidate("doc-3", "监控告警", "摘要"),
	}
	content := "好的：\n```json\n" + `{"relations":[
		{"item_id":"doc-2","reason":"  同一套超时口径  ","strength":"strong","quote":"接口超时统一按 3 秒","target_quote":"网关重试与超时口径","label":"同一超时口径","source":"semantic"},
		{"item_id":"doc-3","reason":"相关","strength":"strong","quote":"","target_quote":"摘要","label":""},
		{"item_id":"doc-99","reason":"编造","strength":"strong","quote":"a","target_quote":"b"},
		{"item_id":"doc-2","reason":"重复","strength":"strong","quote":"a","target_quote":"b"}
	]}` + "\n```"

	relations, dropped, err := parseRelationsPayload(content, shortlist)
	require.NoError(t, err)
	require.Len(t, relations, 2)
	assert.Equal(t, "doc-2", relations[0].ItemID, "强关联排在弱关联之前")
	assert.Equal(t, types.WorkbenchRelationStrengthStrong, relations[0].Strength)
	assert.Equal(t, "同一套超时口径", relations[0].Reason, "理由必须去首尾空白")
	assert.Equal(t, "doc-3", relations[1].ItemID)
	assert.Equal(t, types.WorkbenchRelationStrengthWeak, relations[1].Strength,
		"摘不出当前文档原句时必须降为弱关联")
	assert.Equal(t, "监控告警", relations[1].Title, "标题缺失时回填候选标题")
	assert.True(t, dropped, "编造 id 与重复 id 都要计入丢弃")
}

// 没有 JSON / JSON 结构非法都返回错误，让调用方退回召回结果。
func TestParseRelationsPayload_RejectsInvalid(t *testing.T) {
	shortlist := []*types.WorkbenchRelationCandidate{relationCandidate("doc-2", "t", "d")}
	for name, content := range map[string]string{
		"没有 JSON": "我找不到关联。",
		"结构错误":    `{"relations":"oops"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseRelationsPayload(content, shortlist)
			require.Error(t, err)
		})
	}
}

// ---- 降级 ----

// 没有聊天模型时返回召回结果：全部弱关联、带 degraded_reason、条数受 limit 约束。
func TestRelations_DegradesWithoutModel(t *testing.T) {
	repo := &relationsRepo{
		source: &types.WorkbenchItem{
			ID: "doc-1", Title: "接口超时口径", TextContent: "接口超时统一按 3 秒计算",
		},
		candidates: []*types.WorkbenchRelationCandidate{
			relationCandidate("doc-2", "网关重试方案", "网关层重试与超时口径说明"),
			relationCandidate("doc-3", "监控告警", "告警阈值"),
		},
		mentions:   []string{"doc-2"},
		sharedTags: []string{"doc-3"},
	}
	// 空模型列表等价于「租户没有可用聊天模型」。
	svc := newRelationsService(repo, &fakeModels{})

	result, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{
		ItemID: "doc-1",
		Limit:  1,
	})
	require.NoError(t, err, "缺模型不能报错，必须降级返回")
	require.Len(t, result.Relations, 1, "降级结果同样受 limit 约束")
	assert.Equal(t, types.WorkbenchRelationDegradedNoModel, result.DegradedReason)
	assert.Equal(t, types.WorkbenchRelationStrengthWeak, result.Relations[0].Strength)
	assert.NotEmpty(t, result.Relations[0].Label, "降级结果也要有连线标签")
}

// 模型正常返回时走精判路径，并回填 model_id。
func TestRelations_UsesModelWhenAvailable(t *testing.T) {
	repo := &relationsRepo{
		source: &types.WorkbenchItem{
			ID: "doc-1", Title: "接口超时口径", TextContent: "接口超时统一按 3 秒计算",
		},
		candidates: []*types.WorkbenchRelationCandidate{
			relationCandidate("doc-2", "网关重试方案", "网关层重试与超时口径说明"),
		},
		mentions: []string{"doc-2"},
	}
	content := `{"relations":[{"item_id":"doc-2","reason":"同一套超时口径","strength":"strong",` +
		`"quote":"接口超时统一按 3 秒计算","target_quote":"网关层重试与超时口径说明","label":"同一超时口径"}]}`
	models := &fakeModels{
		model:  &fakeChat{modelID: "m1", chunks: []string{content}},
		models: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newRelationsService(repo, models)

	result, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{ItemID: "doc-1"})
	require.NoError(t, err)
	require.Len(t, result.Relations, 1)
	assert.Empty(t, result.DegradedReason)
	assert.Equal(t, "m1", result.ModelID)
	assert.Equal(t, types.WorkbenchRelationStrengthStrong, result.Relations[0].Strength)
	assert.Equal(t, types.WorkbenchRelationSourceMention, result.Relations[0].Source,
		"来源以服务端召回判定为准，不采信模型自报")
}

// 条目不属于当前用户时必须 2400，且不能触达召回。
func TestRelations_RejectsForeignItem(t *testing.T) {
	repo := &relationsRepo{
		source: &types.WorkbenchItem{ID: "doc-1", Title: "t", TextContent: "c"},
	}
	svc := newRelationsService(repo, &fakeModels{})

	_, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{ItemID: "doc-9"})
	require.Error(t, err)
	assert.Equal(t, apperrors.ErrWorkbenchNotFound, mustAppError(t, err).Code)
}

// 反向链接（入链）与正文提及（出链）都算提及信号：两个方向都能召回。
func TestRelations_RecallsFromBacklinksToo(t *testing.T) {
	repo := &relationsRepo{
		source: &types.WorkbenchItem{ID: "doc-1", Title: "接口口径", TextContent: "接口超时统一 3 秒"},
		candidates: []*types.WorkbenchRelationCandidate{
			relationCandidate("doc-5", "网关重试", "网关层重试说明"),
		},
		backlinks: []*types.WorkbenchBacklink{{ID: "doc-5", Title: "网关重试"}},
	}
	svc := newRelationsService(repo, &fakeModels{})

	result, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{ItemID: "doc-1"})
	require.NoError(t, err)
	require.Len(t, result.Relations, 1)
	assert.Equal(t, "doc-5", result.Relations[0].ItemID)
	assert.Equal(t, types.WorkbenchRelationSourceMention, result.Relations[0].Source,
		"入链与出链同属提及信号")
}

// 同名文档的历史回链（早期按标题匹配写入）也不能把同模板文档召回成关联。
func TestRelations_IgnoresSameTitleBacklinks(t *testing.T) {
	source, candidate := meetingTemplateDocs()
	repo := &relationsRepo{
		source:     source,
		candidates: []*types.WorkbenchRelationCandidate{candidate},
		backlinks:  []*types.WorkbenchBacklink{{ID: candidate.ID, Title: "会议纪要"}},
	}
	svc := newRelationsService(repo, &fakeModels{})

	result, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{ItemID: source.ID})
	require.NoError(t, err)
	assert.Empty(t, result.Relations, "同名回链与模板重合都不该产生关联")
}

// ---- 候选池为空 ----

// 召回为空时直接返回空数组，不调模型。
func TestRelations_EmptyRecallReturnsEmpty(t *testing.T) {
	repo := &relationsRepo{
		source: &types.WorkbenchItem{ID: "doc-1", Title: "t", TextContent: "正文"},
	}
	svc := newRelationsService(repo, &fakeModels{
		model: &fakeChat{modelID: "m", chunks: []string{`{"relations":[]}`}},
	})
	result, err := svc.Relations(inlineCtx(), types.WorkbenchAIRelationsRequest{ItemID: "doc-1"})
	require.NoError(t, err)
	assert.Empty(t, result.Relations)
	assert.Empty(t, result.ModelID, "没有候选就不该调用模型")
}
