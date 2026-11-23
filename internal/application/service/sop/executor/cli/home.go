// Package cli 是本地 CLI 执行器的公共基座：私有 HOME、凭据注入、prompt 投递。
//
// 三条硬口径（spec §5.2 通用机制）：
//   - 私有 HOME：每个节点每次执行用独立 HOME（<ws>/.sop/private-home/<kind>），
//     且经 os.Root 访问——符号链接指到外面一律拒绝。CLI 会往 HOME 里写缓存与凭据，
//     共享 HOME 等于把 A 节点的密钥暴露给 B 节点；
//   - 凭据 env-only：密钥只经环境变量注入，任何配置文件里只出现 `${SOP_CLI_API_KEY}`
//     这样的变量引用（由 CLI 自己展开），杜绝「配置里留明文」；
//   - prompt 投递阈值：命令行有长度上限（Windows 约 32KiB），超过 24KiB 的 prompt
//     改走 stdin 或落文件传路径，避免 exec 直接失败。
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
)

// privateHomeDir 是私有 HOME 在运行工作区里的相对位置。
const privateHomeDir = ".sop/private-home"

// Home 一次执行的私有 HOME：Root 限定所有写入都在 Dir 之内。
type Home struct {
	Root *os.Root
	Dir  string
}

// PrivateHome 准备并打开 `<ws>/.sop/private-home/<kind>`。
//
// kind 会被收敛为安全段（CLI 种类名来自用户配置，不能直接当路径用）。
// 调用方负责 Close（root 未关闭前目录不可删除，Windows 尤其严格）。
func PrivateHome(ws *engine.Workspace, kind string) (*Home, error) {
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return nil, fmt.Errorf("工作区未初始化：无法准备 CLI 私有目录")
	}
	dir := filepath.Join(ws.Root, filepath.FromSlash(privateHomeDir), safeSegment(kind))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建 CLI 私有目录失败：%w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("打开 CLI 私有目录失败：%w", err)
	}
	return &Home{Root: root, Dir: dir}, nil
}

// Close 关闭根句柄。
func (h *Home) Close() error {
	if h == nil || h.Root == nil {
		return nil
	}
	return h.Root.Close()
}

// WriteFile 在私有 HOME 内写文件：逐段校验，符号链接越界会被 os.Root 拒绝。
func (h *Home) WriteFile(rel string, content []byte) error {
	if h == nil || h.Root == nil {
		return fmt.Errorf("私有目录未打开")
	}
	clean := filepath.ToSlash(strings.TrimSpace(rel))
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
		return fmt.Errorf("私有目录内路径非法：%s", rel)
	}
	if dir := filepath.Dir(filepath.FromSlash(clean)); dir != "." {
		if err := h.Root.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建私有目录子路径失败：%w", err)
		}
	}
	f, err := h.Root.OpenFile(filepath.FromSlash(clean), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入私有文件失败：%w", err)
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("写入私有文件失败：%w", err)
	}
	return nil
}

// WriteAgentSystem 写入 agent-system.md（CLI 适配器据此拼系统提示）。
func (h *Home) WriteAgentSystem(content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return h.WriteFile("agent-system.md", []byte(content))
}

// ReadFile 读私有 HOME 内的文件（同样受 Root 约束）。
func (h *Home) ReadFile(rel string) ([]byte, error) {
	if h == nil || h.Root == nil {
		return nil, fmt.Errorf("私有目录未打开")
	}
	return h.Root.ReadFile(filepath.FromSlash(filepath.ToSlash(strings.TrimSpace(rel))))
}

