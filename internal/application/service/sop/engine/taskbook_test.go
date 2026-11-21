package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRenderTaskBookContextBlocks(t *testing.T) {
	in := TaskBookInput{
		Node:            types.SopNode{ID: "n2", Name: "实现", TaskPrompt: "按方案实现，完成后运行自测。"},
		Item:            &types.SopItem{Title: "登录改造", Description: "支持验证码"},
		UpstreamOutputs: map[string]string{"n1": "分析结论：…"},
		UpstreamNames:   map[string]string{"n1": "需求分析"},
	}
	out := RenderTaskBook(in)
	if !strings.Contains(out, "登录改造") || !strings.Contains(out, "事项背景") {
		t.Fatal("事项背景块缺失")
	}
	if !strings.Contains(out, "需求分析") || !strings.Contains(out, "上游步骤产物") {
		t.Fatal("上游产物块缺失")
	}
	if strings.Contains(out, "返工意见") {
		t.Fatal("非返工轮不应带返工意见块")
	}
	if strings.Contains(out, "{{") {
		t.Fatal("大白话任务书不得出现占位符")
	}
	in.Feedback = "验收不通过：缺少异常态"
	out2 := RenderTaskBook(in)
	if !strings.Contains(out2, "返工意见") || !strings.Contains(out2, "缺少异常态") {
		t.Fatal("返工轮必须带返工意见块")
	}
	in.HumanContext = []types.SopHumanContextEntry{{Text: "补充：优先移动端"}}
	in.AnsweredClarifications = []types.SopItemClarification{{Question: "q", Answer: "a", Status: "answered"}}
	out3 := RenderTaskBook(in)
	if !strings.Contains(out3, "人工补充意见") || !strings.Contains(out3, "优先移动端") || !strings.Contains(out3, "a") {
		t.Fatal("人工补充意见块缺失")
	}
}

func TestRenderTaskBookLegacyPlaceholders(t *testing.T) {
	in := TaskBookInput{
		Node:            types.SopNode{ID: "n1", TaskPrompt: "处理 {{input}}，参考 {{upstream}}，意见 {{feedback}}"},
		Item:            &types.SopItem{Title: "T", Description: "D"},
		UpstreamOutputs: map[string]string{"n0": "U"},
		Feedback:        "F",
	}
	out := RenderTaskBook(in)
	if strings.Contains(out, "{{") || !strings.Contains(out, "U") || !strings.Contains(out, "F") {
		t.Fatalf("legacy placeholders not rendered: %s", out)
	}
}

