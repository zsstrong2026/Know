// workspace.go 定义 SOP 运行的本地工作区：目录布局、路径越界守卫、上游产物
// 物化、Git 检查点与项目仓库克隆。
//
// 目录布局（每次运行一个独立目录，全部在 workspace_root 之下）：
//
//	<root>/runs/<run_id>/           运行根（ws.Root，产物相对路径的基准）
//	  inputs/<node_id>/             上游步骤产物物化目录
//	  out/                          本步骤产出目录
//	  work/repos/<name>/            项目绑定的 Git 仓库克隆
//	  logs/                         节点日志
//	  .sop/private-home/            CLI 执行器的隔离 HOME
//	  .sop/gate-results/<node>/attempt-<N>.json   CLI 通道的结构化终态
//
// 所有对外暴露的路径统一用正斜杠（filepath.ToSlash），一方面让落库/日志/界面
// 展示与平台一致，另一方面 Windows 与 POSIX 下 os/exec 都能直接使用。
//
// 安全口径：调用方给的相对路径一律先过解析校验——拒绝绝对路径、`..` 段与符号
// 链接逃逸；git 调用统一追加 GIT_CEILING_DIRECTORIES，避免向上发现工作区之外的
// 仓库。
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/types"
)

// Workspace 是单次运行的工作区句柄（字段均为绝对路径，正斜杠分隔）。
type Workspace struct {
	Root           string // 运行根，产物相对路径的基准
	InputsDir      string // 上游产物物化目录
	OutDir         string // 本步骤产出目录
	WorkDir        string // 克隆仓库等的作业目录
	LogsDir        string // 节点日志目录
	PrivateHomeDir string // CLI 执行器的隔离 HOME

	// gitRun 是执行 git 子命令的注入点（仅测试用来强制失败/替换实现）。
	// 由 PrepareWorkspace 设为 defaultGitRun；为 nil 时回落到默认实现。
	gitRun func(ctx context.Context, dir string, args ...string) error
}

// errPathEscape 越界守卫的统一错误（对用户白话，不含内部路径）。
var errPathEscape = errors.New("产物路径不能越出工作区")

// checkpointMu 串行化检查点。节点与运行都并行执行，`git add -A` 会争抢
// index.lock；检查点只有几十毫秒，用一把全局锁换掉重试与按根分锁的簿记开销。
var checkpointMu sync.Mutex

// lockCheckpoint 取得检查点锁，返回解锁函数。
func lockCheckpoint() func() {
	checkpointMu.Lock()
	return checkpointMu.Unlock
}

// safeSegmentRe 约束 run_id / 节点 id / 仓库名这类单段路径：只允许字母数字与
// `._-`。禁止空格与分隔符，克隆出的分支名 sop/<runID> 也因此天然合法。
var safeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// PrepareWorkspace 在 root 下创建 runs/<runID> 的标准目录布局。
func PrepareWorkspace(root, runID string) (*Workspace, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("工作区根目录不能为空")
	}
	if err := validateSegment(runID); err != nil {
		return nil, fmt.Errorf("运行 ID 非法：%w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析工作区根目录失败：%w", err)
	}
	base := slashJoin(abs, "runs", runID)
	ws := &Workspace{
		Root:           base,
		InputsDir:      slashJoin(base, "inputs"),
		OutDir:         slashJoin(base, "out"),
		WorkDir:        slashJoin(base, "work"),
		LogsDir:        slashJoin(base, "logs"),
		PrivateHomeDir: slashJoin(base, ".sop", "private-home"),
		gitRun:         defaultGitRun,
	}
	for _, d := range []string{ws.Root, ws.InputsDir, ws.OutDir, ws.WorkDir, ws.LogsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("创建工作区目录 %s 失败：%w", d, err)
		}
	}
	// 隔离 HOME 可能存放 CLI 执行器的凭证缓存，权限收紧到仅本人可读写。
	if err := os.MkdirAll(ws.PrivateHomeDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建工作区目录 %s 失败：%w", ws.PrivateHomeDir, err)
	}
	return ws, nil
}

