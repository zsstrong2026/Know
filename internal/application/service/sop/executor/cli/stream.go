// stream.go 是 CLI 执行的统一运行时：私有 HOME → 配置与系统提示落盘 → prompt 投递
// → 启动进程 → 逐行归一化 → 终态帧后排水杀灭 → 按 spec §5.2 判成败。
//
// 成败判定（与 spec 一致，顺序不可换）：
//  1. 出现 result 终态帧：按帧里的 status（success/error/suspended）定结论；
//  2. 无 result 帧但 exit 0：门禁结论留空，交给引擎三段裁定（产物/门禁脚本说话）；
//  3. exit ≠ 0：打回，理由带 stderr 尾部；
//  4. FailureHint 认定为「要人拍板」（如轮次用尽）：转人工，不再自动重跑。
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor/proc"
	"github.com/Tencent/WeKnora/internal/types"
)

// stderrTailBytes 是判成败时保留的 stderr 尾部长度（错误理由只给最后几行）。
const stderrTailBytes = 2000

// streamScanBuf 是单行上限：CLI 的 JSONL 里有整段代码时行会很长。
const streamScanBuf = 4 << 20

// promptScanBuf 与之配套。
const promptWrapHeader = "<system_instructions>\n"

// RunStream 跑一次 CLI 节点。
func RunStream(
	ctx context.Context, a Adapter, tb executor.TaskBook, emit func(executor.StreamEvent),
) (executor.ExecResult, error) {
	if a == nil {
		return executor.ExecResult{}, errors.New("CLI 适配器未就绪")
	}
	root := strings.TrimSpace(tb.WorkspaceRoot)
	if root == "" {
		return executor.ExecResult{}, errors.New("CLI 执行缺少工作区")
	}

	ws := &engine.Workspace{Root: root}
	home, err := PrivateHome(ws, a.Kind())
	if err != nil {
		return executor.ExecResult{}, err
	}
	defer home.Close()

	// 配置与系统提示先落盘：CLI 启动时就要读它们。
	// MCP 段在这里统一合并（而不是每个适配器各写一遍）：只渲染节点勾选的 server
	// 与平台 Know 端点，凭据进头不进文件正文。
	configFiles, mcpEnv := MergeMCPIntoConfig(a.ConfigFiles(tb), RenderMCPConfig(a.Kind(), tb.MCPServers, knowEndpoint(tb)))
	for rel, content := range configFiles {
		if err := home.WriteFile(rel, []byte(content)); err != nil {
			return executor.ExecResult{}, err
		}
	}
	systemPrompt := strings.TrimSpace(tb.SystemPrompt)
	if err := home.WriteAgentSystem(systemPrompt); err != nil {
		return executor.ExecResult{}, err
	}

	prompt := tb.Prompt
	if a.SystemPromptMode() == SystemPromptWrap && systemPrompt != "" {
		// wrap 模式：CLI 没有原生系统提示入口，把规则包在正文前面。
		prompt = promptWrapHeader + systemPrompt + "\n</system_instructions>\n\n" + prompt
	}
	delivery, err := DeliverPrompt(prompt, a.PreferStdin(), ws, tb.NodeID, tb.Attempt)
	if err != nil {
		return executor.ExecResult{}, err
	}

	args := a.BuildArgs(tb, delivery.Argv, strings.TrimSpace(tb.ResumeSessionID))
	env := proc.ScriptEnv(mergedEnv(home, a, tb, mcpEnv))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut bool
	var mu sync.Mutex
	if tb.Timeout > 0 {
		timer := time.AfterFunc(tb.Timeout, func() {
			mu.Lock()
			timedOut = true
			mu.Unlock()
			cancel()
		})
		defer timer.Stop()
	}

	stdoutR, stdoutW := io.Pipe()
	defer stdoutR.Close()
	var stderr bytes.Buffer
	stderrGuard := &limitedWriter{w: &stderr, remaining: stderrTailBytes * 2}

	var stdin io.Reader
	if delivery.Stdin != "" {
		stdin = strings.NewReader(delivery.Stdin)
	}
	p, err := proc.StartIO(runCtx, a.BinaryName(), args, env, root, stdin, stdoutW, stderrGuard)
	if err != nil {
		_ = stdoutW.Close()
		return executor.ExecResult{}, fmt.Errorf("启动 %s 失败：%w", a.Kind(), err)
	}

	// 终态帧到达后给 CLI 2.5s 自行退出，到点仍活着就杀整棵树（spec §5.2）。
	collected := collectStream(stdoutR, a, emit, func() {
		proc.DrainThenKill(p, proc.DrainGrace)
	})
	// 进程退出后写端才会关闭；Wait 先等进程，再关写端让读协程收尾。
	code, waitErr := p.Wait()
	_ = stdoutW.Close()
	collected.wait()

	mu.Lock()
	timeout := timedOut
	mu.Unlock()

	res := collected.result()
	stderrText := strings.TrimSpace(stderr.String())
	switch {
	case timeout:
		res.GateResult = types.SopGateRework
		res.GateReason = "CLI 执行超时被终止" + withTail(stderrText)
	case ctx.Err() != nil:
		// 运行被取消：不是节点结论，交引擎按「取消不是失败」收口。
		return res, ctx.Err()
	case collected.hasResult:
		// 终态帧已定结论（success→pass / error→rework|human）。
	case waitErr != nil || code != 0:
		if hint := a.FailureHint(stderrText); hint == types.SopGateHuman {
			res.GateResult = types.SopGateHuman
			res.GateReason = "CLI 提示需要人工介入" + withTail(stderrText)
		} else {
			res.GateResult = types.SopGateRework
			res.GateReason = fmt.Sprintf("CLI 退出码 %d%s", code, withTail(stderrText))
		}
	default:
		// 无终态帧且正常退出：结论留空，交引擎三段裁定。
		res.GateResult = ""
		res.GateReason = ""
	}
	return res, nil
}

