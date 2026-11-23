// script.go 是脚本节点执行器：把一段 Git Bash 语义的脚本放进运行工作区执行，
// 边跑边把输出行推给 SSE，用退出码给门禁结论。
//
// 设计口径：
//   - 退出码即结论：0=通过；非 0=打回（附退出码与输出尾部）——脚本自己失败不是
//     执行器故障，交门禁三段裁定比自己判失败更符合「让检查脚本说话」；
//   - 超时/取消：杀整个进程组，不留下 `sleep`、构建进程这类继续吃机器的孤儿；
//     结论同样是「打回」，因为下一次尝试本来就该重跑；
//   - 环境受控：系统环境 + 节点声明的键值（凭据以 env 注入，不落盘、不进日志）；
//   - 日志双通道：逐行 Emit（前端直播）+ logs/<node>-attempt-N.log（事后复盘）。
//
// 依赖方向：本包只用标准库与 types，不 import 引擎（引擎负责产物收集与门禁）。
package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// scriptOutputTail 是结论摘要保留的输出尾部长度（字符数，按 rune 截断）。
const scriptOutputTail = 2000

// scriptScanBuf 是单行上限：构建日志里偶有很长的一行，默认 64KB 会直接把扫描打断。
const scriptScanBuf = 1 << 20

// ScriptProvider 脚本节点执行器（Kind = script）。
type ScriptProvider struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	proc   *exec.Cmd
	killed bool
}

// NewScriptProvider 构造脚本执行器。
func NewScriptProvider() *ScriptProvider { return &ScriptProvider{} }

// Kind 返回注册表键：script。
func (p *ScriptProvider) Kind() string { return ProviderKindScript }

// Prepare 脚本执行不需要准备环境（脚本自己负责建目录）。
func (p *ScriptProvider) Prepare(context.Context, *types.SopNode, string) error { return nil }

// Cancel 终止在途脚本所在的进程组（无在途执行时是 no-op）。
func (p *ScriptProvider) Cancel(ctx context.Context) error {
	p.mu.Lock()
	p.killed = true
	cmd := p.proc
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		killProcessGroup(cmd)
	}
	return nil
}

// running 报告是否有在途执行（测试用来等脚本真正跑起来）。
func (p *ScriptProvider) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.proc != nil
}

// Execute 跑一次脚本：合并输出逐行直播 + 落日志，按退出码给出门禁结论。
func (p *ScriptProvider) Execute(ctx context.Context, tb TaskBook, opts ExecOptions) (ExecResult, error) {
	script := strings.TrimSpace(tb.Script)
	if script == "" {
		return ExecResult{}, errors.New("脚本节点没有可执行的脚本正文")
	}
	root := strings.TrimSpace(tb.WorkspaceRoot)
	if root == "" {
		return ExecResult{}, errors.New("脚本执行缺少工作区")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 自己管超时而不是 context.WithTimeout：结束后要能区分「超时」「被取消」
	// 「脚本自己退出非 0」三种收场，把 cancel() 产生的 Canceled 误判成人工取消是
	// 最糟的错误——用户会看到一条没人操作过的「已取消」。
	var timedOut atomic.Bool
	if tb.Timeout > 0 {
		timer := time.AfterFunc(tb.Timeout, func() {
			timedOut.Store(true)
			cancel()
		})
		defer timer.Stop()
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return ExecResult{}, fmt.Errorf("创建输出管道失败：%w", err)
	}

	cmd := exec.Command(shellBinary(), shellArgs(script)...)
	cmd.Dir = root
	cmd.Env = scriptEnv(tb.Env)
	// 合并 stdout/stderr：模型与用户关心的是「发生了什么」，不是它来自哪个 fd；
	// 也让日志顺序与真实发生顺序一致。
	cmd.Stdout, cmd.Stderr = pw, pw
	attachProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return ExecResult{}, fmt.Errorf("启动脚本失败：%w", err)
	}
	// 父进程必须放手写端，否则读端永远等不到 EOF（子进程退出后仍然挂住）。
	_ = pw.Close()

	p.mu.Lock()
	p.proc, p.cancel, p.killed = cmd, cancel, false
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.proc == cmd {
			p.proc, p.cancel = nil, nil
		}
		p.mu.Unlock()
	}()

	logFile := openScriptLog(root, tb.NodeID, tb.Attempt)
	if logFile != nil {
		defer logFile.Close()
	}

	// ctx 结束（超时/取消）：按进程组杀，管道随之关闭，读取循环自然收尾。
	killWatch := make(chan struct{})
	var stopOnce sync.Once
	stopWatch := func() { stopOnce.Do(func() { close(killWatch) }) }
	go func() {
		select {
		case <-runCtx.Done():
			killProcessGroup(cmd)
		case <-killWatch:
		}
	}()

	output := drainScriptOutput(pr, opts.Emit, logFile)

	waitErr := cmd.Wait()
	stopWatch()
	_ = pr.Close()

	p.mu.Lock()
	userKilled := p.killed
	p.mu.Unlock()

	res := ExecResult{Output: tailRunes(output, scriptOutputTail)}
	switch {
	case timedOut.Load():
		res.GateResult = types.SopGateRework
		res.GateReason = scriptTimeoutReason(output)
	case userKilled || ctx.Err() == context.Canceled:
		res.GateResult = types.SopGateRework
		res.GateReason = scriptCancelReason(output)
	case waitErr != nil:
		var exitErr *exec.ExitError
		code := -1
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		}
		res.GateResult = types.SopGateRework
		res.GateReason = scriptExitReason(code, output)
	default:
		res.GateResult = types.SopGatePass
		res.GateReason = "脚本执行成功"
	}
	return res, nil
}