// GateResultPath 返回 CLI 通道的结构化终态文件路径，并确保目录已存在：
// `<Root>/.sop/gate-results/<nodeID>/attempt-<attempt>.json`。
//
// nodeID 正常是 uuid；异常输入（含分隔符）会被就地改写为安全段，保证结果永远
// 落在运行根之内。目录创建失败不在此处打断执行（写文件时才是真正的失败点）。
func (ws *Workspace) GateResultPath(nodeID string, attempt int) string {
	if attempt < 1 {
		attempt = 1
	}
	p := slashJoin(ws.Root, ".sop", "gate-results", sanitizeSegment(nodeID),
		fmt.Sprintf("attempt-%d.json", attempt))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return p
}

// SafeWorkspacePath 把调用方给的相对路径解析成工作区内的绝对路径。
//
// 拒绝：绝对路径（含 Windows 盘符与 POSIX 前导 `/`）、任一段为 `..`、以及解析
// 符号链接后落到工作区之外的路径。目标文件尚不存在时，按最近的已存在祖先
// 解析，避免“先写后逃逸”。
func SafeWorkspacePath(ws *Workspace, rel string) (string, error) {
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return "", fmt.Errorf("工作区未初始化")
	}
	resolved, err := resolveUnder(ws.Root, rel)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(resolved), nil
}

// MaterializeInputs 把上游节点产物复制进 inputs/<node_id>/。
//
// arts 的 Path 是相对 srcRoot（通常是上游节点所在运行的 Root）的路径，同样受
// 越界守卫约束；目标文件名取源文件的 basename，同一节点目录内重名视为错误，
// 避免上游两个产物静默互相覆盖。
//
// 重试时上游产物集合可能变化：每个节点的输入先落到运行根下的临时目录
// （`.sop/tmp-inputs-<node>-<n>`），校验与复制全部成功后再整体替换
// `inputs/<node>/`。这样上一轮的遗留文件不会冒充本轮输入，也不会出现
// “复制到一半失败、旧输入已被清空”的半成品状态。
func MaterializeInputs(ws *Workspace, arts []types.SopArtifact, srcRoot string) error {
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return fmt.Errorf("工作区未初始化")
	}
	if len(arts) == 0 {
		return nil
	}
	if strings.TrimSpace(srcRoot) == "" {
		return fmt.Errorf("上游产物目录不能为空")
	}
	// 先完成全部校验与源文件探测，再落盘：校验失败时现有 inputs 保持原样。
	plans := make(map[string][]inputFile, len(arts))
	order := make([]string, 0, len(arts))
	seen := map[string]string{}
	for _, art := range arts {
		if err := validateSegment(art.NodeID); err != nil {
			return fmt.Errorf("上游节点标识非法：%w", err)
		}
		src, err := resolveUnder(srcRoot, art.Path)
		if err != nil {
			return fmt.Errorf("上游产物 %q 路径非法：%w", artifactLabel(art), err)
		}
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("上游产物 %q 不可读：%w", artifactLabel(art), err)
		}
		if info.IsDir() {
			return fmt.Errorf("上游产物 %q 是目录，暂不支持目录物化", artifactLabel(art))
		}
		name := filepath.Base(src)
		key := art.NodeID + "/" + name
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("上游产物重名：%q 与 %q 都会写入 inputs/%s/%s", prev, artifactLabel(art), art.NodeID, name)
		}
		seen[key] = artifactLabel(art)
		if _, ok := plans[art.NodeID]; !ok {
			order = append(order, art.NodeID)
		}
		plans[art.NodeID] = append(plans[art.NodeID], inputFile{src: src, name: name})
	}
	for _, node := range order {
		if err := replaceInputDir(ws, node, plans[node]); err != nil {
			return err
		}
	}
	return nil
}

// inputFile 是一次物化的单个目标：源绝对路径与目标文件名。
type inputFile struct {
	src  string
	name string
}

// inputTmpSeq 给临时目录名加序号，避免并行节点/重复调用撞名。
var inputTmpSeq uint64

