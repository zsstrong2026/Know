// adapter.go 定义本地 CLI 执行器的适配契约：每种 CLI 只需描述「怎么起、怎么读、
// 怎么续」，进程治理、私有 HOME、凭据注入、成败判定由 RunStream 统一负责。
//
// 为什么要这一层：10 种 CLI 的启动参数与输出形态各不相同（stream-json / JSONL /
// 纯文本），但「跑一次、直播、判成败、能续跑」是同一件事。把差异收敛到 Adapter，
// 共同机制只实现一遍，出问题时也只有一个地方要查。
package cli

import (
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
)

// 系统提示注入方式。
const (
	// SystemPromptNative：CLI 自己会加载私有 HOME 里的 agent-system.md。
	SystemPromptNative = "native"
	// SystemPromptWrap：CLI 没有原生系统提示入口，由 RunStream 把内容包成
	// <system_instructions> 前缀塞进 prompt。
	SystemPromptWrap = "wrap"
)

// Adapter 一个本地 CLI 的适配描述（T30–T33 的 10 种适配器都实现它）。
type Adapter interface {
	// Kind 是注册表键里的 CLI 种类（如 "claude"）。
	Kind() string
	// BinaryName 是 PATH 上的可执行文件名。
	BinaryName() string
	// BuildArgs 组装命令行参数；prompt 已按 PromptDelivery 决议好（argv 文本或省略）。
	BuildArgs(tb executor.TaskBook, prompt string, resumeID string) []string
	// PreferStdin 表示该 CLI 支持从标准输入读 prompt（大 prompt 优先走 stdin）。
	PreferStdin() bool
	// SystemPromptMode 见上方常量。
	SystemPromptMode() string
	// ConfigFiles 返回要写进私有 HOME 的配置文件（相对路径 → 内容）。
	// 密钥只允许以 ${SOP_CLI_API_KEY} 等形式引用，绝不能出现明文。
	ConfigFiles(tb executor.TaskBook) map[string]string
	// Env 是额外环境变量（credential 的 env-only 映射在这里完成）。
	Env(tb executor.TaskBook) map[string]string
	// ParseLine 把一行输出解析成统一流事件；terminal 表示这是终态帧。
	ParseLine(line string) (events []executor.StreamEvent, terminal bool)
	// ResumeID 从事件里提取续跑 id（不支持续跑返回 ""）。
	ResumeID(events []executor.StreamEvent) string
	// FailureHint 把 stderr 尾部翻译成门禁提示：返回 "human" 表示要转人工
	// （例如把「轮次用尽」当成脚本失败重跑毫无意义）；返回 "" 表示无特别提示。
	FailureHint(stderr string) string
}

// RegistryKey 返回适配器在 executor.Registry 里的键。
func RegistryKey(a Adapter) string { return executor.ProviderKindCLIPrefix + a.Kind() }
