package executor

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// stubQA 记录入参并按需通过事件总线发事件（模拟 AgentQA 的流式行为）。
type stubQA struct {
	gotReq *types.QARequest
	emit   func(bus *event.EventBus)
	retErr error
}

func (s *stubQA) AgentQA(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
	s.gotReq = req
	if s.emit != nil {
		s.emit(bus)
	}
	return s.retErr
}

// stubSessions 是最小会话服务：新建会话补固定 id，按 id 查回同一份。
type stubSessions struct {
	created []*types.Session
}

func (s *stubSessions) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	if session.ID == "" {
		session.ID = "sess-1"
	}
	s.created = append(s.created, session)
	return session, nil
}

func (s *stubSessions) GetSessionByID(_ context.Context, _ uint64, id string) (*types.Session, error) {
	return &types.Session{ID: id, TenantID: 1}, nil
}

// stubMessages 是最小消息服务：建行补 id，更新只记最后一次内容。
type stubMessages struct {
	created []*types.Message
	updated *types.Message
}

func (s *stubMessages) CreateMessage(_ context.Context, msg *types.Message) (*types.Message, error) {
	if msg.ID == "" {
		msg.ID = "msg-" + msg.Role
	}
	s.created = append(s.created, msg)
	return msg, nil
}

func (s *stubMessages) UpdateMessage(_ context.Context, msg *types.Message) error {
	s.updated = msg
	return nil
}

// stubAgents 按 id 返回预置角色。
type stubAgents struct {
	role *types.CustomAgent
	err  error
}

func (s stubAgents) GetAgentByID(_ context.Context, _ string, _ uint64) (*types.CustomAgent, error) {
	return s.role, s.err
}

func TestAgentProviderBuildsQARequest(t *testing.T) {
	qa := &stubQA{}
	messages := &stubMessages{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: messages})
	role := &types.CustomAgent{Config: types.CustomAgentConfig{
		SystemPrompt: "你是工程师",
		AllowedTools: []string{"search_knowledge"},
	}}
	tb := TaskBook{
		RunID: "r1", NodeRunID: "nr1", NodeID: "n1", NodeName: "实现", Prompt: "任务书正文",
		WorkspaceRoot: t.TempDir(), OutDir: t.TempDir(), Model: "model-x", Role: role,
		Skills: []string{"go-test"}, Timeout: time.Minute, Attempt: 1,
	}
	var events []StreamEvent
	res, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) { events = append(events, e) }})
	require.NoError(t, err)
	require.NotNil(t, qa.gotReq)
	require.Equal(t, "任务书正文", qa.gotReq.Query)
	require.True(t, qa.gotReq.Suspendable)
	require.Equal(t, types.RunModeDeferred, qa.gotReq.Mode)
	require.Contains(t, qa.gotReq.RunNote, "r1")
	require.Equal(t, "model-x", qa.gotReq.CustomAgent.Config.ModelID)
	require.Equal(t, []string{"go-test"}, qa.gotReq.CustomAgent.Config.SelectedSkills)
	require.Equal(t, []string{"search_knowledge", "submit_node_result"},
		qa.gotReq.CustomAgent.Config.AllowedTools)
	require.NotEmpty(t, qa.gotReq.UserMessageID)
	require.NotEmpty(t, qa.gotReq.AssistantMessageID)
	require.NotEmpty(t, res.SessionID)
	// 共享的角色对象不能被就地改写。
	require.Empty(t, role.Config.ModelID)
	require.Equal(t, []string{"search_knowledge"}, role.Config.AllowedTools)
}

func TestAgentProviderSessionDescriptionCarriesMarker(t *testing.T) {
	sessions := &stubSessions{}
	p := NewAgentProvider(AgentDeps{QA: &stubQA{}, Sessions: sessions, Messages: &stubMessages{}})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	_, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Len(t, sessions.created, 1)
	require.Equal(t, types.SOPSessionMarker+"run=r1 node=n1", sessions.created[0].Description)
	require.Equal(t, "实现 - SOP", sessions.created[0].Title)
}

func TestAgentProviderReusesSessionAndAppendsResume(t *testing.T) {
	qa := &stubQA{}
	p := NewAgentProvider(AgentDeps{
		QA:          qa,
		Sessions:    &stubSessions{},
		Messages:    &stubMessages{},
		Suspensions: stubSuspensions{snapshot: []byte("snap"), prompt: &types.AgentPrompt{ID: "p1"}},
	})
	tb := TaskBook{
		RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "追问答复",
		Role: &types.CustomAgent{}, Attempt: 2, ResumeSessionID: "sess-9",
	}
	res, err := p.Execute(context.Background(), tb, ExecOptions{SessionID: "sess-9"})
	require.NoError(t, err)
	require.Equal(t, "sess-9", res.SessionID)
	require.NotNil(t, qa.gotReq.Resume)
	require.Equal(t, []byte("snap"), qa.gotReq.Resume.Snapshot)
	require.Equal(t, "p1", qa.gotReq.Resume.Prompt.ID)
}