// replaceInputDir 在临时目录内组装 node 的完整输入集，再整体替换 inputs/<node>/。
func replaceInputDir(ws *Workspace, node string, files []inputFile) error {
	seq := atomic.AddUint64(&inputTmpSeq, 1)
	tmp := filepath.Join(ws.Root, ".sop", fmt.Sprintf("tmp-inputs-%s-%d", sanitizeSegment(node), seq))
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("创建输入暂存目录失败：%w", err)
	}
	// 成功时目录已被 Rename 走，RemoveAll 为无害的空操作；失败时清掉半成品。
	defer os.RemoveAll(tmp)
	for _, f := range files {
		if err := copyFile(f.src, filepath.Join(tmp, f.name)); err != nil {
			return fmt.Errorf("物化上游产物到 inputs/%s/%s 失败：%w", node, f.name, err)
		}
	}
	dest := filepath.Join(ws.InputsDir, node)
	// Windows 下 rename 到已存在的目录会失败，故先移除旧目标（重试语义本就是整体替换）。
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("清理旧的输入目录 inputs/%s 失败：%w", node, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("替换输入目录 inputs/%s 失败：%w", node, err)
	}
	return nil
}

// MaterializeProjectBrief 把渲染好的项目简报写进 inputs/project-brief.md。
//
// 放在 inputs/ 而不是 out/：它是只读输入，节点引用路径 `inputs/project-brief.md`
// 时不需要再区分自己是第几个节点。空正文不物化（返回错误，调用方降级为空上下文）。
func (w *Workspace) MaterializeProjectBrief(markdown string) (string, error) {
	body := strings.TrimSpace(markdown)
	if body == "" {
		return "", fmt.Errorf("项目简报为空，无法物化")
	}
	full, err := SafeWorkspacePath(w, projectBriefPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("创建 inputs 目录失败：%w", err)
	}
	content := "<!-- 自动生成，请勿手改；要补充请在「项目动态」里记一条 -->\n\n" + body + "\n"
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写入项目简报失败：%w", err)
	}
	return full, nil
}

// GitCheckpoint 在运行根打一个检查点提交（git add -A + commit --allow-empty）。
//
// 复用判断：internal/application/service/workspace_checkpointer.go 的辅助函数
// 只面向沙箱内的 shell 脚本（依赖 git_ws 包装与 sandbox.WorkspaceLayout），无法
// 在本地进程里直接调用，故此处实现最小等价版本。
//
// 返回错误而不是静默吞掉：检查点失败不应让节点失败，不想关心细节的调用方用
// CheckpointBestEffort，需要自己决定降级方式的调用方用本函数。
func GitCheckpoint(ws *Workspace, msg string) error {
	return ws.gitCheckpoint(context.Background(), msg)
}

// CheckpointBestEffort 是检查点的“尽力而为”包装：失败只记日志并返回一句给用户
// 看的说明，成功返回空串。检查点属于可降级能力，任何失败都不该让节点失败。
func (ws *Workspace) CheckpointBestEffort(ctx context.Context, msg string) string {
	if err := ws.gitCheckpoint(ctx, msg); err != nil {
		note := "工作区检查点未创建（不影响节点执行）：" + err.Error()
		log.Printf("[sop-workspace] %s", note)
		return note
	}
	return ""
}

// gitCheckpoint 是 GitCheckpoint / CheckpointBestEffort 的共同实现。
func (ws *Workspace) gitCheckpoint(ctx context.Context, msg string) error {
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return fmt.Errorf("工作区未初始化")
	}
	// 并行执行节点时多个检查点会争抢同一个 index.lock，进程内按运行根串行化。
	unlock := lockCheckpoint()
	defer unlock()
	if err := os.MkdirAll(ws.Root, 0o755); err != nil {
		return fmt.Errorf("创建工作区失败：%w", err)
	}
	message := strings.TrimSpace(msg)
	if message == "" {
		message = "sop checkpoint"
	}
	if !isGitRepo(ws.Root) {
		if err := ws.runGit(ctx, ws.Root, "init", "-q"); err != nil {
			return fmt.Errorf("初始化工作区 Git 仓库失败：%w", err)
		}
	}
	if err := ensureCheckpointIgnore(ws.Root); err != nil {
		return err
	}
	if err := ws.runGit(ctx, ws.Root, "add", "-A"); err != nil {
		return fmt.Errorf("暂存工作区变更失败：%w", err)
	}
	// 身份与工作区仓库同机写入：走 -c 临时覆盖，不落任何项目级配置。
	if err := ws.runGit(ctx, ws.Root,
		"-c", "user.name=SOP", "-c", "user.email=sop@local",
		"commit", "-q", "--allow-empty", "-m", message); err != nil {
		return fmt.Errorf("创建工作区检查点失败：%w", err)
	}
	return nil
}

