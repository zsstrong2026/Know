package openaicompletions

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

var dsmlTestOptions = &api.Options{Tools: []api.Tool{{
	Type: "function", Function: api.FunctionDef{Name: "search_knowledge"},
}}}

const dsmlSample = "先查一下。\n<｜DSML｜function_calls>\n" +
	"<｜DSML｜invoke name=\"search_knowledge\">\n" +
	"<｜DSML｜parameter name=\"query\" string=\"true\">c2p_tber_0024</｜DSML｜parameter>\n" +
	"<｜DSML｜parameter name=\"limit\" string=\"false\">5</｜DSML｜parameter>\n" +
	"</｜DSML｜invoke>\n" +
	"<｜DSML｜invoke name=\"search_knowledge\">\n" +
	"<｜DSML｜parameter name=\"query\" string=\"true\">c2p_err_0024 是什么故障</｜DSML｜parameter>\n" +
	"</｜DSML｜invoke>\n" +
	"</｜DSML｜function_calls>"

func TestExtractDSMLToolCalls(t *testing.T) {
	rest, calls := extractDSMLToolCalls(dsmlSample, dsmlTestOptions)
	if rest != "先查一下。\n" {
		t.Fatalf("rest = %q", rest)
	}
	if len(calls) != 2 || calls[0].Function.Name != "search_knowledge" {
		t.Fatalf("calls = %+v", calls)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["query"] != "c2p_tber_0024" || args["limit"] != float64(5) {
		t.Fatalf("args = %v", args)
	}
	if calls[0].ID == "" || calls[0].ID == calls[1].ID {
		t.Fatalf("ids = %q %q", calls[0].ID, calls[1].ID)
	}
}

func TestDSMLFilterStreamedByteByByte(t *testing.T) {
	f := dsmlFilter{options: dsmlTestOptions}
	shown := ""
	for i := range len(dsmlSample) {
		shown += f.Feed(dsmlSample[i : i+1])
	}
	rest, calls := f.Flush(true)
	if shown != "先查一下。\n" || rest != "" {
		t.Fatalf("shown = %q rest = %q", shown, rest)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
}

func TestDSMLFilterPassesPlainText(t *testing.T) {
	f := dsmlFilter{options: dsmlTestOptions}
	shown := f.Feed("a < b and <c")
	rest, calls := f.Flush(true)
	if shown+rest != "a < b and <c" || calls != nil {
		t.Fatalf("shown=%q rest=%q calls=%v", shown, rest, calls)
	}
}

func TestDSMLMalformedKeptVisible(t *testing.T) {
	in := "x <｜DSML｜function_calls> broken"
	rest, calls := extractDSMLToolCalls(in, dsmlTestOptions)
	if rest != in || calls != nil {
		t.Fatalf("rest=%q calls=%v", rest, calls)
	}
}

// 工具被禁用时不能恢复调用，但协议文本仍必须从答案里清掉。
func TestDSMLToolChoiceNoneStripsWithoutRecovering(t *testing.T) {
	opts := &api.Options{Tools: dsmlTestOptions.Tools, ToolChoice: "none"}
	rest, calls := extractDSMLToolCalls(dsmlSample, opts)
	if rest != "先查一下。\n" || calls != nil {
		t.Fatalf("rest=%q calls=%v", rest, calls)
	}
}

// 最终回答阶段可能既不传工具又禁用调用（ToolChoice=none），同样要清理。
func TestDSMLToolChoiceNoneWithoutToolsStrips(t *testing.T) {
	opts := &api.Options{ToolChoice: "none"}
	rest, calls := extractDSMLToolCalls(dsmlSample, opts)
	if rest != "先查一下。\n" || calls != nil {
		t.Fatalf("rest=%q calls=%v", rest, calls)
	}
}

func TestDSMLFilterToolChoiceNoneStripsStream(t *testing.T) {
	f := dsmlFilter{options: &api.Options{ToolChoice: "none"}}
	shown := ""
	for i := range len(dsmlSample) {
		shown += f.Feed(dsmlSample[i : i+1])
	}
	rest, calls := f.Flush(true)
	if shown != "先查一下。\n" || rest != "" || calls != nil {
		t.Fatalf("shown=%q rest=%q calls=%v", shown, rest, calls)
	}
}

// 结构不完整时保持原样，避免为了清理而静默截断答案。
func TestDSMLToolChoiceNoneKeepsIncompleteBlock(t *testing.T) {
	in := "先查一下。\n<｜DSML｜function_calls><｜DSML｜invoke name=\"search_knowledge\">"
	rest, calls := extractDSMLToolCalls(in, &api.Options{ToolChoice: "none"})
	if rest != in || calls != nil {
		t.Fatalf("rest=%q calls=%v", rest, calls)
	}
}

// 实际线上泄漏的形态：双竖线 + 外层标签简写为 calls。
const dsmlDoubleBarSample = "先跑一下。\n<｜｜DSML｜｜ calls>\n" +
	"<｜｜DSML｜｜ invoke name=\"shell_exec\">\n" +
	"<｜｜DSML｜｜ parameter name=\"command\" string=\"true\">ls -la</｜｜DSML｜｜ parameter>\n" +
	"</｜｜DSML｜｜ invoke>\n" +
	"</｜｜DSML｜｜ calls>"

var dsmlShellTestOptions = &api.Options{Tools: []api.Tool{{
	Type: "function", Function: api.FunctionDef{Name: "shell_exec"},
}}}

func TestDSMLDoubleBarShortEnvelopeRecovered(t *testing.T) {
	rest, calls := extractDSMLToolCalls(dsmlDoubleBarSample, dsmlShellTestOptions)
	if rest != "先跑一下。\n" {
		t.Fatalf("rest = %q", rest)
	}
	if len(calls) != 1 || calls[0].Function.Name != "shell_exec" {
		t.Fatalf("calls = %+v", calls)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["command"] != "ls -la" {
		t.Fatalf("args = %v", args)
	}
}

func TestDSMLDoubleBarStreamedByteByByte(t *testing.T) {
	f := dsmlFilter{options: &api.Options{ToolChoice: "none"}}
	shown := ""
	for i := range len(dsmlDoubleBarSample) {
		shown += f.Feed(dsmlDoubleBarSample[i : i+1])
	}
	rest, calls := f.Flush(true)
	if shown != "先跑一下。\n" || rest != "" || calls != nil {
		t.Fatalf("shown=%q rest=%q calls=%v", shown, rest, calls)
	}
}

// 其他外层标签写法同样要能识别。
func TestDSMLEnvelopeVariants(t *testing.T) {
	for _, envelope := range []string{"calls", "tool_calls", "function_calls"} {
		body := "先跑一下。\n<｜｜DSML｜｜" + envelope + ">\n" +
			"<｜｜DSML｜｜invoke name=\"shell_exec\">\n" +
			"<｜｜DSML｜｜parameter name=\"command\" string=\"true\">ls</｜｜DSML｜｜parameter>\n" +
			"</｜｜DSML｜｜invoke>\n</｜｜DSML｜｜" + envelope + ">"
		rest, calls := extractDSMLToolCalls(body, dsmlShellTestOptions)
		if rest != "先跑一下。\n" || len(calls) != 1 {
			t.Fatalf("%s: rest=%q calls=%v", envelope, rest, calls)
		}
	}
}

// 线上真实泄漏的整段答案（双竖线 + calls + 参数里带反引号）。
const dsmlLiveLeak = "<｜｜DSML｜｜ calls>\n" +
	"<｜｜DSML｜｜ invoke name=\"shell_exec\">\n" +
	"<｜｜DSML｜｜ parameter name=\"command\" string=\"true\">cd /workspace/draft && " +
	"grep -c '```mermaid' prd.md; grep -c '^!\\[' prd.md; ls /workspace/output/*.svg; ls preview" +
	"</｜｜DSML｜｜ parameter>\n" +
	"</｜｜DSML｜｜ invoke>\n" +
	"</｜｜DSML｜｜ calls>"

func TestDSMLLiveLeak(t *testing.T) {
	// 最终回答阶段：没有工具、tool_choice=none，整段都该消失。
	rest, calls := extractDSMLToolCalls(dsmlLiveLeak, &api.Options{ToolChoice: "none"})
	if rest != "" || calls != nil {
		t.Fatalf("strip: rest=%q calls=%v", rest, calls)
	}
	// 正常轮次：补回工具声明后应恢复为一次调用，而不是答案文本。
	rest, calls = extractDSMLToolCalls(dsmlLiveLeak, dsmlShellTestOptions)
	if rest != "" || len(calls) != 1 || calls[0].Function.Name != "shell_exec" {
		t.Fatalf("recover: rest=%q calls=%v", rest, calls)
	}
}