// ---- 输出收集 ----

// drainScriptOutput 逐行读合并输出：每行 Emit 一次 text_delta 并写日志，返回全文。
func drainScriptOutput(pr *os.File, emit func(StreamEvent), logFile io.Writer) string {
	var (
		buf strings.Builder
		mu  sync.Mutex
	)
	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, 64*1024), scriptScanBuf)
	push := func(text string) {
		mu.Lock()
		buf.WriteString(text)
		buf.WriteString("\n")
		mu.Unlock()
		if logFile != nil {
			_, _ = io.WriteString(logFile, text+"\n")
		}
		if emit != nil {
			emit(StreamEvent{Type: StreamEventTextDelta, Text: text + "\n", At: time.Now()})
		}
	}
	for scanner.Scan() {
		push(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		push("[读取脚本输出失败] " + err.Error())
	}
	mu.Lock()
	defer mu.Unlock()
	return strings.TrimRight(buf.String(), "\n")
}

// openScriptLog 打开 logs/<node>-attempt-N.log（失败只放弃日志，不影响执行）。
func openScriptLog(root, nodeID string, attempt int) *os.File {
	if attempt < 1 {
		attempt = 1
	}
	dir := filepath.Join(root, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	name := fmt.Sprintf("%s-attempt-%d.log", safeSegment(nodeID), attempt)
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// ---- 环境 ----

// scriptEnv 组装子进程环境：系统环境打底，节点声明的键值覆盖同名项。
//
// 覆盖而不是追加：同一变量出现两次时 glibc/Windows 的取值行为并不一致，显式去重
// 才能保证「节点写的值就是生效的值」。键名或值含 NUL 的一律跳过（exec 会直接报错）。
func scriptEnv(extra map[string]string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	overridden := make(map[string]bool, len(extra))
	for k := range extra {
		overridden[k] = true
	}
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if overridden[key] {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range sortedKeys(extra) {
		v := extra[k]
		if strings.ContainsRune(k, 0) || strings.ContainsRune(v, 0) || strings.Contains(k, "=") || k == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// sortedKeys 让环境变量的追加顺序稳定（同一份配置每次执行的环境字节一致，
// 便于复现与对账）。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- 结论文案 ----

// scriptExitReason 拼「退出码 + 输出尾部」的白话打回理由。
func scriptExitReason(code int, output string) string {
	tail := tailRunes(strings.TrimSpace(output), 800)
	switch {
	case code >= 0 && tail != "":
		return fmt.Sprintf("脚本退出码 %d：%s", code, tail)
	case code >= 0:
		return fmt.Sprintf("脚本退出码 %d（脚本没有输出任何内容）", code)
	case tail != "":
		return "脚本被系统终止：" + tail
	default:
		return "脚本被系统终止（没有输出任何内容）"
	}
}

// scriptTimeoutReason 是超时结论：带上最后几行输出，便于一眼看出卡在哪。
func scriptTimeoutReason(output string) string {
	if tail := tailRunes(strings.TrimSpace(output), 400); tail != "" {
		return "脚本执行超时被终止，最后的输出：" + tail
	}
	return "脚本执行超时被终止"
}

// scriptCancelReason 是取消结论：带上首个非空行，便于确认脚本跑到哪一步被打断。
func scriptCancelReason(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			return "脚本执行已取消：" + v
		}
	}
	return "脚本执行已取消"
}

// tailRunes 截取字符串尾部最多 limit 个字符（按 rune，避免切坏多字节字符）。
func tailRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return string(rs[len(rs)-limit:])
}
