// definition.go 维护 SopDefinition 的派生字段：gate_commands 只能由编译函数产出。
//
// 调用时机：保存草稿、发布、以及 AI 编排的 set/add op 应用后——凡 definition
// 可能改动的入口都调用一次 RefreshDerived，保证落库内容与编译结果一致。
package engine

import "github.com/Tencent/WeKnora/internal/types"

// RefreshDerived 逐节点重算派生字段 GateCommands = CompileGateCommands(node)。
//
// 幂等；nil 定义安全返回。编译结果为空时写空切片（而非 nil），
// 让落库 JSON 恒为数组，前端不需额外判空。
func RefreshDerived(def *types.SopDefinition) {
	if def == nil {
		return
	}
	for i := range def.Nodes {
		cmds := CompileGateCommands(&def.Nodes[i])
		if cmds == nil {
			cmds = []string{}
		}
		def.Nodes[i].GateCommands = cmds
	}
}
