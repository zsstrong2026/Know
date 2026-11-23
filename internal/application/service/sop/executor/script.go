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

// scriptWaitDelay 是「脚本主进程已退出、但仍有子进程握着输出管道」时等待读取收尾的上限。
// 没有它，`nohup 长任务 &` 这种脚本会让读取端一直等 EOF，节点永远不结束。
const scriptWaitDelay = 10 * time.Second

// scriptRun 是一次在途执行：取消函数与「被 Cancel 显式终止」标记都按执行隔离，
// 不能放在 Provider 上——Provider 是进程内单例，两个节点并行跑脚本时共享一个
// 标记会让一次 Cancel 把另一条正常执行的结论改成「已取消」。
type scriptRun struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	killed atomic.Bool
}

// ScriptProvider 脚本节点执行器（Kind = script）。
type ScriptProvider struct {
	mu   sync.Mutex
	runs map[*scriptRun]struct{}
}

// NewScriptProvider 构造脚本执行器。
func NewScriptProvider() *ScriptProvider {
	return &ScriptProvider{runs: map[*scriptRun]struct{}{}}
}

// Kind 返回注册表键：script。
func (p *ScriptProvider) Kind() string { return ProviderKindScript }

// Prepare 脚本执行不需要准备环境（脚本自己负责建目录）。
func (p *ScriptProvider) Prepare(context.Context, *types.SopNode, string) error { return nil }

// Cancel 终止本执行器全部在途脚本所在的进程组（无在途执行时是 no-op）。
//
// 生产路径其实是取消 run 的 ctx（脚本随之被杀）；这里是兜底出口，没有节点标识，
// 因此作用于全部在途执行——引擎的取消本身就是按运行整体的，粒度一致。
func (p *ScriptProvider) Cancel(ctx context.Context) error {
	p.mu.Lock()
	runs := make([]*scriptRun, 0, len(p.runs))
	for run := range p.runs {
		runs = append(runs, run)
	}
	p.mu.Unlock()
	for _, run := range runs {
		run.killed.Store(true)
		run.cancel()
		killProcessGroup(run.cmd)
	}
	return nil
}

// register 登记一次在途执行；返回注销函数与「是否有在途执行」的探针。
func (p *ScriptProvider) register(run *scriptRun) func() {
	p.mu.Lock()
	p.runs[run] = struct{}{}
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.runs, run)
		p.mu.Unlock()
	}
}

