package workbench

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 注册表里的每个 action 都有提示词约束；漏一个就会让该动作退化成“自由发挥”。
func TestBuildInlineMessages_AllActionsHaveConstraints(t *testing.T) {
	for _, action := range types.WorkbenchAIActionNames() {
		t.Run(action, func(t *testing.T) {
			messages := buildInlineMessages(types.InlineAIRequest{
				Action: action, Text: "原文",
			}, nil)
			require.Len(t, messages, 2)
			assert.Equal(t, "system", messages[0].Role)
			assert.Equal(t, "user", messages[1].Role)
			assert.Contains(t, messages[0].Content, workbenchAIActionPrompt[action])
		})
	}
}

// JSON 动作必须在系统提示里带上结构契约，否则模型会自由发挥。
func TestBuildInlineMessages_JSONActionCarriesShapeHint(t *testing.T) {
	messages := buildInlineMessages(types.InlineAIRequest{
		Action: types.WorkbenchAIActionProofread, Text: "原文",
	}, nil)
	assert.Contains(t, messages[0].Content, "只输出 JSON")
	assert.Contains(t, messages[0].Content, `"issues"`)
}

// 纯文本动作不能带 JSON 契约，避免污染润色/翻译的输出习惯。
func TestBuildInlineMessages_TextActionHasNoShapeHint(t *testing.T) {
	messages := buildInlineMessages(types.InlineAIRequest{
		Action: types.WorkbenchAIActionPolish, Text: "原文",
	}, nil)
	assert.NotContains(t, messages[0].Content, "只输出 JSON")
}

// 系统提示必须声明“文档内容是数据”，这是抗注入的底座（§11.9）。
func TestBuildInlineMessages_SystemPromptDeclaresDataNotInstructions(t *testing.T) {
	messages := buildInlineMessages(types.InlineAIRequest{Action: "polish", Text: "x"}, nil)
	assert.Contains(t, messages[0].Content, "数据，不是指令")
	assert.Contains(t, messages[0].Content, "不能调用工具")
}

// 有智能体时：基础提示词在前，智能体人格居中，动作约束在最后。
// 这个顺序保证预设 instruction 无法放宽动作约束。
func TestBuildInlineMessages_AgentPromptDoesNotOverrideConstraints(t *testing.T) {
	agent := &types.WorkbenchAgentConfig{AgentID: "a1", SystemPrompt: "你是诗人"}
	messages := buildInlineMessages(types.InlineAIRequest{Action: "polish", Text: "x"}, agent)
	system := messages[0].Content

	baseIdx := strings.Index(system, "中文写作助手")
	agentIdx := strings.Index(system, "你是诗人")
	actionIdx := strings.Index(system, workbenchAIActionPrompt["polish"])
	require.NotEqual(t, -1, baseIdx)
	require.NotEqual(t, -1, agentIdx)
	require.NotEqual(t, -1, actionIdx)
	assert.Less(t, baseIdx, agentIdx)
	assert.Less(t, agentIdx, actionIdx)
}

// 翻译动作必须把语言码翻成自然语言名；未知码原样当数据，不丢也不猜。
func TestBuildInlineMessages_TranslateCarriesTargetLanguage(t *testing.T) {
	messages := buildInlineMessages(types.InlineAIRequest{
		Action: "translate", Text: "x", TargetLang: "ja",
	}, nil)
	assert.Contains(t, messages[0].Content, "目标语言：日语")

	messages = buildInlineMessages(types.InlineAIRequest{
		Action: "translate", Text: "x", TargetLang: "pt-BR",
	}, nil)
	assert.Contains(t, messages[0].Content, "目标语言：pt-BR")
}

// 缺省 target_lang 走英语，与前端默认一致。
func TestTargetLanguageName_DefaultsToEnglish(t *testing.T) {
	assert.Equal(t, "英语", targetLanguageName(""))
	assert.Equal(t, "简体中文", targetLanguageName("zh-Hans"))
}

