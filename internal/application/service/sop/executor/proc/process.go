// process.go 是 CLI/脚本共用的进程治理基座：启动、宽限排水、杀整棵树。
//
// 为什么需要 Job Object（Windows）/ 进程组（POSIX）：CLI 会拉起孙进程
// （node、python、构建工具），只杀直接子进程会留下吃满 CPU 的孤儿。两平台都用
// 「整组/整作业」的语义杀灭，并在杀灭失败时退化为系统级兜底（taskkill /T）。
package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// DrainGrace 是「收到终态帧后」给子进程自行退出的宽限时间（spec §5.2：2.5s）。
const DrainGrace = 2500 * time.Millisecond

// Proc 一次受监管的子进程。
type Proc struct {
	Cmd *exec.Cmd
	// pid 是直接子进程 pid（unix 下同时是进程组 id）。
	pid int
	// job 是 Windows Job Object 句柄（unix 恒为 0）。
	job uintptr
	// stdinPipe 是调用方写入 stdin 的管道（stdin 为 nil 时不建）。
	stdinPipe io.WriteCloser
	// stdinDone 在 stdin 写完并关闭管道后关闭：子进程读到 EOF 才会退出，
	// 所以 Wait 必须先等它——否则我们会提前关管道，子进程只收到半截输入。
	stdinDone chan struct{}
	// killed 记录是否已经被显式杀过（避免重复 taskkill）。
	killed sync.Once
	// killedErr 保存首次杀灭的错误（便于日志）。
	killedErr error
}

// Start 启动子进程并挂到进程组/作业对象上；ctx 取消会立即杀整棵树。
func Start(
	ctx context.Context, name string, args []string, env []string, dir string, stdin io.Reader,
) (*Proc, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	if stdin != nil {
		pipe, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("创建标准输入管道失败：%w", err)
		}
		// 注意：StdinPipe 已把读端挂到 cmd.Stdin 上，绝不能再把它置空，
		// 否则子进程读到的是空 EOF（表现为「stdin 一个字节都没送到」）。
		if err := cmd.Start(); err != nil {
			_ = pipe.Close()
			return nil, fmt.Errorf("启动进程失败：%w", err)
		}
		p := &Proc{Cmd: cmd, pid: cmd.Process.Pid, stdinPipe: pipe, stdinDone: make(chan struct{})}
		// 写 stdin 放到协程：大 prompt（数十 KB）会把管道写满，
		// 同步写会卡住调用方直到子进程读走。
		go func() {
			defer close(p.stdinDone)
			_, _ = io.Copy(pipe, stdin)
			_ = pipe.Close()
		}()
		p.attach()
		p.watchContext(ctx)
		return p, nil
	}
	attachGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动进程失败：%w", err)
	}
	p := &Proc{Cmd: cmd, pid: cmd.Process.Pid}
	p.attach()
	p.watchContext(ctx)
	return p, nil
}

// attach 在进程启动后把 Windows 作业对象挂上（unix 无需后置动作）。
func (p *Proc) attach() { p.job = attachJob(p.Cmd) }

// watchContext 在 ctx 结束时杀整棵树。
func (p *Proc) watchContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	go func() {
		<-ctx.Done()
		p.KillTree()
	}()
}

// Wait 等待进程退出并返回退出码（不吞错误：调用方要区分「正常退出」与「被杀」）。
func (p *Proc) Wait() (int, error) {
	if p == nil || p.Cmd == nil {
		return -1, errors.New("进程未启动")
	}
	// 先让 stdin 写完（子进程读到 EOF 才会退出）；被杀的子进程读不到 EOF，
	// 因此在被杀场景下这里只是尽快失败，不阻塞调用方。
	if p.stdinDone != nil {
		select {
		case <-p.stdinDone:
		case <-time.After(30 * time.Second):
		}
	}
	err := p.Cmd.Wait()
	p.release()
	code := 0
	if p.Cmd.ProcessState != nil {
		code = p.Cmd.ProcessState.ExitCode()
	}
	return code, err
}

// KillTree 杀整棵进程树（幂等：重复调用只执行一次实际杀灭）。
func (p *Proc) KillTree() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	p.killed.Do(func() {
		p.killedErr = killTree(p.Cmd, p.pid, p.job)
	})
	return p.killedErr
}

// ExitCode 返回退出码（进程未结束时 -1）。
func (p *Proc) ExitCode() int {
	if p == nil || p.Cmd == nil || p.Cmd.ProcessState == nil {
		return -1
	}
	return p.Cmd.ProcessState.ExitCode()
}

// release 释放作业对象句柄（进程已退出，句柄不释放会泄漏）。
func (p *Proc) release() {
	releaseJob(p.job)
	p.job = 0
	if p.stdinPipe != nil {
		_ = p.stdinPipe.Close()
		p.stdinPipe = nil
	}
}

// DrainThenKill 给子进程 grace 的自行退出宽限，到点仍活着就杀整棵树。
//
// 调用方在收到「终态帧」（例如 agent 输出的最后一条 result）后调用：CLI 有时会
// 在输出完成后继续挂住（等网络、等清理），不杀就会一直占着推进协程。
func DrainThenKill(p *Proc, grace time.Duration) {
	if p == nil {
		return
	}
	if grace <= 0 {
		grace = DrainGrace
	}
	// 不能 defer Stop：函数一返回计时器就被取消，等于永不排水杀灭。
	// 计时器只触发一次；进程已自行退出时 KillTree 是 no-op。
	time.AfterFunc(grace, func() { _ = p.KillTree() })
}

// KillProcessTree 杀一棵由调用方自行启动的进程树（脚本执行器复用）。
//
// 与 Proc.KillTree 的区别：这里没有作业对象句柄（进程不是经 Start 起的），
// 只能按 pid 走系统级杀灭（Windows taskkill /T、POSIX kill(-pgid)）。
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return killTree(cmd, cmd.Process.Pid, 0)
}

// ScriptEnv 是脚本/CLI 共用的环境拼装：系统环境打底，额外键值覆盖同名项。
//
// 覆盖而非追加：同名变量出现两次时各平台取值不一致，显式去重才保证
// 「调用方写的值就是生效的值」。
func ScriptEnv(extra map[string]string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		key, _, _ := cutEq(kv)
		if _, overridden := extra[key]; overridden {
			continue
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := extra[k]
		if k == "" || containsNUL(k) || containsNUL(v) {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// cutEq 在第一个 '=' 处切分环境变量条目。
func cutEq(kv string) (key, value string, found bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

// containsNUL 判断是否含 NUL（exec 会直接拒绝这类环境变量）。
func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}
