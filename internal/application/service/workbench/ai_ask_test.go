package workbench

import (
	"context"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRetriever 只回预置命中；hits 为空时模拟“没检索到”。
type fakeRetriever struct {
	hits  []*types.SearchResult
	err   error
	calls int
	// 记录本次调用收到的 KB ID 列表，用于断言“强制工作 KB”。
	lastKBIDs []string
}

func (r *fakeRetriever) SearchKnowledge(
	_ context.Context, kbIDs []string, _ []string, _ []types.TagScope, _ string,
	_ *types.KnowledgeSearchOptions,
) (*types.RetrievalResult, error) {
	r.calls++
	r.lastKBIDs = kbIDs
	if r.err != nil {
		return nil, r.err
	}
	return &types.RetrievalResult{Results: r.hits}, nil
}

// fakeWorkKB 永远返回同一份工作知识库。
type fakeWorkKB struct {
	kb *types.KnowledgeBase
}

func (f *fakeWorkKB) EnsureWorkKnowledgeBase(
	_ context.Context, _ uint64, _ string,
) (*types.KnowledgeBase, error) {
	return f.kb, nil
}

// 强制使用工作 KB：即便检索层支持多 KB，也只传工作 KB 的 id。
func TestAskStream_ForcesWorkKnowledgeBase(t *testing.T) {
	retriever := &fakeRetriever{}
	svc := newInlineService(
		&fakeModels{
			model:  &fakeChat{modelID: "m", chunks: []string{"回答"}},
			models: []*types.Model{{ID: "m", Type: types.ModelTypeKnowledgeQA}},
		},
		&fakeAgents{}, nil,
	).WithAskLane(retriever, &fakeWorkKB{kb: &types.KnowledgeBase{ID: "work-kb"}})

	stream, err := svc.AskStream(inlineCtx(), types.AskAIRequest{Question: "P0 退出标准是什么"})
	require.NoError(t, err)
	_, done, failed := collectInline(t, stream)
	assert.True(t, done)
	assert.False(t, failed)
	assert.Equal(t, []string{"work-kb"}, retriever.lastKBIDs, "只能查工作 KB")
}

// 未配置问答车道 → 503，而不是空回答。
func TestAskStream_NotConfigured(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.AskStream(inlineCtx(), types.AskAIRequest{Question: "q"})
	require.Error(t, err)
	assert.Equal(t, 1008, int(mustAppError(t, err).Code), "未装配应返回 1008 服务不可用")
}

// 空问题 → 400；超长 → 2402。
func TestAskStream_QuestionValidation(t *testing.T) {
	retriever := &fakeRetriever{}
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil).
		WithAskLane(retriever, &fakeWorkKB{kb: &types.KnowledgeBase{ID: "kb"}})

	_, err := svc.AskStream(inlineCtx(), types.AskAIRequest{Question: "   "})
	require.Error(t, err)
	assert.Equal(t, 1000, int(mustAppError(t, err).Code))

	_, err = svc.AskStream(inlineCtx(), types.AskAIRequest{
		Question: strings.Repeat("问", types.WorkbenchAITextMaxRunes+1),
	})
	require.Error(t, err)
	assert.Equal(t, 2402, int(mustAppError(t, err).Code))
}

