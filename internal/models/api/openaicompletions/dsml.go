package openaicompletions

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// DeepSeek models (V3.2 and later) sometimes leak their native tool-call
// syntax into the content channel instead of returning structured
// tool_calls, typically when the serving gateway's parser misses it:
//
//	<｜DSML｜function_calls>
//	<｜DSML｜invoke name="search_knowledge">
//	<｜DSML｜parameter name="query" string="true">foo</｜DSML｜parameter>
//	</｜DSML｜invoke>
//	</｜DSML｜function_calls>
//
// The encoding drifts between builds and gateways: the bar may be doubled
// (<｜｜DSML｜｜) and the outer tag shortened to "calls". Both are accepted.
//
// Left alone, the round reaches the agent as a plain answer and the turn ends
// with the markup shown to the user. dsmlFilter holds that markup back from
// the stream and recovers the calls from it. When the request disables tool
// calls (tool_choice="none") the markup is stripped instead of recovered: it
// is still protocol noise, but executing it would violate the request.

// dsmlIndexBase keeps recovered calls clear of the vendor's own indices.
const dsmlIndexBase = 1 << 20

const (
	// dsmlBar matches a bar separator. The model emits the full-width bar
	// (U+FF5C), some gateways normalise it to ASCII '|', and later builds
	// repeat it ("<｜｜DSML｜｜"), so one or more of either is accepted.
	dsmlBar = `\s*[｜|]+\s*`
	// dsmlEnvelope names the outer tag. DeepSeek V3.2 emits "function_calls";
	// later builds shorten it to "calls" (also seen as "tool_calls").
	dsmlEnvelope = `\w*calls`
)

var (
	// dsmlMarkerRe finds the first opening of any DSML tag.
	dsmlMarkerRe = regexp.MustCompile(`<` + dsmlBar + `DSML` + dsmlBar)
	// dsmlPartialRe matches a trailing fragment that may still grow into a
	// marker ("<", "<｜", "<｜D", …) so it can be held across chunk borders.
	dsmlPartialRe = regexp.MustCompile(
		`^<[｜|\s]*(?:D(?:S(?:M(?:L[｜|\s]*)?)?)?)?$`)

	dsmlInvokeRe = regexp.MustCompile(
		`(?s)<` + dsmlBar + `DSML` + dsmlBar + `invoke\s+name\s*=\s*"([^"]+)"\s*>(.*?)</` +
			dsmlBar + `DSML` + dsmlBar + `invoke\s*>`)
	dsmlParamRe = regexp.MustCompile(
		`(?s)<` + dsmlBar + `DSML` + dsmlBar + `parameter\s+name\s*=\s*"([^"]+)"` +
			`(?:\s+string\s*=\s*"(true|false)")?\s*>(.*?)</` + dsmlBar + `DSML` + dsmlBar + `parameter\s*>`)
	dsmlBlockRe = regexp.MustCompile(
		`(?s)^<` + dsmlBar + `DSML` + dsmlBar + dsmlEnvelope + `\s*>(.*?)</` + dsmlBar + `DSML` + dsmlBar +
			dsmlEnvelope + `\s*>\s*$`)
)

// dsmlFilter is fed the streamed content. Everything before the first DSML
// tag passes through; from that tag on the text is held until Flush.
type dsmlFilter struct {
	options   *api.Options
	prefix    strings.Builder
	held      strings.Builder
	capturing bool
}

// dsmlToolChoiceNone reports whether the request forbids tool calls. Markup
// still has to be stripped in that case; it just must not be recovered.
func dsmlToolChoiceNone(opts *api.Options) bool {
	return opts != nil && opts.ToolChoice == "none"
}

// Feed returns the part of chunk that is safe to show now.
func (f *dsmlFilter) Feed(chunk string) string {
	// Without tools there is nothing to recover, so only tool_choice="none"
	// (the final-answer phase) still needs the markup held back for stripping.
	if f.options == nil || (len(f.options.Tools) == 0 && !dsmlToolChoiceNone(f.options)) {
		return chunk
	}
	if chunk == "" {
		return ""
	}
	if f.capturing {
		f.held.WriteString(chunk)
		return ""
	}
	buf := f.held.String() + chunk
	f.held.Reset()
	shown := buf
	if idx := indexDSML(buf); idx >= 0 {
		f.capturing = true
		f.held.WriteString(buf[idx:])
		shown = buf[:idx]
	} else if cut := partialMarkerStart(buf); cut >= 0 {
		// Keep a trailing partial marker back: the tag may be split across chunks.
		f.held.WriteString(buf[cut:])
		shown = buf[:cut]
	}
	f.prefix.WriteString(shown)
	return shown
}

// Flush ends the stream: it returns the held text that turned out not to be
// tool calls, and the calls recovered from the rest.
func (f *dsmlFilter) Flush(recoverCalls bool) (string, []types.LLMToolCall) {
	prefix := f.prefix.String()
	held := f.held.String()
	f.prefix.Reset()
	f.held.Reset()
	f.capturing = false
	if held == "" {
		return "", nil
	}
	// Parse the whole retained tail: the quoted-example check needs the text
	// that was already streamed, but only the held part is returned.
	rest, calls := extractDSMLToolCalls(prefix+held, f.options)
	if len(calls) > 0 && !recoverCalls {
		// The turn ends for another reason, so nothing can be executed here.
		// Keep the text visible rather than dropping it silently.
		return held, nil
	}
	return rest[len(prefix):], calls
}