// running 报告是否有在途执行（测试用来等脚本真正跑起来）。
func (p *ScriptProvider) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.runs) > 0
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
	// 自己管超时而不是 context.WithTimeout：收场要区分「超时」「被 Cancel 终止」
	// 「父 ctx 取消」「脚本自己非 0 退出」四种，把定时器触发的 cancel 当成人工取消
	// 是最糟的错判——用户会看到一条没人操作过的「已取消」。
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

	// 用 CommandContext：cmd.WaitDelay 只有在 Cmd 绑定了 ctx 时才生效（exec 的
	// watchCtx 会在 ctx 结束时强制关闭管道）。
	cmd := exec.CommandContext(runCtx, shellBinary(), shellArgs(script)...)
	cmd.Dir = root
	cmd.Env = scriptEnv(tb.Env)
	// 合并 stdout/stderr：模型与用户关心的是「发生了什么」，不是它来自哪个 fd；
	// 也让日志顺序与真实发生顺序一致。
	cmd.Stdout, cmd.Stderr = pw, pw
	attachProcessGroup(cmd)
	// 主进程退出后，被脚本遗留在后台的子进程若仍握着管道，Wait 最多再等这么久：
	// 否则读取端等不到 EOF，节点就永远不结束。
	cmd.WaitDelay = scriptWaitDelay

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return ExecResult{}, fmt.Errorf("启动脚本失败：%w", err)
	}
	// 父进程必须放手写端，否则读端永远等不到 EOF（子进程退出后仍然挂住）。
	_ = pw.Close()

	run := &scriptRun{cmd: cmd, cancel: cancel}
	unregister := p.register(run)
	defer unregister()

	logFile := openScriptLog(root, tb.NodeID, tb.Attempt)
	if logFile != nil {
		defer logFile.Close()
	}

	// 读取放在协程里：进程被杀后仍可能有孙进程握着写端（Windows 上 taskkill 也可能
	// 失败），此时阻塞在 EOF 上就等于把超时变成「等到脚本自己跑完」。这里以 ctx 结束
	// 为准，最多再给读取一小段收尾时间就掐掉读端。
	collected := startScriptReader(pr, opts.Emit, logFile)
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

	if !collected.wait(scriptDrainGrace) {
		collected.close()
		collected.wait(scriptDrainGrace)
	}

	waitErr := cmd.Wait()
	stopWatch()
	collected.close()
	output := collected.output()

	res := ExecResult{Output: tailRunes(output, scriptOutputTail)}
	switch {
	case timedOut.Load():
		res.GateResult = types.SopGateRework
		res.GateReason = scriptTimeoutReason(output)
	case run.killed.Load():
		res.GateResult = types.SopGateRework
		res.GateReason = scriptCancelReason(output)
	case ctx.Err() != nil:
		// 父 ctx（运行的推进 ctx）被取消：这不是节点结论，原样向上返回，由引擎
		// 按「取消不是失败」收口（与 agent.go / node.go 的处理一致）。
		return ExecResult{}, ctx.Err()
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

// scriptDrainGrace 是进程结束后等读取协程收尾的上限：孙进程握着管道时不能无限等。
const scriptDrainGrace = 2 * time.Second

// scriptReader 是一次输出收集：读取在协程里跑，主流程按 ctx 决定何时放弃等待。
//
// closed 之后不再 Emit、不再写日志：读取协程可能比 Execute 晚收尾，迟到的推送
// 会写到已经关闭的日志文件上，还会混进超时摘要里变成一条假错误。
type scriptReader struct {
	pr     *os.File
	done   chan struct{}
	mu     sync.Mutex
	buf    strings.Builder
	closed bool
}

// startScriptReader 起一个逐行读取协程：每行 Emit 一次 text_delta 并写日志。
func startScriptReader(pr *os.File, emit func(StreamEvent), logFile io.Writer) *scriptReader {
	r := &scriptReader{pr: pr, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), scriptScanBuf)
		for scanner.Scan() {
			r.push(scanner.Text(), emit, logFile)
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			// 单行超过上限会让扫描提前结束。此时必须把剩余字节排空：读端不再读，
			// 子进程写一个满的管道就会永远阻塞，Wait 随之挂死。
			_, _ = io.Copy(io.Discard, pr)
			r.push("[读取脚本输出失败] "+err.Error(), emit, logFile)
		}
	}()
	return r
}

// wait 等读取协程收尾，最多等 d；返回是否已结束。
func (r *scriptReader) wait(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-r.done:
		return true
	case <-timer.C:
		return false
	}
}

// push 追加一行：缓冲、日志、SSE 三处同一份文本（reader 关闭后整条跳过）。
func (r *scriptReader) push(text string, emit func(StreamEvent), logFile io.Writer) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.buf.WriteString(text)
	r.buf.WriteString("\n")
	r.mu.Unlock()
	if logFile != nil {
		_, _ = io.WriteString(logFile, text+"\n")
	}
	if emit != nil {
		emit(StreamEvent{Type: StreamEventTextDelta, Text: text + "\n", At: time.Now()})
	}
}

// close 掐掉读端：读取协程阻塞在 Read 上时随之中断，已收集的内容不丢。
//
// 先置 closed 再关文件：晚到的读取行会在 push 里被丢弃，不会写进已关闭的日志。
func (r *scriptReader) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	_ = r.pr.Close()
}

// output 返回已收集的全文（去掉末尾换行）。
func (r *scriptReader) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimRight(r.buf.String(), "\n")
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
