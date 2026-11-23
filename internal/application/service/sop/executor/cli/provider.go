package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// CLIProvider 把 Adapter 包装成引擎的 executor.Provider（Kind = "cli:<kind>"）。
type CLIProvider struct {
	A Adapter
}

// Kind 返回注册表键。
func (p *CLIProvider) Kind() string {
	if p == nil || p.A == nil {
		return ""
	}
	return RegistryKey(p.A)
}

// Prepare 校验可执行文件在 PATH 上：缺失时给白话指引，而不是等运行时 exec 报错。
func (p *CLIProvider) Prepare(_ context.Context, _ *types.SopNode, _ string) error {
	if p == nil || p.A == nil {
		return errors.New("CLI 执行器未就绪")
	}
	if _, err := exec.LookPath(p.A.BinaryName()); err != nil {
		return errors.New("未检测到 " + p.A.BinaryName() +
			" 命令，请先安装并加入 PATH（设置页有指引）")
	}
	return nil
}

// Execute 跑一次 CLI 节点：统一走 RunStream。
func (p *CLIProvider) Execute(
	ctx context.Context, tb executor.TaskBook, opts executor.ExecOptions,
) (executor.ExecResult, error) {
	return RunStream(ctx, p.A, tb, opts.Emit)
}

// Cancel 中断在途执行：进程治理层的 ctx 取消已覆盖；这里是兜底（无状态 provider）。
func (p *CLIProvider) Cancel(_ context.Context) error { return nil }

// 编译期断言：适配器包装后满足引擎契约。
var _ executor.Provider = (*CLIProvider)(nil)

// marshalPayload 把任意值编码为事件 payload（失败返回 nil，事件照发）。
func marshalPayload(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// containsAnyFold 判断文本是否包含任一关键词（忽略大小写）。
func containsAnyFold(text string, keywords ...string) bool {
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