// withTail 把 stderr 尾部拼进理由（空串返回空）。
func withTail(stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	return "：" + tailRunes(trimmed, 800)
}

// knowEndpoint 取本次注入的 Know MCP 端点（T37 供给；未供给时 URL 为空=不注入）。
func knowEndpoint(tb executor.TaskBook) KnowMCPEndpoint {
	if tb.KnowMCP == nil {
		return KnowMCPEndpoint{}
	}
	return *tb.KnowMCP
}

// mcpConfigPath 是 claude --mcp-config 指向的文件（私有 HOME 内的相对路径，
// RunStream 以 $HOME 为工作基底写入；这里用绝对形式由 CLI 自行展开）。
const mcpConfigPath = ".sop/mcp.json"

// mergedEnv 组装子进程环境：系统环境打底 → 私有 HOME → 凭据/适配器变量。
//
// HOME/USERPROFILE 指向私有目录：CLI 会把凭据与缓存写进 HOME，共享 HOME 等于
// 把 A 节点的密钥暴露给 B 节点（spec §5.2）。
func mergedEnv(home *Home, a Adapter, tb executor.TaskBook, mcpEnv map[string]string) map[string]string {
	extra := map[string]string{}
	for k, v := range a.Env(tb) {
		extra[k] = v
	}
	// MCP 的 env 类配置（opencode 整段 JSON）覆盖同名项。
	for k, v := range mcpEnv {
		extra[k] = v
	}
	if home != nil && home.Dir != "" {
		extra["HOME"] = home.Dir
		extra["USERPROFILE"] = home.Dir
		extra["XDG_CONFIG_HOME"] = home.Dir
		extra["XDG_CACHE_HOME"] = home.Dir
		// 二次确认：即便适配器忘了映射，密钥也只在环境里出现一次。
		if tb.Credential != nil {
			for k, v := range CredentialEnv(tb.Credential) {
				extra[k] = v
			}
		}
	}
	return extra
}

// streamCollector 逐行解析 stdout，收集正文/用量/终态结论。
type streamCollector struct {
	// onTerminal 在首个终态帧到达时触发一次（用于「排水后杀灭」）。
	onTerminal   func()
	terminalOnce sync.Once
	adapter      Adapter
	emit         func(executor.StreamEvent)
	done         chan struct{}
	mu           sync.Mutex
	text         strings.Builder
	events       []executor.StreamEvent
	usage        types.SopUsage
	resumeID     string
	hasResult    bool
	// resultStatus 是终态帧里的 status（success/error/suspended）。
	resultStatus string
	resultReason string
}

