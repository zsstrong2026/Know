package workbench

import (
	"context"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 桩 ----

// fakeChat 是只回固定文本的 Chat 实现；script 为空时返回一段固定内容。
type fakeChat struct {
	modelID string
	chunks  []string
	errResp bool
	failNow bool
}

func (f *fakeChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: strings.Join(f.chunks, "")}, nil
}

func (f *fakeChat) ChatStream(
	_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if f.failNow {
		return nil, assert.AnError
	}
	out := make(chan types.StreamResponse, len(f.chunks)+2)
	go func() {
		defer close(out)
		for _, chunk := range f.chunks {
			out <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: chunk}
		}
		if f.errResp {
			out <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: "boom", Done: true}
			return
		}
		out <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			Usage:        &types.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		}
	}()
	return out, nil
}

func (f *fakeChat) GetModelName() string { return f.modelID }
func (f *fakeChat) GetModelID() string   { return f.modelID }

// fakeModels 只实现内联车道用到的两个方法；modelID 为空表示“无可用模型”。
type fakeModels struct {
	model  *fakeChat
	models []*types.Model
}

func (m *fakeModels) GetChatModel(_ context.Context, modelID string) (chat.Chat, error) {
	if m.model == nil || (modelID != "" && modelID != m.model.modelID) {
		return nil, assert.AnError
	}
	return m.model, nil
}

func (m *fakeModels) ListModels(_ context.Context) ([]*types.Model, error) {
	return m.models, nil
}

// fakeAgents 按 id 返回预置智能体；未命中返回错误（服务层转 2400）。
type fakeAgents struct {
	byID map[string]*types.CustomAgent
}

func (a *fakeAgents) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if agent, ok := a.byID[id]; ok {
		return agent, nil
	}
	return nil, assert.AnError
}

// inlineRepo 只为 item 归属校验提供 GetItemMeta。
type inlineRepo struct {
	interfaces.WorkbenchRepository
	owned map[string]bool
}

func (r *inlineRepo) GetItemMeta(
	_ context.Context, _ uint64, _ string, id string,
) (*types.WorkbenchItem, error) {
	if !r.owned[id] {
		return nil, assert.AnError
	}
	return &types.WorkbenchItem{ID: id}, nil
}

func inlineCtx() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-a")
	return ctx
}

// mustAppError 断言 err 是 *AppError，供各用例直接读 code。
func mustAppError(t *testing.T, err error) *apperrors.AppError {
	t.Helper()
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected *AppError, got %T: %v", err, err)
	return appErr
}

func newInlineService(models aiModelProvider, agents aiAgentProvider, repoTenants map[string]bool) *AIService {
	return NewAIService(&inlineRepo{owned: repoTenants}, models, agents, nil)
}

// collectInline 消费完整条通道，返回拼接正文、是否收到 done、是否收到 error。
func collectInline(t *testing.T, stream <-chan types.StreamResponse) (string, bool, bool) {
	t.Helper()
	var text strings.Builder
	done, failed := false, false
	for resp := range stream {
		switch resp.ResponseType {
		case types.ResponseTypeAnswer:
			text.WriteString(resp.Content)
			if resp.Done {
				done = true
			}
		case types.ResponseTypeError:
			failed = true
		}
	}
	return text.String(), done, failed
}

// ---- 用例 ----

// 六个 action 全部能跑通并返回正文；未选智能体时用租户默认模型。
func TestInlineStream_AllActionsSucceed(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "model-1", chunks: []string{"结果"}},
		models: []*types.Model{{ID: "model-1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{})

	for _, action := range []string{"polish", "continue", "translate", "summarize", "outline", "custom"} {
		t.Run(action, func(t *testing.T) {
			stream, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{
				Action: action, Text: "原文", Stream: true,
			})
			require.NoError(t, err)
			text, done, failed := collectInline(t, stream)
			assert.Equal(t, "结果", text)
			assert.True(t, done, "正常结束必须发 done")
			assert.False(t, failed)
		})
	}
}