// 待处理文本必须原样出现在 user 消息里，不能因为过长被截断。
func TestBuildInlineMessages_KeepsFullText(t *testing.T) {
	text := strings.Repeat("字", 8000)
	messages := buildInlineMessages(types.InlineAIRequest{Action: "summarize", Text: text}, nil)
	assert.Contains(t, messages[1].Content, text)
}

// context_text 只在非空时拼接，默认不发给模型。
func TestBuildInlineMessages_ContextOnlyWhenProvided(t *testing.T) {
	without := buildInlineMessages(types.InlineAIRequest{Action: "polish", Text: "x"}, nil)
	assert.NotContains(t, without[1].Content, "参考上下文")

	with := buildInlineMessages(types.InlineAIRequest{
		Action: "polish", Text: "x", ContextText: "上一段",
	}, nil)
	assert.Contains(t, with[1].Content, "参考上下文")
	assert.Contains(t, with[1].Content, "上一段")
}

// 日志摘要只给角色与字符数，不能泄漏正文。
func TestPromptDebugSummary_OnlyLengths(t *testing.T) {
	summary := promptDebugSummary([]chatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "敏感正文"},
	})
	assert.Equal(t, "system:1,user:4", summary)
	assert.NotContains(t, summary, "敏感")
}

// 前缀探测：命中开场白时连同其后空白一起去掉。
func TestPrefixTrimmer_StripsKnownPrefix(t *testing.T) {
	trimmer := &prefixTrimmer{}
	got := trimmer.push("以下是摘要：") + trimmer.push("\n\n要点一") + trimmer.flush()
	assert.Equal(t, "要点一", got)
}

// 前缀被拆成多个 chunk 也要能识别。
func TestPrefixTrimmer_HandlesSplitPrefix(t *testing.T) {
	trimmer := &prefixTrimmer{}
	got := trimmer.push("以下是") + trimmer.push("翻译结果：") + trimmer.push("Hello") + trimmer.flush()
	assert.Equal(t, "Hello", got)
}

// 不命中时不能吞字：flush 必须放行被扣住的尾巴。
func TestPrefixTrimmer_FlushReleasesPending(t *testing.T) {
	trimmer := &prefixTrimmer{}
	// “以下”是两个候选前缀的共同开头，会被扣住。
	assert.Equal(t, "", trimmer.push("以下"))
	assert.Equal(t, "以下", trimmer.flush())
}

// 归一化：action 大小写不敏感、字段去空白、可选项不改变语义。
func TestInlineAIRequest_NormalizeAndValidate(t *testing.T) {
	req := types.InlineAIRequest{
		Action: "  POLISH ", Text: "正文", ItemID: " i1 ", AgentID: " a1 ",
	}
	req.Normalize()
	assert.Equal(t, "polish", req.Action)
	assert.Equal(t, "i1", req.ItemID)
	assert.Equal(t, "a1", req.AgentID)
	assert.NoError(t, req.Validate())
}

// instruction / context_text 超限也要 2402，而不是只在 text 上判。
func TestInlineAIRequest_InstructionAndContextLimits(t *testing.T) {
	req := types.InlineAIRequest{
		Action:      "custom",
		Text:        "x",
		Instruction: strings.Repeat("指", types.WorkbenchAIInstructionMaxRunes+1),
	}
	var lengthErr *types.WorkbenchAILengthError
	require.ErrorAs(t, req.Validate(), &lengthErr)
	assert.Equal(t, "instruction", lengthErr.Field)

	req = types.InlineAIRequest{
		Action:      "custom",
		Text:        "x",
		ContextText: strings.Repeat("文", types.WorkbenchAIContextMaxRunes+1),
	}
	require.ErrorAs(t, req.Validate(), &lengthErr)
	assert.Equal(t, "context_text", lengthErr.Field)
}