func TestResolveSkills(t *testing.T) {
	role := &types.CustomAgent{}
	role.Config.SelectedSkills = []string{"a", "b", "c"} // 字段路径以 custom_agent.go 实际为准
	if got := ResolveSkills(role, nil); len(got) != 3 {
		t.Fatalf("nil should mean all: %v", got)
	}
	if got := ResolveSkills(role, []string{"a", "x"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("subset intersection wrong: %v", got)
	}
	if got := ResolveSkills(role, []string{}); len(got) != 0 {
		t.Fatalf("explicit empty should mean none: %v", got)
	}
}

func TestRenderAgentSystem(t *testing.T) {
	role := &types.CustomAgent{}
	role.Config.SystemPrompt = "你是资深工程师" // CustomAgent 无顶层 SystemPrompt，实际字段路径在 Config（task-11 brief 澄清 18）
	s := RenderAgentSystem(role, []string{"go-test"}, "禁止改公共 API", "按方案实现")
	for _, want := range []string{"你是资深工程师", "go-test", "禁止改公共 API", "按方案实现"} {
		if !strings.Contains(s, want) {
			t.Fatalf("agent-system missing %q: %s", want, s)
		}
	}
}

// ---- 以下为补充用例 ----

func TestRenderTaskBookLegacyExtraTokens(t *testing.T) {
	in := TaskBookInput{
		Node: types.SopNode{
			ID:   "n2",
			Name: "实现",
			TaskPrompt: "读 {{ node.n1.output }}，目录 {{inputs_dir}} 与 {{artifacts_dir}}，" +
				"人工意见 {{humanContext}}，未知 {{whatever}}",
		},
		Item:            &types.SopItem{Title: "T", Description: "D"},
		UpstreamOutputs: map[string]string{"n1": "上游正文"},
		RefOutputs:      map[string]string{"n1": "完整输出"},
		HumanContext:    []types.SopHumanContextEntry{{Text: "优先移动端"}},
		InputsDir:       "runs/r1/inputs",
		OutDir:          "runs/r1/out",
	}
	out := RenderTaskBook(in)
	require.NotContains(t, out, "{{")
	require.Contains(t, out, "完整输出")
	require.Contains(t, out, "runs/r1/inputs")
	require.Contains(t, out, "runs/r1/out")
	require.Contains(t, out, "优先移动端")
	require.NotContains(t, out, "whatever")
}

func TestRenderTaskBookSectionOrder(t *testing.T) {
	in := TaskBookInput{
		Node: types.SopNode{
			ID:         "n2",
			Name:       "实现",
			Type:       types.SopNodeTypeAgent,
			TaskPrompt: "写代码",
			ExtraRules: "不改公共 API",
			ArtifactOutputs: []types.SopArtifactOutput{
				{Name: "代码", Path: "out/x.go", Required: true, Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists}},
			},
		},
		Item:                   &types.SopItem{Title: "T", Description: "D"},
		UpstreamOutputs:        map[string]string{"n1": "U"},
		UpstreamNames:          map[string]string{"n1": "需求分析"},
		Feedback:               "F",
		HumanContext:           []types.SopHumanContextEntry{{Text: "H"}},
		AnsweredClarifications: []types.SopItemClarification{{Question: "q", Answer: "a", Status: types.SopClarificationStatusAnswered}},
		Role:                   &types.CustomAgent{Name: "工程师"},
		InputsDir:              "inputs",
		OutDir:                 "out",
	}
	out := RenderTaskBook(in)
	order := []string{
		"# 步骤：", "## 角色与目标", "## 遵循规则", "## 这一步做什么",
		"## 事项背景", "## 上游步骤产物", "## 返工意见", "## 人工补充意见",
		"## 产出要求", "## 结果提交",
	}
	last := -1
	for _, h := range order {
		idx := strings.Index(out, h)
		require.GreaterOrEqual(t, idx, 0, "缺少小节 %q", h)
		require.Greater(t, idx, last, "小节 %q 顺序不对", h)
		last = idx
	}
}

func TestRenderTaskBookUpstreamOrderDeterministic(t *testing.T) {
	base := TaskBookInput{
		Node: types.SopNode{ID: "n9", Name: "汇总", TaskPrompt: "汇总"},
		Item: &types.SopItem{Title: "T", Description: "D"},
	}
	a := base
	a.UpstreamOutputs = map[string]string{"n1": "A", "n2": "B", "n3": "C"}
	a.UpstreamNames = map[string]string{"n1": "分析", "n2": "实现", "n3": "验收"}
	b := base
	b.UpstreamOutputs = map[string]string{"n3": "C", "n1": "A", "n2": "B"}
	b.UpstreamNames = map[string]string{"n2": "实现", "n3": "验收", "n1": "分析"}
	outA, outB := RenderTaskBook(a), RenderTaskBook(b)
	require.Equal(t, outA, outB, "同一输入不同 map 插入顺序应产出相同任务书")

	// 按节点 ID 升序：n1(分析) → n2(实现) → n3(验收)。
	require.Less(t, strings.Index(outA, "分析"), strings.Index(outA, "实现"))
	require.Less(t, strings.Index(outA, "实现"), strings.Index(outA, "验收"))
}

func TestRenderTaskBookTitlesAvoidJargon(t *testing.T) {
	in := TaskBookInput{
		Node:      types.SopNode{ID: "n1", Name: "实现", TaskPrompt: "写代码"},
		Item:      &types.SopItem{Title: "T", Description: "D"},
		InputsDir: "inputs",
		OutDir:    "out",
	}
	out := RenderTaskBook(in)
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		for _, bad := range []string{"任务书", "门禁", "gate", "Gate"} {
			require.NotContains(t, line, bad, "标题含工程术语：%q", line)
		}
	}
}