func TestAgentProviderMapsStreamEvents(t *testing.T) {
	qa := &stubQA{emit: func(bus *event.EventBus) {
		ctx := context.Background()
		bus.Emit(ctx, event.Event{Type: event.EventAgentToolCall, Data: event.AgentToolCallData{
			ToolName: "search_knowledge", Arguments: map[string]any{"query": "q"},
		}})
		bus.Emit(ctx, event.Event{Type: event.EventAgentToolResult, Data: event.AgentToolResultData{
			ToolName: "search_knowledge", Output: "命中 2 条", Success: true,
		}})
		bus.Emit(ctx, event.Event{Type: event.EventAgentFinalAnswer, Data: event.AgentFinalAnswerData{Content: "结论正文"}})
		bus.Emit(ctx, event.Event{Type: event.EventAgentComplete, Data: event.AgentCompleteData{
			FinalAnswer: "结论正文",
			Usage:       &types.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		}})
	}}
	messages := &stubMessages{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: messages})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	var types []string
	res, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) { types = append(types, e.Type) }})
	require.NoError(t, err)
	require.Equal(t, []string{
		StreamEventToolCall, StreamEventToolResult, StreamEventTextDelta, StreamEventResult,
	}, types)
	require.Equal(t, "结论正文", res.Output)
	require.Equal(t, int64(10), res.Usage.InputTokens)
	require.Equal(t, int64(5), res.Usage.OutputTokens)
	// assistant 行被写回正文并标记完成。
	require.NotNil(t, messages.updated)
	require.True(t, messages.updated.IsCompleted)
	require.Equal(t, "结论正文", messages.updated.Content)
}

func TestAgentProviderSuspensionMarksResult(t *testing.T) {
	qa := &stubQA{emit: func(bus *event.EventBus) {
		bus.Emit(context.Background(), event.Event{Type: event.EventAgentComplete, Data: event.AgentCompleteData{
			Suspended: &types.PromptSuspension{PromptID: "p1"},
		}})
	}}
	messages := &stubMessages{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: messages})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.True(t, res.Suspended)
	require.False(t, messages.updated.IsCompleted)
	require.Equal(t, types.MessageRunStateAwaitingInput, messages.updated.RunState)
}

func TestAgentProviderErrorEventBecomesError(t *testing.T) {
	qa := &stubQA{emit: func(bus *event.EventBus) {
		bus.Emit(context.Background(), event.Event{Type: event.EventError, Data: event.ErrorData{
			Error: "模型调用失败", Stage: "agent_execution",
		}})
	}}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	_, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "模型调用失败")
}

func TestAgentProviderPrepareRerankGuard(t *testing.T) {
	p := NewAgentProvider(AgentDeps{QA: &stubQA{}})
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: "platform_agent", KBScope: "project"}}
	err := p.Prepare(context.Background(), node, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "重排模型")
}

func TestAgentProviderPreparePassesWithRerankOrNoKB(t *testing.T) {
	role := &types.CustomAgent{Config: types.CustomAgentConfig{RerankModelID: "rr-1"}}
	p := NewAgentProvider(AgentDeps{
		QA:     &stubQA{},
		Agents: stubAgents{role: role},
	})
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1", KBScope: "project"}}
	require.NoError(t, p.Prepare(context.Background(), node, t.TempDir()))

	// 不使用知识库时不需要重排模型。
	plain := NewAgentProvider(AgentDeps{QA: &stubQA{}})
	node.Executor.KBScope = "none"
	require.NoError(t, plain.Prepare(context.Background(), node, t.TempDir()))
}

func TestAgentProviderPrepareWrapsAgentReadError(t *testing.T) {
	p := NewAgentProvider(AgentDeps{QA: &stubQA{}, Agents: stubAgents{err: context.DeadlineExceeded}})
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1", KBScope: "all"}}
	err := p.Prepare(context.Background(), node, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "读取执行角色失败")
}

// stubSuspensions 按会话返回预置挂起。
type stubSuspensions struct {
	snapshot []byte
	prompt   *types.AgentPrompt
}

func (s stubSuspensions) GetBySession(_ context.Context, _ string) (*AgentSuspension, error) {
	return &AgentSuspension{Snapshot: s.snapshot, Prompt: s.prompt}, nil
}

func TestAgentProviderCancelStopsRun(t *testing.T) {
	started := make(chan struct{})
	qa := &blockingQA{started: started}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	done := make(chan error, 1)
	go func() {
		_, err := p.Execute(context.Background(), tb, ExecOptions{})
		done <- err
	}()
	<-started
	require.NoError(t, p.Cancel(context.Background()))
	select {
	case err := <-done:
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel 没有中断在途执行")
	}
}

// blockingQA 阻塞到 ctx 取消，模拟长跑 AgentQA。
type blockingQA struct{ started chan struct{} }