// CredentialEnv 把凭据翻译成环境变量：密钥、地址、模型各占一个变量。
//
// 只产出变量，不产出文件：配置文件里的明文密钥会被日志、备份、`.sop/` 快照
// 一路带走，而环境变量随进程生命周期消失。
func CredentialEnv(c *types.SopCLICredential) map[string]string {
	if c == nil {
		return nil
	}
	env := map[string]string{}
	if key := strings.TrimSpace(c.APIKey); key != "" {
		env["SOP_CLI_API_KEY"] = key
	}
	if base := strings.TrimSpace(c.BaseURL); base != "" {
		env["SOP_CLI_BASE_URL"] = base
	}
	if model := strings.TrimSpace(c.DefaultModel); model != "" {
		env["SOP_CLI_MODEL"] = model
	}
	return env
}

// PromptArgMaxBytes 是可放进命令行的 prompt 上限：Windows 命令行约 32KiB，
// 这里按 24KiB 留出参数与转义的余量。
const PromptArgMaxBytes = 24576

// 投递方式（供调用方组装 argv/stdin）。
const (
	PromptDeliveryArgv  = "argv"
	PromptDeliveryStdin = "stdin"
	PromptDeliveryFile  = "file"
)

// PromptResult 一次 prompt 投递的结果。
type PromptResult struct {
	// Argv 是要放进命令行的文本：小 prompt（或落文件时的绝对路径）。
	Argv string
	// Stdin 是要写进子进程标准输入的文本（cli 支持 stdin 且 prompt 较大时）。
	Stdin string
	// Mode 是实际采用的投递方式（argv/stdin/file），便于日志与排障。
	Mode string
	// FilePath 是落文件时的绝对路径（Mode == file 时有值）。
	FilePath string
}

// DeliverPrompt 按大小与 CLI 能力选择投递方式：
//
//	≤24KiB             → argv 原文
//	>24KiB && preferStdin → stdin 原文
//	>24KiB && !preferStdin → 落 .sop/prompt-<node>-<attempt>.md，argv 传该绝对路径
func DeliverPrompt(
	prompt string, preferStdin bool, ws *engine.Workspace, nodeID string, attempt int,
) (PromptResult, error) {
	if len(prompt) <= PromptArgMaxBytes {
		return PromptResult{Argv: prompt, Mode: PromptDeliveryArgv}, nil
	}
	if preferStdin {
		return PromptResult{Stdin: prompt, Mode: PromptDeliveryStdin}, nil
	}
	if ws == nil || strings.TrimSpace(ws.Root) == "" {
		return PromptResult{}, fmt.Errorf("工作区未初始化：无法落 prompt 文件")
	}
	dir := filepath.Join(ws.Root, ".sop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return PromptResult{}, fmt.Errorf("创建 prompt 目录失败：%w", err)
	}
	if attempt < 1 {
		attempt = 1
	}
	name := fmt.Sprintf("prompt-%s-%d.md", safeSegment(nodeID), attempt)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(prompt), 0o600); err != nil {
		return PromptResult{}, fmt.Errorf("写入 prompt 文件失败：%w", err)
	}
	return PromptResult{Argv: full, Mode: PromptDeliveryFile, FilePath: full}, nil
}

// safeSegment 把用户可控的名字收敛为单段安全文件名。
func safeSegment(seg string) string {
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
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// ---- 配置渲染（T30–T32 各适配器会补齐自己的模板，这里先交付 zcode）----

// RenderZcodeConfig 渲染 zcode 的配置文件内容。
//
// 密钥只以 `${SOP_CLI_API_KEY}` 形式出现：zcode 读取配置时自行展开环境变量，
// 于是磁盘上永远没有明文。
func RenderZcodeConfig(c *types.SopCLICredential) string {
	model := strings.TrimSpace(valOr(c, func(c *types.SopCLICredential) string { return c.DefaultModel }))
	if model == "" {
		model = "${SOP_CLI_MODEL}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  \"apiKey\": \"${SOP_CLI_API_KEY}\",\n")
	fmt.Fprintf(&b, "  \"model\": %q\n", model)
	b.WriteString("}\n")
	return b.String()
}

// valOr 取凭据字段（nil 安全）。
func valOr(c *types.SopCLICredential, get func(*types.SopCLICredential) string) string {
	if c == nil {
		return ""
	}
	return get(c)
}