// checkpointIgnoreBody 让检查点只看运行产物本身：克隆进来的仓库是独立 Git
// 工作副本（`git add -A` 会把它们记成 gitlink，甚至因空仓库报错），隔离 HOME
// 里可能有凭证缓存，两者都不该进检查点。
const checkpointIgnoreBody = `# 由 SOP 运行工作区自动生成
.sop/private-home/
work/repos/
`

// ensureCheckpointIgnore 保证运行根有上述 .gitignore（已存在则不动）。
func ensureCheckpointIgnore(root string) error {
	path := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte(checkpointIgnoreBody), 0o644); err != nil {
		return fmt.Errorf("写入工作区 .gitignore 失败：%w", err)
	}
	return nil
}

// CloneRepos 把项目绑定的仓库逐个克隆到 work/repos/<name>/，并切出运行分支
// sop/<runID>。返回克隆目录清单（正斜杠）。
//
// 凭证一律走本机 git 凭证管理（credential helper），调用时不注入任何明文，也不
// 把凭证写进仓库或项目配置；GIT_TERMINAL_PROMPT=0 保证无凭证时立刻失败而不是
// 卡在交互提示上。任一仓库失败即返回错误（附带已克隆清单），由调用方把错误写进
// 节点 feedback。
func CloneRepos(ws *Workspace, runID string, repos []types.SopProjectRepo) ([]string, error) {
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return nil, fmt.Errorf("工作区未初始化")
	}
	if len(repos) == 0 {
		return nil, nil
	}
	if err := validateSegment(runID); err != nil {
		return nil, fmt.Errorf("运行 ID 非法：%w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("未找到 git 命令，无法克隆仓库：%w", err)
	}
	branch := "sop/" + runID
	reposDir := filepath.Join(ws.WorkDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建仓库目录失败：%w", err)
	}

	dirs := make([]string, 0, len(repos))
	for _, repo := range repos {
		url := strings.TrimSpace(repo.URL)
		if url == "" {
			return dirs, fmt.Errorf("仓库 %q 缺少地址", repo.Name)
		}
		name := strings.TrimSpace(repo.Name)
		if name == "" {
			name = deriveRepoName(strings.TrimRight(url, "/"))
		}
		if err := validateSegment(name); err != nil {
			return dirs, fmt.Errorf("仓库名称 %q 非法：%w", repo.Name, err)
		}
		base := strings.TrimSpace(repo.Branch)
		if base == "" {
			base = "main"
		}
		dir := filepath.Join(reposDir, name)
		if _, err := os.Stat(dir); err == nil {
			return dirs, fmt.Errorf("克隆仓库 %q 失败：目录 work/repos/%s 已存在", name, name)
		}
		if err := cloneRepo(url, base, dir); err != nil {
			return dirs, fmt.Errorf("克隆仓库 %q 失败：%w", name, err)
		}
		if err := ws.runGit(context.Background(), dir, "checkout", "-b", branch); err != nil {
			return dirs, fmt.Errorf("克隆仓库 %q 后创建运行分支 %s 失败：%w", name, branch, err)
		}
		dirs = append(dirs, filepath.ToSlash(dir))
	}
	return dirs, nil
}

// deriveRepoName 从仓库地址推导目录名：取最后一段，去掉 .git 后缀。
// SSH 形态 `git@host:path/repo.git` 与 `ssh://…` 都按 `/` 与 `:` 双分隔符处理
// （与 types.NormalizeSopRepos 的口径一致）。
func deriveRepoName(url string) string {
	base := url[strings.LastIndexAny(url, "/:")+1:]
	return strings.TrimSuffix(base, ".git")
}