func (b *blockingQA) AgentQA(ctx context.Context, _ *types.QARequest, _ *event.EventBus) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

// slotQA 首次调用正常返回、第二次（追问）调用时通过结果槽提交结论，模拟真实模型。
type slotQA struct {
	slots *ResultSlots
	key   string
	calls []*types.QARequest
	put   bool
	// putOnCall 指定在第几次调用（1 起）提交结果；0 表示 put 为 true 时每次都提交。
	putOnCall int
}

func (s *slotQA) AgentQA(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
	s.calls = append(s.calls, req)
	bus.Emit(context.Background(), event.Event{Type: event.EventAgentComplete, Data: event.AgentCompleteData{
		FinalAnswer: "正文",
	}})
	call := len(s.calls)
	if s.put && (s.putOnCall == 0 || s.putOnCall == call) {
		key := s.key
		if key == "" {
			key = "r1/n1/1"
		}
		s.slots.Put(key, SubmitResult{GateResult: types.SopGateRework, GateReason: "缺少测试", Output: "改后正文"})
	}
	return nil
}

func TestAgentProviderPrefersSubmittedResult(t *testing.T) {
	slots := NewResultSlots()
	qa := &slotQA{slots: slots, put: true}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}, Results: slots})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Equal(t, "缺少测试", res.GateReason)
	require.Equal(t, "改后正文", res.Output)
	require.Len(t, qa.calls, 1, "提交了结果就不该追问")
}

func TestAgentProviderAsksOnceWhenResultMissing(t *testing.T) {
	slots := NewResultSlots()
	// 首次执行不提交（模拟模型忘了调工具），追问那一轮才提交。
	qa := &slotQA{slots: slots, put: true, putOnCall: 2}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}, Results: slots})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Len(t, qa.calls, 2, "缺失结果应追问一次")
	require.Contains(t, qa.calls[1].Query, "submit_node_result")
	require.False(t, qa.calls[1].Suspendable, "追问轮不得再次挂起")
	require.Equal(t, types.SopGateRework, res.GateResult)
}

func TestAgentProviderLeavesGateEmptyWhenResultStillMissing(t *testing.T) {
	slots := NewResultSlots()
	qa := &slotQA{slots: slots}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}, Results: slots})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Len(t, qa.calls, 2, "只追问一次")
	require.Empty(t, res.GateResult, "缺失时交门禁裁定")
}

func TestAgentProviderOverridesMaxIterations(t *testing.T) {
	qa := &stubQA{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}})
	role := &types.CustomAgent{Config: types.CustomAgentConfig{MaxIterations: -1}}
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: role, Attempt: 1, LoopMaxIterations: 3}
	_, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Equal(t, 3, qa.gotReq.CustomAgent.Config.MaxIterations)
	require.Equal(t, -1, role.Config.MaxIterations, "共享角色对象不得被就地改写")
}

// 提交后挂起（执行器不消费槽）不得污染下一次尝试：键带尝试号，旧槽取不到。
func TestSubmitSlotIsolatesAttempts(t *testing.T) {
	slots := NewResultSlots()
	slots.Put("r1/n1/1", SubmitResult{GateResult: types.SopGateRework, GateReason: "旧结论"})
	qa := &slotQA{slots: slots}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}, Results: slots})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 2}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Empty(t, res.GateResult, "第二次尝试不得命中第一次的残槽")
	require.Len(t, qa.calls, 2, "残槽不计入，仍应追问一次")
}

// 追问轮正文与 token 统计都并入本轮（消息行与用量不能各说各话）。
func TestAgentProviderMergesReminderRound(t *testing.T) {
	slots := NewResultSlots()
	qa := &mergeQA{slots: slots}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}, Results: slots})
	tb := TaskBook{RunID: "r1", NodeID: "n1", NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	require.Contains(t, res.Output, "补充说明")
	require.Equal(t, int64(30), res.Usage.InputTokens)
	require.Equal(t, int64(20), res.Usage.OutputTokens)
	require.Equal(t, types.SopGatePass, res.GateResult)
}

// mergeQA 第一轮不提交、第二轮提交，两轮各有不同的用量与正文。
type mergeQA struct {
	slots *ResultSlots
	calls int
}

func (m *mergeQA) AgentQA(_ context.Context, _ *types.QARequest, bus *event.EventBus) error {
	m.calls++
	content := "首轮正文"
	if m.calls > 1 {
		content = "补充说明"
		m.slots.Put("r1/n1/1", SubmitResult{GateResult: types.SopGatePass})
	}
	bus.Emit(context.Background(), event.Event{
		Type: event.EventAgentFinalAnswer, Data: event.AgentFinalAnswerData{Content: content},
	})
	bus.Emit(context.Background(), event.Event{Type: event.EventAgentComplete, Data: event.AgentCompleteData{
		FinalAnswer: content, Usage: &types.TokenUsage{PromptTokens: 15, CompletionTokens: 10},
	}})
	return nil
}
