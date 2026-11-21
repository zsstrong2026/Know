package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// requireGit 在 PATH 没有 git 时跳过依赖 git 的断言（开发机可能未装）。
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不在 PATH，跳过依赖 git 的断言")
	}
}

func TestPrepareWorkspaceLayout(t *testing.T) {
	ws, err := PrepareWorkspace(t.TempDir(), "run1")
	require.NoError(t, err)
	for _, d := range []string{ws.InputsDir, ws.OutDir, ws.WorkDir, ws.LogsDir, ws.PrivateHomeDir} {
		require.DirExists(t, d)
	}
	require.Contains(t, ws.Root, "runs/run1")
	require.Contains(t, ws.PrivateHomeDir, ".sop/private-home")
}

func TestSafeWorkspacePath(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run1")
	ok, err := SafeWorkspacePath(ws, "out/a.md")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ok, ws.Root))
	for _, bad := range []string{"../evil", "/etc/passwd", "out/../../evil"} {
		_, err := SafeWorkspacePath(ws, bad)
		require.Error(t, err, "path %q must be rejected", bad)
	}
}

func TestMaterializeInputs(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "out", "a.md"), []byte("# A"), 0o644))
	ws, _ := PrepareWorkspace(t.TempDir(), "run2")
	arts := []types.SopArtifact{{NodeID: "n1", Path: "out/a.md", Name: "报告"}}
	require.NoError(t, MaterializeInputs(ws, arts, src))
	got, err := os.ReadFile(filepath.Join(ws.InputsDir, "n1", "a.md"))
	require.NoError(t, err)
	require.Equal(t, "# A", string(got))
}

func TestGitCheckpoint(t *testing.T) {
	requireGit(t)
	ws, _ := PrepareWorkspace(t.TempDir(), "run3")
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "node n1 succeeded"))
	out, err := exec.Command("git", "-C", ws.Root, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "node n1 succeeded")
}

// 二次检查点必须追加提交，而不是重复初始化或报“nothing to commit”。
func TestGitCheckpointTwice(t *testing.T) {
	requireGit(t)
	ws, _ := PrepareWorkspace(t.TempDir(), "run4")
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "first"))
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v2"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "second"))
	out, err := exec.Command("git", "-C", ws.Root, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "first")
	require.Contains(t, string(out), "second")
}

// 工作区内的符号链接/目录联接不得成为逃逸通道。
func TestSafeWorkspacePathRejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	ws, _ := PrepareWorkspace(t.TempDir(), "run5")
	if err := createDirLink(filepath.Join(ws.Root, "escape"), outside); err != nil {
		t.Skipf("当前环境无法创建符号链接：%v", err)
	}
	_, err := SafeWorkspacePath(ws, "escape/evil.md")
	require.Error(t, err)
}

// 检查点不能向上发现工作区之外的仓库（GIT_CEILING_DIRECTORIES）。
func TestGitCheckpointDoesNotReachParentRepo(t *testing.T) {
	requireGit(t)
	parent := t.TempDir()
	mustGit(t, parent, "-c", "init.defaultBranch=main", "init", "-q")
	mustGit(t, parent, "-c", "user.name=SOP", "-c", "user.email=sop@local", "commit", "-q",
		"--allow-empty", "-m", "parent-init")
	ws, err := PrepareWorkspace(parent, "run14")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "inside-workspace"))
	require.True(t, isGitRepo(ws.Root), "检查点应在工作区内自建仓库")
	out, err := exec.Command("git", "-C", parent, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)
	require.NotContains(t, string(out), "inside-workspace")
}

// createDirLink 创建目录链接：Windows 上优先符号链接，无权限时退到目录联接。
func createDirLink(link, target string) error {
	if err := os.Symlink(target, link); err == nil {
		return nil
	}
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, target).Run(); err == nil {
		return nil
	}
	return os.Symlink(target, link)
}

// 源侧路径同样受守卫约束：上游产物清单里的相对路径不能越出 srcRoot。
func TestMaterializeInputsRejectsEscapingSource(t *testing.T) {
	src := t.TempDir()
	ws, _ := PrepareWorkspace(t.TempDir(), "run6")
	arts := []types.SopArtifact{{NodeID: "n1", Path: "../secret.md", Name: "x"}}
	require.Error(t, MaterializeInputs(ws, arts, src))
}