// extractDSMLToolCalls recovers a complete trailing protocol block, optionally
// preceded by a plain-text preamble. Ambiguous examples, undeclared tools and
// malformed calls stay intact: a partial parse must not silently change a call.
// With tool calls disabled the block is only stripped, never recovered.
func extractDSMLToolCalls(content string, opts *api.Options) (string, []types.LLMToolCall) {
	if opts == nil {
		return content, nil
	}
	if dsmlToolChoiceNone(opts) {
		return stripDSMLBlock(content), nil
	}
	if len(opts.Tools) == 0 {
		return content, nil
	}
	rest, calls, ok := parseDSMLToolCalls(content, opts, true)
	if !ok {
		return content, nil
	}
	if opts.ParallelToolCalls != nil && !*opts.ParallelToolCalls && len(calls) > 1 {
		return content, nil
	}
	return rest, calls
}

// stripDSMLBlock removes a complete trailing protocol block and keeps the text
// before it. Tool names are not validated here: when tool calls are disabled,
// any well-formed block is protocol noise rather than an answer.
func stripDSMLBlock(content string) string {
	rest, calls, ok := parseDSMLToolCalls(content, nil, false)
	if !ok || len(calls) == 0 {
		return content
	}
	return rest
}

// parseDSMLToolCalls parses a complete trailing protocol block. When
// requireDeclared is true every invoke must target a declared and permitted
// tool; otherwise only the structure is checked.
func parseDSMLToolCalls(
	content string, opts *api.Options, requireDeclared bool,
) (string, []types.LLMToolCall, bool) {
	start := indexDSML(content)
	if start < 0 {
		return content, nil, false
	}
	prefix := content[:start]
	// Only recover a block starting on its own line. Code spans/fences in the
	// preamble make the context ambiguous, so conservatively leave it alone.
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	if strings.TrimSpace(prefix[lineStart:]) != "" ||
		strings.HasPrefix(prefix[lineStart:], "    ") || strings.HasPrefix(prefix[lineStart:], "\t") ||
		strings.Contains(prefix, "`") || strings.Contains(prefix, "~~~") {
		return content, nil, false
	}
	block := dsmlBlockRe.FindStringSubmatch(content[start:])
	if len(block) == 0 {
		return content, nil, false
	}
	allowed := map[string]bool{}
	if requireDeclared {
		for _, tool := range opts.Tools {
			if tool.Type != "" && tool.Type != "function" {
				continue
			}
			name := tool.Function.Name
			if opts.ToolChoice == "" || opts.ToolChoice == "auto" || opts.ToolChoice == "required" ||
				opts.ToolChoice == name {
				allowed[name] = true
			}
		}
	}
	inner := block[1]
	matches := dsmlInvokeRe.FindAllStringSubmatchIndex(inner, -1)
	calls := make([]types.LLMToolCall, 0, len(matches))
	consumed := 0
	for _, m := range matches {
		name, body := inner[m[2]:m[3]], inner[m[4]:m[5]]
		if strings.TrimSpace(inner[consumed:m[0]]) != "" || (requireDeclared && !allowed[name]) {
			return content, nil, false
		}
		consumed = m[1]
		args := map[string]any{}
		paramEnd := 0
		for _, p := range dsmlParamRe.FindAllStringSubmatchIndex(body, -1) {
			key := body[p[2]:p[3]]
			if strings.TrimSpace(body[paramEnd:p[0]]) != "" {
				return content, nil, false
			}
			if _, duplicate := args[key]; duplicate {
				return content, nil, false
			}
			paramEnd = p[1]
			isString := ""
			if p[4] >= 0 {
				isString = body[p[4]:p[5]]
			}
			value, ok := dsmlParamValue(body[p[6]:p[7]], isString)
			if !ok {
				return content, nil, false
			}
			args[key] = value
		}
		if strings.TrimSpace(body[paramEnd:]) != "" {
			return content, nil, false
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return content, nil, false
		}
		calls = append(calls, types.LLMToolCall{
			ID:       "call_dsml_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
			Type:     "function",
			Function: types.FunctionCall{Name: name, Arguments: string(raw)},
		})
	}
	if len(calls) == 0 || strings.TrimSpace(inner[consumed:]) != "" {
		return content, nil, false
	}
	return prefix, calls, true
}

// dsmlParamValue keeps JSON numbers verbatim instead of rounding them through
// float64. An explicitly non-string parameter must contain complete JSON.
func dsmlParamValue(body, isString string) (any, bool) {
	if isString == "true" {
		return body, true
	}
	trimmed := strings.TrimSpace(body)
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed), true
	}
	return body, isString == ""
}

// dsmlToolCallDeltas turns recovered calls into stream deltas numbered after
// any native calls so the two never share an index.
func dsmlToolCallDeltas(calls []types.LLMToolCall, base int) []api.ToolCallDelta {
	deltas := make([]api.ToolCallDelta, 0, len(calls))
	for i, c := range calls {
		deltas = append(deltas, api.ToolCallDelta{
			Index: base + i, ID: c.ID, Type: c.Type, Name: c.Function.Name, Arguments: c.Function.Arguments,
		})
	}
	return deltas
}

func indexDSML(s string) int {
	if !strings.Contains(s, "DSML") {
		return -1
	}
	if loc := dsmlMarkerRe.FindStringIndex(s); loc != nil {
		return loc[0]
	}
	return -1
}

// partialMarkerStart reports where a trailing prefix of a DSML marker begins,
// or -1.
func partialMarkerStart(s string) int {
	i := strings.LastIndexByte(s, '<')
	if i < 0 {
		return -1
	}
	if !dsmlPartialRe.MatchString(trimPartialRune(s[i:])) {
		return -1
	}
	return i
}

// trimPartialRune drops a trailing incomplete UTF-8 sequence: a chunk can split
// the multi-byte bar, and the regex would then see an invalid byte instead of
// the rune it belongs to.
func trimPartialRune(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size > 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