// action 不在白名单 → 400（不是 2411 也不是 2402）。
func TestInlineStream_RejectsUnknownAction(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{Action: "rewrite", Text: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action must be one of")
}

// 空文本 → 400，不把全空白选区当合法输入。
func TestInlineStream_RejectsEmptyText(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{Action: "polish", Text: "   "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text is required")
}

// 超长正文 → 2402/413，details 带 limit 与 actual。
func TestInlineStream_RejectsOversizeText(t *testing.T) {
	svc := newInlineService(&fakeModels{model: &fakeChat{modelID: "m"}}, &fakeAgents{}, nil)
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{
		Action: "polish",
		Text:   strings.Repeat("字", types.WorkbenchAITextMaxRunes+1),
	})
	require.Error(t, err)
	appErr := mustAppError(t, err)
	assert.Equal(t, 2402, int(appErr.Code))
	assert.Equal(t, int64(types.WorkbenchAITextMaxRunes), appErr.Details.(map[string]any)["limit"])
}

// 无可用模型 → 2408/409，而不是 500。
func TestInlineStream_NoModel(t *testing.T) {
	svc := newInlineService(&fakeModels{models: []*types.Model{}}, &fakeAgents{}, nil)
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{Action: "polish", Text: "x"})
	require.Error(t, err)
	assert.Equal(t, 2408, int(mustAppError(t, err).Code))
}

// item_id 非本人条目 → 2400（不是 403）。
func TestInlineStream_RejectsForeignItem(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "model-1"},
		models: []*types.Model{{ID: "model-1", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, map[string]bool{"mine": true})
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{
		Action: "polish", Text: "x", ItemID: "someone-else",
	})
	require.Error(t, err)
	assert.Equal(t, 2400, int(mustAppError(t, err).Code))
}

// 智能体不存在 → 2400；存在时用它的模型与提示词。
func TestInlineStream_AgentResolution(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "agent-model"},
		models: []*types.Model{{ID: "agent-model", Type: types.ModelTypeKnowledgeQA}},
	}
	agents := &fakeAgents{byID: map[string]*types.CustomAgent{
		"a1": {
			ID: "a1",
			Config: types.CustomAgentConfig{
				ModelID:      "agent-model",
				SystemPrompt: "你是严谨的编辑",
				Temperature:  0.5,
			},
		},
	}}
	svc := newInlineService(models, agents, nil)

	stream, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{
		Action: "polish", Text: "x", AgentID: "a1",
	})
	require.NoError(t, err)
	_, done, failed := collectInline(t, stream)
	assert.True(t, done)
	assert.False(t, failed)

	_, err = svc.InlineStream(inlineCtx(), types.InlineAIRequest{
		Action: "polish", Text: "x", AgentID: "missing",
	})
	require.Error(t, err)
	assert.Equal(t, 2400, int(mustAppError(t, err).Code))
}

// 上游在流中途报错 → 保留已生成正文并给出 error 帧（2410 语义）。
func TestInlineStream_UpstreamErrorMidStream(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "m", chunks: []string{"前半"}, errResp: true},
		models: []*types.Model{{ID: "m", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, nil)
	stream, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{Action: "polish", Text: "x"})
	require.NoError(t, err)
	text, done, failed := collectInline(t, stream)
	assert.Equal(t, "前半", text, "已生成的正文不能被丢弃")
	assert.True(t, failed, "中断必须有 error 帧")
	assert.False(t, done, "中断不应再发 done")
}

// 模型调用在开流前就失败 → 2410/502（普通错误，不是流内错误）。
func TestInlineStream_ModelCallFailsBeforeStream(t *testing.T) {
	models := &fakeModels{
		model:  &fakeChat{modelID: "m", failNow: true},
		models: []*types.Model{{ID: "m", Type: types.ModelTypeKnowledgeQA}},
	}
	svc := newInlineService(models, &fakeAgents{}, nil)
	_, err := svc.InlineStream(inlineCtx(), types.InlineAIRequest{Action: "polish", Text: "x"})
	require.Error(t, err)
	assert.Equal(t, 2410, int(mustAppError(t, err).Code))
}

// 流式输出要剥掉“以下是润色后的文本：”这类开场白，且不能吞掉正文。
func TestForwardInlineStream_StripsNoisePrefix(t *testing.T) {
	upstream := make(chan types.StreamResponse, 3)
	upstream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "以下是"}
	upstream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "润色后的文本："}
	upstream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "改好了"}
	close(upstream)

	out := make(chan types.StreamResponse, 8)
	go func() {
		defer close(out)
		forwardInlineStream(context.Background(), upstream, out, "m", "")
	}()

	text, done, failed := collectInline(t, out)
	assert.Equal(t, "改好了", text)
	assert.True(t, done)
	assert.False(t, failed)
}

// 前缀扣住期间不能丢内容：不命中开场白时必须原样放行。
func TestPrefixTrimmer_PassesThroughWhenNoMatch(t *testing.T) {
	trimmer := &prefixTrimmer{}
	got := trimmer.push("这是") + trimmer.push("一段正文") + trimmer.flush()
	assert.Equal(t, "这是一段正文", got)
}