// 同名产物落到同一节点目录时不允许互相覆盖。
func TestMaterializeInputsRejectsDuplicateName(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "out", "a.md"), []byte("x"), 0o644))
	ws, _ := PrepareWorkspace(t.TempDir(), "run7")
	arts := []types.SopArtifact{
		{NodeID: "n1", Path: "out/a.md", Name: "一"},
		{NodeID: "n1", Path: "out/a.md", Name: "二"},
	}
	require.Error(t, MaterializeInputs(ws, arts, src))
}

func TestCloneRepos(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	mustGit(t, src, "-c", "init.defaultBranch=main", "init", "-q")
	mustGit(t, src, "-c", "user.name=SOP", "-c", "user.email=sop@local", "commit", "-q",
		"--allow-empty", "-m", "init")
	ws, _ := PrepareWorkspace(t.TempDir(), "run8")
	dirs, err := CloneRepos(ws, "run8", []types.SopProjectRepo{
		{Name: "app", URL: src, Branch: "main"},
	})
	require.NoError(t, err)
	require.Len(t, dirs, 1)
	require.DirExists(t, filepath.Join(ws.WorkDir, "repos", "app"))
	out, err := exec.Command("git", "-C", dirs[0], "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, "sop/run8", strings.TrimSpace(string(out)))
}

func TestCloneReposFailureIsChinese(t *testing.T) {
	requireGit(t)
	ws, _ := PrepareWorkspace(t.TempDir(), "run9")
	_, err := CloneRepos(ws, "run9", []types.SopProjectRepo{
		{Name: "app", URL: filepath.Join(t.TempDir(), "missing-repo"), Branch: "main"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "克隆")
}

func TestCloneReposRejectsUnsafeName(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run10")
	_, err := CloneRepos(ws, "run10", []types.SopProjectRepo{{Name: "../evil", URL: "https://example.com/a.git"}})
	require.Error(t, err)
}

func TestCloneReposRejectsUnsafeRunID(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run11")
	_, err := CloneRepos(ws, "bad run", []types.SopProjectRepo{{Name: "app", URL: "https://example.com/a.git"}})
	require.Error(t, err)
}

// run_id 直接进路径，必须拒绝 `..` 与分隔符，否则工作区根可被改写。
func TestPrepareWorkspaceRejectsUnsafeRunID(t *testing.T) {
	for _, bad := range []string{"", "..", "../x", "a/b", `a\b`} {
		_, err := PrepareWorkspace(t.TempDir(), bad)
		require.Error(t, err, "run id %q must be rejected", bad)
	}
	_, err := PrepareWorkspace("", "run1")
	require.Error(t, err)
}

func TestGateResultPath(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run12")
	got := ws.GateResultPath("n1", 2)
	require.Equal(t,
		filepath.ToSlash(filepath.Join(ws.Root, ".sop", "gate-results", "n1", "attempt-2.json")), got)
	require.DirExists(t, filepath.Dir(got))
	// 异常节点标识同样不得越出运行根（不得出现 `..` 路径段）。
	bad := ws.GateResultPath("../../evil", 0)
	require.True(t, strings.HasPrefix(bad, filepath.ToSlash(ws.Root)+"/"), bad)
	for _, seg := range strings.Split(bad, "/") {
		require.NotEqual(t, "..", seg, bad)
	}
	require.True(t, strings.HasSuffix(bad, "attempt-1.json"), bad)
}

// 克隆进来的仓库是独立 Git 工作副本，检查点不得把它们记成 gitlink。
func TestGitCheckpointSkipsClonedRepos(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	mustGit(t, src, "-c", "init.defaultBranch=main", "init", "-q")
	mustGit(t, src, "-c", "user.name=SOP", "-c", "user.email=sop@local", "commit", "-q",
		"--allow-empty", "-m", "init")
	ws, _ := PrepareWorkspace(t.TempDir(), "run13")
	_, err := CloneRepos(ws, "run13", []types.SopProjectRepo{{Name: "app", URL: src, Branch: "main"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "with repo"))
	out, err := exec.Command("git", "-C", ws.Root, "ls-tree", "--name-only", "HEAD").CombinedOutput()
	require.NoError(t, err)
	require.NotContains(t, string(out), "work")
	require.Contains(t, string(out), "out")
}

func TestDeriveRepoName(t *testing.T) {
	cases := map[string]string{
		"https://example.com/team/app.git": "app",
		"https://example.com/team/app":     "app",
		"git@example.com:team/app.git":     "app",
		"ssh://git@example.com/t/app":      "app",
	}
	for url, want := range cases {
		require.Equal(t, want, deriveRepoName(url), "url %q", url)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// scrubbedGit 用生产同一套环境过滤执行 git，供断言时避开测试注入的 GIT_* 变量。
func scrubbedGit(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(dir)
	return cmd
}

// 重试时上游产物集合可能变化：新一轮物化必须清掉上一轮遗留的 inputs，
// 否则旧产物会被下游误当成有效输入。
func TestMaterializeInputsClearsStaleOnRetry(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "out", "a.md"), []byte("# A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "out", "b.md"), []byte("# B"), 0o644))
	ws, _ := PrepareWorkspace(t.TempDir(), "run15")
	require.NoError(t, MaterializeInputs(ws,
		[]types.SopArtifact{{NodeID: "n1", Path: "out/a.md", Name: "A"}}, src))
	require.FileExists(t, filepath.Join(ws.InputsDir, "n1", "a.md"))

	require.NoError(t, MaterializeInputs(ws,
		[]types.SopArtifact{{NodeID: "n1", Path: "out/b.md", Name: "B"}}, src))
	require.NoFileExists(t, filepath.Join(ws.InputsDir, "n1", "a.md"), "上一轮产物必须被清掉")
	require.FileExists(t, filepath.Join(ws.InputsDir, "n1", "b.md"))
	// 临时目录不得残留（含失败前的暂存区）。
	entries, err := os.ReadDir(filepath.Join(ws.Root, ".sop"))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), "tmp-inputs", "物化临时目录必须清理")
	}
}

// 继承环境里的 GIT_DIR / GIT_WORK_TREE 会让 git 指向别处，检查点必须无视它们。
func TestGitCheckpointIgnoresInheritedGitDir(t *testing.T) {
	requireGit(t)
	bogus := filepath.Join(t.TempDir(), "bogus-gitdir")
	t.Setenv("GIT_DIR", bogus)
	t.Setenv("GIT_WORK_TREE", filepath.Join(t.TempDir(), "bogus-worktree"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "bogus-index"))
	ws, _ := PrepareWorkspace(t.TempDir(), "run16")
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "no inherited gitenv"))
	require.True(t, isGitRepo(ws.Root))
	require.NoDirExists(t, bogus, "不得在继承的 GIT_DIR 上创建任何东西")
	out, err := scrubbedGit(t, ws.Root, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "no inherited gitenv")
}

// 克隆失败会把 git stderr 拼进节点 feedback：URL 里的 userinfo 必须脱敏。
func TestMaskRepoURLCredentials(t *testing.T) {
	const raw = "https://bot:ghp_secret@git.example.com/team/app.git?token=ghp_secret"
	masked := maskRepoURL(raw)
	require.NotContains(t, masked, "ghp_secret")
	require.NotContains(t, masked, "bot:")
	require.Contains(t, masked, "https://***@git.example.com/team/app.git")

	// token 只作为 URL 查询参数、userinfo 为单段（token 即用户名）的形态。
	tokenOnly := maskRepoURL("https://ghp_secret@git.example.com/team/app.git")
	require.NotContains(t, tokenOnly, "ghp_secret")
	require.Contains(t, tokenOnly, "https://***@git.example.com/team/app.git")

	stderr := "fatal: unable to access '" + raw + "': 403\nsee also user:pass@host/repo"
	scrubbed := scrubCloneError(stderr, raw)
	require.NotContains(t, scrubbed, "ghp_secret")
	require.NotContains(t, scrubbed, "user:pass@")
	require.Contains(t, scrubbed, "https://***@git.example.com")

	// 无凭证的地址保持原样，避免把正常信息也抹掉。
	plain := "https://example.com/team/app.git"
	require.Equal(t, plain, maskRepoURL(plain))
}

// 检查点失败不得让节点失败：BestEffort 只返回一句人话说明。
func TestCheckpointBestEffort(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run17")
	ws.gitRun = func(_ context.Context, _ string, _ ...string) error {
		return errors.New("simulated git failure")
	}
	note := ws.CheckpointBestEffort(context.Background(), "should not fail node")
	require.NotEmpty(t, note)
	require.Contains(t, note, "检查点")
	require.NotContains(t, note, "panic")

	// 成功时返回空串。
	ws2, _ := PrepareWorkspace(t.TempDir(), "run18")
	ws2.gitRun = func(_ context.Context, _ string, _ ...string) error { return nil }
	require.Empty(t, ws2.CheckpointBestEffort(context.Background(), "ok"))
}
