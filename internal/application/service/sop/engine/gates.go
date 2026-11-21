// gates.go 把节点声明的产物校验（artifact_outputs[].check）与额外校验
// （gate_extra）确定性地编译成门禁命令，并对执行器上报结论做纯函数裁定。
//
// gate_commands 是派生字段：只能由本文件的编译函数产出，AI/用户永不直写。
package engine

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// GateReasonClipRunes 裁定原因保留的最大字符数（再长截断加省略号）。
const GateReasonClipRunes = 500

// CompileGateCommands 把节点配置编译成门禁命令清单（顺序即执行顺序）：
//   - exists（含空 type）→ test -s "<path>"（文件存在且非空）
//   - contains → grep -q -- "<keyword>" "<path>"
//   - command → 原样保留
//
// 之后按 && 拆分 gate_extra 追加，空白片段丢弃。函数不写回节点的派生字段，
// 由 RefreshDerived 统一落位。
func CompileGateCommands(n *types.SopNode) []string {
	if n == nil {
		return nil
	}
	var cmds []string
	for _, a := range n.ArtifactOutputs {
		path := strings.TrimSpace(a.Path)
		if path == "" {
			continue
		}
		switch a.Check.Type {
		case types.SopArtifactCheckContains:
			// `--` 终止选项解析：关键词以 - 开头（如 markdown 待办项「- [ ]」）时
			// 不会被 grep 当成选项；关键词与路径都做 shell 引用。
			cmds = append(cmds, fmt.Sprintf("grep -q -- %s %s", shellDoubleQuote(a.Check.Keyword), shellDoubleQuote(path)))
		case types.SopArtifactCheckCommand:
			if cmd := strings.TrimSpace(a.Check.Command); cmd != "" {
				cmds = append(cmds, cmd)
			}
		default: // exists
			cmds = append(cmds, "test -s "+shellDoubleQuote(path))
		}
	}
	for _, part := range strings.Split(n.GateExtra, "&&") {
		if p := strings.TrimSpace(part); p != "" {
			cmds = append(cmds, p)
		}
	}
	return cmds
}

// JoinGateScript 把命令清单合并为一段 bash 脚本：首行 set -e，任一命令失败即中止。
// 命令为空返回空串（调用方据此跳过执行）。
func JoinGateScript(cmds []string) string {
	if len(cmds) == 0 {
		return ""
	}
	return "set -e\n" + strings.Join(cmds, "\n")
}

// shellDoubleQuote 把关键词包成 bash 双引号字面量，转义会被解释的字符
// （\ " $ `），保证 grep 模式原样传入。换行等控制字符降级为空格。
func shellDoubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		case '`':
			b.WriteString("\\`")
		case '\r', '\n', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// GateEvidence 是门禁裁定所需的全部事实，由调用方（引擎节点推进）采集后传入。
//
// 拆成结构体是为了让裁定保持纯函数：文件是否存在、脚本退出码这些 IO 事实
// 在调用方收集，决策逻辑在此集中且可表驱动测试。
type GateEvidence struct {
	// Reported 执行器上报的结论（pass|rework|fail|human）；空或其他值=缺失，
	// 落到产物契约与脚本两段裁定。
	Reported string
	// Reason 执行器给出的原因（rework/fail/human 时使用）。
	Reason string
	// MissingArtifacts 必需但缺失或为空的产物路径（白话展示给用户）。
	MissingArtifacts []string
	// ScriptRan 是否实际跑了门禁脚本（无命令时为 false）。
	ScriptRan bool
	// ScriptOK 脚本退出码是否为 0。
	ScriptOK bool
	// ScriptOutput 脚本合并输出（失败时进原因，超长截断）。
	ScriptOutput string
}

// DecideGate 按三段顺序裁定：① 执行器上报一致采纳；② 必需产物缺失→打回；
// ③ 门禁脚本非 0 退出→打回；都没有问题即通过。
//
// 返回的原因一律是给用户看的大白话，不含「门禁/gate/任务书」等工程术语。
func DecideGate(ev GateEvidence) (verdict string, reason string) {
	switch ev.Reported {
	case types.SopGatePass, types.SopGateRework, types.SopGateFail, types.SopGateHuman:
		return ev.Reported, ev.Reason
	}

	if len(ev.MissingArtifacts) > 0 {
		return types.SopGateRework, "缺少必需产物：" + strings.Join(ev.MissingArtifacts, "、")
	}
	if ev.ScriptRan && !ev.ScriptOK {
		return types.SopGateRework, "检查没通过：" + clipRunes(ev.ScriptOutput, GateReasonClipRunes)
	}
	return types.SopGatePass, ""
}

// clipRunes 按字符截断并加省略号；未超长原样返回。
func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