func TestRenderTaskBookArtifactsAndSubmit(t *testing.T) {
	node := types.SopNode{
		ID:   "n1",
		Name: "实现",
		Type: types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{
			Kind:    types.SopExecutorCLI,
			CLIKind: "zcode",
		},
		TaskPrompt: "写代码",
		ArtifactOutputs: []types.SopArtifactOutput{
			{Name: "代码", Path: "out/x.go", Required: true, Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists}},
			{Name: "说明", Path: "out/readme.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckContains, Keyword: "用法"}},
			{Name: "检查", Path: "out/y", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckCommand, Command: "test -f out/y"}},
		},
	}
	out := RenderTaskBook(TaskBookInput{Node: node, OutDir: "runs/r1/out"})
	require.Contains(t, out, "out/x.go")
	require.Contains(t, out, "必须交")
	require.Contains(t, out, "可选")
	require.Contains(t, out, "用法")
	require.Contains(t, out, "test -f out/y")
	require.Contains(t, out, "完成后把产物写到 runs/r1/out 对应路径")
	require.Contains(t, out, ".sop/gate-results/n1/attempt-N.json")

	platform := node
	platform.Executor = types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a1"}
	out2 := RenderTaskBook(TaskBookInput{Node: platform})
	require.Contains(t, out2, "submit_node_result")
	require.NotContains(t, out2, "gate-results")
	for _, want := range []string{"完成", "需返工", "失败", "需人裁定"} {
		require.Contains(t, out2, want)
	}

	// 无产物声明时不出现空的产物清单，也不能崩。
	out3 := RenderTaskBook(TaskBookInput{Node: types.SopNode{ID: "n3", TaskPrompt: "想清楚"}})
	require.Contains(t, out3, "## 产出要求")
	require.NotContains(t, out3, "{{")
}

func TestRenderTaskBookIndependentRunAndEmptyPrompt(t *testing.T) {
	out := RenderTaskBook(TaskBookInput{Node: types.SopNode{ID: "n1", Name: "分析"}})
	require.Contains(t, out, "# 步骤：分析")
	require.Contains(t, out, "## 事项背景")
	require.Contains(t, out, "## 上游步骤产物")
	require.Contains(t, out, "独立运行")
	require.NotContains(t, out, "返工意见")
	require.NotContains(t, out, "人工补充意见")
	// 没填这一步做什么时给一句可执行的白话兜底。
	require.Contains(t, out, "这一步还没有填写要做什么")
}

func TestRenderTaskBookHumanContextSources(t *testing.T) {
	in := TaskBookInput{
		Node: types.SopNode{ID: "n1", Name: "实现", TaskPrompt: "写代码"},
		HumanContext: []types.SopHumanContextEntry{
			{Source: "system_reminder", Text: "该步骤已超时一次"},
			{Source: "human", Text: "优先移动端"},
		},
		AnsweredClarifications: []types.SopItemClarification{
			{Question: "要不要兼容旧接口", Answer: "要", Status: types.SopClarificationStatusAnswered},
			{Question: "还没回答的问题", Answer: "", Status: types.SopClarificationStatusOpen},
		},
	}
	out := RenderTaskBook(in)
	require.Contains(t, out, "该步骤已超时一次")
	require.Contains(t, out, "优先移动端")
	require.Contains(t, out, "要不要兼容旧接口")
	require.Contains(t, out, "要")
	require.NotContains(t, out, "还没回答的问题")
	require.Contains(t, out, "系统提醒")
}

func TestResolveSkillsOrderAndNilRole(t *testing.T) {
	role := &types.CustomAgent{}
	role.Config.SelectedSkills = []string{"c", "a", "b"}
	got := ResolveSkills(role, []string{"b", "a"})
	require.Equal(t, []string{"a", "b"}, got, "交集按角色顺序输出，保证渲染稳定")
	require.Nil(t, ResolveSkills(nil, nil))
	require.Equal(t, []string{"x"}, ResolveSkills(nil, []string{"x"}))
	require.Equal(t, []string{}, ResolveSkills(role, []string{}))
}

func TestRenderAgentSystemOmitsEmptySections(t *testing.T) {
	s := RenderAgentSystem(nil, nil, "", "只做这一件事")
	require.Contains(t, s, "只做这一件事")
	require.NotContains(t, s, "可用技能")
	role := &types.CustomAgent{Name: "工程师", Description: "负责实现"}
	role.Config.SystemPrompt = "守规矩"
	s2 := RenderAgentSystem(role, []string{"go-test"}, "", "目标")
	require.Contains(t, s2, "工程师")
	require.Contains(t, s2, "守规矩")
	require.Contains(t, s2, "go-test")
	require.Contains(t, s2, "目标")
}