// cloneRepo 执行一次单分支克隆；工作目录设为仓库目录的父目录，避免 -C 干扰
// clone 的目标解析。Ceiling 取该父目录，git 不会向上发现运行根之外的仓库。
//
// git 报错会把地址原样打进 stderr（`https://user:token@host/…`），错误文案会进
// 节点 feedback 与日志，故先脱敏再包装。
func cloneRepo(url, branch, dir string) error {
	cmd := exec.Command("git", "clone", "--branch", branch, "--single-branch", url, dir)
	cmd.Dir = filepath.Dir(dir)
	cmd.Env = gitEnv(filepath.Dir(dir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		stderr := scrubCloneError(strings.TrimSpace(string(out)), url)
		return fmt.Errorf("%s：%s", maskRepoURL(url), stderr)
	}
	return nil
}

// defaultGitRun 在 dir 内执行 git 子命令，环境统一带越界与免交互设置。
func defaultGitRun(ctx context.Context, dir string, args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("未找到 git 命令：%w", err)
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGit 走注入点（默认 defaultGitRun），让测试可以强制失败。
func (ws *Workspace) runGit(ctx context.Context, dir string, args ...string) error {
	if ws.gitRun != nil {
		return ws.gitRun(ctx, dir, args...)
	}
	return defaultGitRun(ctx, dir, args...)
}

// scrubbedGitEnvKeys 是从继承环境中剔除的 GIT_* 变量：它们会让 git 指向别的
// 仓库/索引（GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE），或覆盖我们的天花板设置
// （GIT_CEILING_DIRECTORIES 会重复出现，语义取决于实现，不如直接重设）。
var scrubbedGitEnvKeys = map[string]struct{}{
	"GIT_DIR":                 {},
	"GIT_WORK_TREE":           {},
	"GIT_INDEX_FILE":          {},
	"GIT_CEILING_DIRECTORIES": {},
}

// gitEnv 构造工作区口径的 git 环境：丢弃继承的 GIT_DIR/GIT_WORK_TREE/
// GIT_INDEX_FILE/GIT_CEILING_DIRECTORIES，再追加自己的天花板（取 target 的父
// 目录，使 git 只看 target 本身）与免交互开关（无凭证时立刻失败，不挂起）。
func gitEnv(target string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if _, drop := scrubbedGitEnvKeys[strings.ToUpper(name)]; drop {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(filepath.FromSlash(target)),
		"GIT_TERMINAL_PROMPT=0",
	)
}

// userinfoRe 匹配带方案地址里的 userinfo（`scheme://user[:token]@`）。
var userinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

// bareCredRe 匹配不带方案时裸露的 `user:pass@` / `user:token@` 片段。
var bareCredRe = regexp.MustCompile(`\b[A-Za-z0-9._~%+-]+:[^\s@/'"]+@`)

// queryTokenRe 匹配地址查询串里的凭证类参数值。
var queryTokenRe = regexp.MustCompile(`(?i)([?&](?:token|access_token|private_token|password|secret)=)[^&\s'"]+`)

// maskRepoURL 把仓库地址脱敏成可展示形态：userinfo 换成 `***`、查询串里的
// 凭证参数值换成 `***`。无凭证的地址原样返回。
func maskRepoURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return url
	}
	masked := userinfoRe.ReplaceAllString(url, "${1}***@")
	masked = queryTokenRe.ReplaceAllString(masked, "${1}***")
	return masked
}

// scrubCloneError 从 git stderr 里抹掉原始地址与裸露的 `user:pass@` 片段，
// 再交给上层拼错误。raw 是调用方传入的原始地址。
func scrubCloneError(stderr, raw string) string {
	out := stderr
	if raw = strings.TrimSpace(raw); raw != "" {
		// 地址可能被 git 加了引号或截断，先整体替换，再按变体兜底。
		out = strings.ReplaceAll(out, raw, maskRepoURL(raw))
		out = strings.ReplaceAll(out, strings.TrimRight(raw, "/"), maskRepoURL(strings.TrimRight(raw, "/")))
	}
	out = userinfoRe.ReplaceAllString(out, "${1}***@")
	out = bareCredRe.ReplaceAllString(out, "***@")
	out = queryTokenRe.ReplaceAllString(out, "${1}***")
	return out
}

func isGitRepo(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (st.IsDir() || st.Mode().IsRegular())
}