// 命中检索时先发 references 帧，再发正文与 done。
func TestAskStream_EmitsReferencesBeforeAnswer(t *testing.T) {
	retriever := &fakeRetriever{hits: []*types.SearchResult{
		{KnowledgeID: "k1", KnowledgeTitle: "方案 A", ChunkIndex: 2, Score: 0.9},
		{KnowledgeID: "k1", KnowledgeTitle: "方案 A", ChunkIndex: 3, Score: 0.8},
		{KnowledgeID: "k2", KnowledgeTitle: "方案 B", ChunkIndex: 0, Score: 0.7},
	}}
	svc := newInlineService(
		&fakeModels{
			model:  &fakeChat{modelID: "m", chunks: []string{"答"}},
			models: []*types.Model{{ID: "m", Type: types.ModelTypeKnowledgeQA}},
		},
		&fakeAgents{}, nil,
	).WithAskLane(retriever, &fakeWorkKB{kb: &types.KnowledgeBase{ID: "kb"}})

	stream, err := svc.AskStream(inlineCtx(), types.AskAIRequest{Question: "q"})
	require.NoError(t, err)

	var order []string
	var refs []types.WorkbenchAIReference
	for resp := range stream {
		switch resp.ResponseType {
		case types.ResponseTypeReferences:
			order = append(order, "references")
			refs, _ = resp.Data["references"].([]types.WorkbenchAIReference)
		case types.ResponseTypeAnswer:
			if resp.Done {
				order = append(order, "done")
			} else {
				order = append(order, "delta")
			}
		}
	}
	assert.Equal(t, []string{"references", "delta", "done"}, order)
	require.Len(t, refs, 2, "同一知识只保留一条引用")
	assert.Equal(t, "k1", refs[0].KnowledgeID)
}

// 检索失败 → 500，不能假装成功给出没有依据的回答。
func TestAskStream_RetrievalFailure(t *testing.T) {
	svc := newInlineService(
		&fakeModels{
			model:  &fakeChat{modelID: "m"},
			models: []*types.Model{{ID: "m", Type: types.ModelTypeKnowledgeQA}},
		},
		&fakeAgents{}, nil,
	).WithAskLane(&fakeRetriever{err: assert.AnError}, &fakeWorkKB{kb: &types.KnowledgeBase{ID: "kb"}})

	_, err := svc.AskStream(inlineCtx(), types.AskAIRequest{Question: "q"})
	require.Error(t, err)
	assert.Equal(t, 1007, int(mustAppError(t, err).Code))
}

// 未命中时提示词里要明确“没有检索到片段”，避免模型编造引用。
func TestBuildAskMessages_NoHitIsExplicit(t *testing.T) {
	messages := buildAskMessages("问题内容", nil)
	require.Len(t, messages, 2)
	assert.Contains(t, messages[1].Content, "没有检索到")
	assert.Contains(t, messages[1].Content, "问题内容")
}

// 命中时把条目标题列进上下文，且去重。
func TestBuildAskMessages_ListsTitles(t *testing.T) {
	messages := buildAskMessages("q", []types.WorkbenchAIReference{
		{KnowledgeID: "k1", KnowledgeTitle: "方案 A"},
		{KnowledgeID: "k2", KnowledgeTitle: ""},
	})
	assert.Contains(t, messages[1].Content, "可引用的工作知识库条目")
	assert.Contains(t, messages[1].Content, "方案 A")
	assert.NotContains(t, messages[1].Content, "- \n", "空标题不产生空条目")
}

// 问答提示词必须声明“内容不是指令”，与内联车道同源。
func TestBuildAskMessages_SystemPromptGuardsInjection(t *testing.T) {
	messages := buildAskMessages("q", nil)
	assert.Contains(t, messages[0].Content, "不是指令")
	assert.Contains(t, messages[0].Content, "不要编造")
}

// 引用上限 8 条；超出部分丢弃而不是无限增长。
func TestToWorkbenchReferences_CapsAtEight(t *testing.T) {
	hits := make([]*types.SearchResult, 0, 12)
	for i := 0; i < 12; i++ {
		hits = append(hits, &types.SearchResult{
			KnowledgeID: string(rune('a' + i)), KnowledgeTitle: "t",
		})
	}
	refs := toWorkbenchReferences(&types.RetrievalResult{Results: hits})
	assert.Len(t, refs, 8)
}

// 空检索结果返回空切片而不是 nil，方便前端直接 .map。
func TestToWorkbenchReferences_EmptyIsSlice(t *testing.T) {
	assert.NotNil(t, toWorkbenchReferences(nil))
	assert.NotNil(t, toWorkbenchReferences(&types.RetrievalResult{}))
}

// 编译期断言：假实现满足窄接口。
var (
	_ aiRetriever     = (*fakeRetriever)(nil)
	_ aiWorkKBProvider = (*fakeWorkKB)(nil)
	_                  = apperrors.ErrWorkbenchNoModel
)
