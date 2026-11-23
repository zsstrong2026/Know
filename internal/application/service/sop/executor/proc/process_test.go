package proc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestKillTreeTerminatesChildren：bash 拉起一个孙进程（sleep），杀树必须连它一起走。
func TestKillTreeTerminatesChildren(t *testing.T) {
	p, err := Start(context.Background(), "bash", []string{"-c", "sleep 60 & wait"},
		os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, p.KillTree())
	done := make(chan struct{})
	go func() { _, _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("进程树没有被杀掉")
	}
}

// DrainThenKill：宽限期到点后仍在跑的进程被杀，调用在宽限附近返回。
func TestDrainThenKillWithinGrace(t *testing.T) {
	p, err := Start(context.Background(), "bash",
		[]string{"-c", "echo done; sleep 60"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)

	start := time.Now()
	DrainThenKill(p, 1500*time.Millisecond)
	_, _ = p.Wait()
	require.Less(t, time.Since(start), 20*time.Second, "排水后必须结束")
}

// ctx 超时：WatchContext 触发杀树，Wait 返回错误且不再挂住。
func TestStartTimeoutKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p, err := Start(ctx, "bash", []string{"-c", "sleep 30"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { _, err := p.Wait(); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err, "被杀掉的进程 Wait 必须报错")
	case <-time.After(15 * time.Second):
		t.Fatal("超时后进程没有被杀")
	}
}

// stdin 投递：大 prompt 经管道写入，子进程能完整读到。
func TestStartWritesStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	payload := strings.Repeat("字", 20000) // 约 60KB，大于任何管道缓冲的一半
	// 用 byte count 校验完整送达（含多字节内容时按字符比较会误导）。
	p, err := Start(context.Background(), "bash",
		[]string{"-c", "wc -c > " + quoteShell(out)}, os.Environ(), dir,
		strings.NewReader(payload))
	require.NoError(t, err)
	code, err := p.Wait()
	require.NoError(t, err)
	require.Equal(t, 0, code)

	body, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	require.Equal(t, len(payload), atoiTrim(string(body)), "stdin 字节数必须完整送达")
}

// quoteShell 把路径包成单引号（bash 语义）。
func quoteShell(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// 正常退出：退出码透传，KillTree 对已退出进程是 no-op。
func TestStartExitCodeAndIdempotentKill(t *testing.T) {
	p, err := Start(context.Background(), "bash", []string{"-c", "exit 3"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)
	code, err := p.Wait()
	require.Error(t, err, "非 0 退出必须报错（调用方据此判门禁）")
	require.Equal(t, 3, code)
	require.Equal(t, 3, p.ExitCode())

	// 重复杀灭不 panic 也不阻塞
	_ = p.KillTree()
	_ = p.KillTree()
}

// ScriptEnv 同名变量覆盖、含 NUL 的跳过。
func TestScriptEnvOverride(t *testing.T) {
	t.Setenv("SOP_TEST_VAR", "old")
	env := ScriptEnv(map[string]string{"SOP_TEST_VAR": "new", "SOP_BAD": "a\x00b"})
	var found int
	for _, kv := range env {
		if strings.HasPrefix(kv, "SOP_TEST_VAR=") {
			found++
			require.Equal(t, "SOP_TEST_VAR=new", kv)
		}
		if strings.HasPrefix(kv, "SOP_BAD=") {
			t.Fatal("含 NUL 的变量必须被跳过")
		}
	}
	require.Equal(t, 1, found, "同名变量只保留覆盖后的那一份")
}

// atoiTrim 解析去空白的十进制整数（测试辅助）。
func atoiTrim(s string) int {
	s = strings.TrimSpace(s)
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