// artifactLabel 给错误信息用的产物称呼：优先名称，其次路径。
func artifactLabel(a types.SopArtifact) string {
	if strings.TrimSpace(a.Name) != "" {
		return a.Name
	}
	return a.Path
}

// validateSegment 校验单段路径（run_id / node_id / 仓库名）。
func validateSegment(seg string) error {
	if seg == "" {
		return fmt.Errorf("不能为空")
	}
	if seg == "." || seg == ".." || !safeSegmentRe.MatchString(seg) {
		return fmt.Errorf("只允许字母数字与 . _ -，长度 1–128")
	}
	return nil
}

// sanitizeSegment 把任意字符串压成合法单段（非法字符替换为 `_`），用于那些
// 只需“不越界”而不必报错的场景（如日志/结果文件命名）。
func sanitizeSegment(seg string) string {
	out := []rune(strings.TrimSpace(seg))
	for i, r := range out {
		ok := r < 128 && (r == '.' || r == '_' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
		if !ok {
			out[i] = '_'
		}
	}
	s := strings.Trim(string(out), ".")
	if s == "" {
		return "_"
	}
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}

// resolveUnder 把 rel 解析为 root 下的绝对路径并做越界校验（含符号链接）。
//
// 返回的是未做链接展开的拼接路径（调用方据此与 ws.Root 对账、落库展示），展开
// 结果只用于越界判定——工作区根本身是链接时（如 macOS 的 /tmp）也不会误报。
func resolveUnder(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析根目录失败：%w", err)
	}
	p := strings.TrimSpace(rel)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if isAbsLike(p) || hasParentSegment(p) {
		return "", errPathEscape
	}
	full := filepath.Join(rootAbs, filepath.FromSlash(p))
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("工作区不可用：%w", err)
	}
	resolved, err := evalExistingAncestor(full)
	if err != nil {
		return "", err
	}
	if !within(rootResolved, resolved) {
		return "", errPathEscape
	}
	// 链接通道单独再查一遍：Windows 的目录联接（junction）不被 EvalSymlinks 展开，
	// 仅靠上面的解析对账会漏。工作区里的路径段都是运行产物，出现链接没有正当用途。
	if err := rejectLinkedSegments(rootAbs, p); err != nil {
		return "", err
	}
	return full, nil
}

// rejectLinkedSegments 逐段检查 rel 中已存在的路径成分，链接一律拒绝。
//
// 用 Readlink 而不是只看 Lstat 的 ModeSymlink：Windows 目录联接（junction）不会
// 被标成符号链接，EvalSymlinks 也不展开它，但 Readlink 能读出目标——这是实践中
// 唯一能在三平台上统一识别 junction 的方式。
func rejectLinkedSegments(root, rel string) error {
	cur := root
	segs := strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/")
	for _, seg := range segs {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		if _, err := os.Lstat(cur); err != nil {
			if os.IsNotExist(err) {
				return nil // 后续段必然也不存在
			}
			return fmt.Errorf("检查路径失败：%w", err)
		}
		if _, err := os.Readlink(cur); err == nil {
			return errPathEscape
		}
	}
	return nil
}

// evalExistingAncestor 解析 full 最近的已存在祖先，再拼回不存在的尾段。
func evalExistingAncestor(full string) (string, error) {
	cur := filepath.Clean(full)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errPathEscape
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// within 判断 p 是否等于 base 或位于 base 之下。
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// isAbsLike 识别绝对路径：POSIX 前导 `/`、Windows 盘符、UNC，以及 Win32 备用数据流。
//
// `name:stream` 不是绝对路径，但它写的是同一文件的隐藏流：产物收集按文件名读不到，
// 目录列表里也看不见——效果等同于把产物丢进黑洞。盘符位（`C:`）之外的冒号一律拒绝。
func isAbsLike(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) >= 2 && p[1] == ':' { // C:foo（盘符相对）
		return true
	}
	return strings.Contains(p, ":")
}

// hasParentSegment 逐个路径段拒绝 `..`（两种分隔符都拆，保证跨平台一致）。
func hasParentSegment(p string) bool {
	for _, seg := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// slashJoin 拼接路径并统一为正斜杠。
func slashJoin(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}

// copyFile 复制单个文件（保留 0644，父目录须已存在）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