// collectStream 起一个读协程。
func collectStream(
	r io.Reader, a Adapter, emit func(executor.StreamEvent), onTerminal func(),
) *streamCollector {
	c := &streamCollector{adapter: a, emit: emit, done: make(chan struct{}), onTerminal: onTerminal}
	go func() {
		defer close(c.done)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), streamScanBuf)
		for scanner.Scan() {
			line := scanner.Text()
			events, terminal := a.ParseLine(line)
			if len(events) == 0 && !terminal {
				continue
			}
			c.absorb(events, terminal)
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			// 读端被掐断（进程被杀）属于正常收尾，不再补假事件。
			_, _ = io.Copy(io.Discard, r)
		}
	}()
	return c
}

// absorb 应用一批事件（含终态标记）。
func (c *streamCollector) absorb(events []executor.StreamEvent, terminal bool) {
	c.mu.Lock()
	for _, ev := range events {
		c.events = append(c.events, ev)
		if ev.Type == executor.StreamEventTextDelta {
			c.text.WriteString(ev.Text)
		}
		if ev.Type == executor.StreamEventResult {
			c.hasResult = true
			var payload struct {
				Status string `json:"status"`
				Reason string `json:"reason"`
				Usage  *struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			}
			if len(ev.Payload) > 0 && json.Unmarshal(ev.Payload, &payload) == nil {
				c.resultStatus = payload.Status
				c.resultReason = payload.Reason
				if payload.Usage != nil {
					c.usage.InputTokens += payload.Usage.InputTokens
					c.usage.OutputTokens += payload.Usage.OutputTokens
				}
			}
		}
	}
	if id := c.adapter.ResumeID(events); id != "" {
		c.resumeID = id
	}
	c.mu.Unlock()

	for _, ev := range events {
		if c.emit != nil {
			if ev.At.IsZero() {
				ev.At = time.Now()
			}
			c.emit(ev)
		}
	}
	if terminal && c.onTerminal != nil {
		c.terminalOnce.Do(c.onTerminal)
	}
}

// wait 等读协程结束（进程退出后管道关闭，最多再等一小段）。
func (c *streamCollector) wait() {
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
	}
}

// result 汇总成本轮结果：正文/续跑 id/用量/终态结论。
func (c *streamCollector) result() executor.ExecResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := executor.ExecResult{
		Output:          strings.TrimSpace(c.text.String()),
		ResumeSessionID: c.resumeID,
		Usage:           c.usage,
	}
	if c.hasResult {
		switch c.resultStatus {
		case "success", "":
			res.GateResult = types.SopGatePass
			res.GateReason = firstNonEmpty(c.resultReason, "CLI 报告执行完成")
		case "suspended":
			// CLI 侧挂起：没有统一的人工答复通道，交门禁转人工。
			res.GateResult = types.SopGateHuman
			res.GateReason = firstNonEmpty(c.resultReason, "CLI 等待人工输入")
		default:
			res.GateResult = types.SopGateRework
			res.GateReason = firstNonEmpty(c.resultReason, "CLI 报告执行失败")
		}
	}
	return res
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// tailRunes 截取字符串尾部（按 rune，避免切坏多字节字符）。
func tailRunes(s string, limit int) string {
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return "…" + string(rs[len(rs)-limit:])
}

// limitedWriter 只保留前 remaining 字节，并在超出时标记（stderr 只用于判因，
// 不需要全文；限制内存也避免恶意 CLI 刷爆）。
type limitedWriter struct {
	w         io.Writer
	remaining int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.remaining <= 0 {
		return len(p), nil
	}
	buf := p
	if len(buf) > l.remaining {
		buf = buf[:l.remaining]
	}
	n, err := l.w.Write(buf)
	l.remaining -= n
	return len(p), err
}

// ensure exec is referenced even if a future refactor drops it.
var _ = exec.LookPath
