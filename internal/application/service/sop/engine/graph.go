// Package engine 是 SOP 执行引擎：图算法、派生字段编译与（后续任务）运行推进。
//
// 依赖方向固定为 engine → executor → types：本包不感知 repository/handler，
// 本文件中的函数全部是纯函数——不读库、不碰文件系统、不接收 context，
// 因此可以直接用表驱动单测覆盖。
package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Upstreams 返回直接上游节点 id（有边 source→nodeID）；按 id 升序保证确定性。
func Upstreams(def *types.SopDefinition, nodeID string) []string {
	if def == nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, e := range def.Edges {
		if e.Target == nodeID && e.Source != "" && !seen[e.Source] {
			seen[e.Source] = true
			ids = append(ids, e.Source)
		}
	}
	sort.Strings(ids)
	return ids
}

// Downstreams 返回直接下游节点 id（有边 nodeID→target）；按 id 升序保证确定性。
func Downstreams(def *types.SopDefinition, nodeID string) []string {
	if def == nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, e := range def.Edges {
		if e.Source == nodeID && e.Target != "" && !seen[e.Target] {
			seen[e.Target] = true
			ids = append(ids, e.Target)
		}
	}
	sort.Strings(ids)
	return ids
}

// Ancestors 返回全部上游节点（不含自身）的集合；BFS 顺边反向遍历。
func Ancestors(def *types.SopDefinition, nodeID string) map[string]bool {
	return reach(def, nodeID, Upstreams)
}

// Descendants 返回全部下游节点（不含自身）的集合；BFS 顺边正向遍历。
func Descendants(def *types.SopDefinition, nodeID string) map[string]bool {
	return reach(def, nodeID, Downstreams)
}

// NodesOnPaths 返回从 from 到 to 的所有路径上的节点（含两端）：即 from 的下游里
// 能走到 to 的那些。打回重置只该动这条链——无条件重置 from 的全部下游会把并行
// 兄弟分支的产出一起抹掉，下游节点重跑时就读不到本该存在的数据。
//
// from == to 时只返回该节点自身。
func NodesOnPaths(def *types.SopDefinition, from, to string) []string {
	if def == nil || from == "" || to == "" {
		return nil
	}
	if from == to {
		return []string{from}
	}
	// 反向可达集：从 to 往上游走，能到 from 的节点都在某条 from→to 的路径上。
	upFromTo := Ancestors(def, to)
	if !upFromTo[from] {
		return []string{from}
	}
	out := []string{from}
	for id := range Descendants(def, from) {
		if id == to || upFromTo[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func reach(def *types.SopDefinition, nodeID string, next func(*types.SopDefinition, string) []string) map[string]bool {
	out := map[string]bool{}
	if def == nil || nodeID == "" {
		return out
	}
	queue := next(def, nodeID)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == nodeID || out[id] {
			continue
		}
		out[id] = true
		queue = append(queue, next(def, id)...)
	}
	return out
}

// HasCycle 用 Kahn 入度法判断流程里是否有循环依赖。
//
// 边引用不存在的节点不计入入度（悬空连线由 ValidateDefinition 单独报错），
// 避免一个笔误的连线把整个图判成有环。重复的平行边只算一次，
// 与 Downstreams 的去重口径一致。
func HasCycle(def *types.SopDefinition) bool {
	if def == nil {
		return false
	}
	indeg := map[string]int{}
	for _, n := range def.Nodes {
		indeg[n.ID] = 0
	}
	for _, e := range uniqueEdges(def) {
		indeg[e.target]++
	}
	var queue []string
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range Downstreams(def, id) {
			if _, ok := indeg[next]; !ok {
				continue
			}
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return visited != len(indeg)
}

// edgePair 是两端都指向已存在节点的边（去重后）。
type edgePair struct{ source, target string }

// uniqueEdges 过滤悬空连线并按 source+target 去重，保持定义顺序。
func uniqueEdges(def *types.SopDefinition) []edgePair {
	known := make(map[string]bool, len(def.Nodes))
	for _, n := range def.Nodes {
		known[n.ID] = true
	}
	seen := map[edgePair]bool{}
	var out []edgePair
	for _, e := range def.Edges {
		if !known[e.Source] || !known[e.Target] {
			continue
		}
		p := edgePair{e.Source, e.Target}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// TopoLayers 按层分组返回节点 id（自动排版用）：第 0 层是无上游的步骤，
// 每层内部按节点在定义里的先后顺序排序。存在环时环内节点归入最后一层，
// 保证返回结果覆盖所有节点（排版不丢卡片）。
func TopoLayers(def *types.SopDefinition) [][]string {
	if def == nil || len(def.Nodes) == 0 {
		return nil
	}
	order := make([]string, 0, len(def.Nodes))
	indeg := make(map[string]int, len(def.Nodes))
	for _, n := range def.Nodes {
		if _, dup := indeg[n.ID]; dup {
			continue
		}
		order = append(order, n.ID)
		indeg[n.ID] = 0
	}
	for _, e := range uniqueEdges(def) {
		indeg[e.target]++
	}

	remaining := map[string]bool{}
	for _, id := range order {
		remaining[id] = true
	}
	layers := make([][]string, 0, len(order))
	for len(remaining) > 0 {
		var layer []string
		for _, id := range order {
			if remaining[id] && indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			// 只剩环内节点：整层收尾，避免死循环。
			var leftover []string
			for _, id := range order {
				if remaining[id] {
					leftover = append(leftover, id)
				}
			}
			layers = append(layers, leftover)
			break
		}
		for _, id := range layer {
			delete(remaining, id)
			for _, next := range Downstreams(def, id) {
				if _, ok := indeg[next]; ok {
					indeg[next]--
				}
			}
		}
		layers = append(layers, layer)
	}
	return layers
}

// TopoSort 返回扁平的拓扑顺序（层内保持定义顺序）；有环时环内节点排在末尾。
// 与 TopoLayers 同源，便于调用方只需要一个有序 id 列表时使用。
func TopoSort(def *types.SopDefinition) []string {
	layers := TopoLayers(def)
	if len(layers) == 0 {
		return nil
	}
	out := make([]string, 0, len(def.Nodes))
	for _, l := range layers {
		out = append(out, l...)
	}
	return out
}

// ValidateDefinition 用大白话列出流程定义的问题；返回空切片=可以发布。
//
// allowIncompleteExecutor=true 用于草稿态（AI 生成的初稿还没选执行角色），
// 此时跳过执行器完整性检查与孤立步骤检查（画布上还没连线的步骤允许保存）；
// 其余检查对草稿同样生效。
func ValidateDefinition(def *types.SopDefinition, allowIncompleteExecutor bool) []string {
	return ValidateDefinitionWithSelf(def, "", allowIncompleteExecutor)
}

// ValidateDefinitionWithSelf 在 ValidateDefinition 基础上额外检查子流程自引用：
// sop 节点的 child_template_id 不得等于正在编辑的模板 id（否则运行期无限递归）。
func ValidateDefinitionWithSelf(def *types.SopDefinition, selfTemplateID string, allowIncompleteExecutor bool) []string {
	if def == nil || len(def.Nodes) == 0 {
		return []string{"流程里还没有步骤，请先加一步"}
	}

	var errs []string

	// 节点编号：空 / 重复。
	nodeByID := make(map[string]*types.SopNode, len(def.Nodes))
	idCount := make(map[string]int, len(def.Nodes))
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if strings.TrimSpace(n.ID) == "" {
			errs = append(errs, fmt.Sprintf("步骤「%s」没写编号，请重新保存", displayName(n)))
			continue
		}
		idCount[n.ID]++
		if _, exists := nodeByID[n.ID]; !exists {
			nodeByID[n.ID] = n
		}
	}
	dupIDs := make([]string, 0, len(idCount))
	for id, c := range idCount {
		if c > 1 {
			dupIDs = append(dupIDs, id)
		}
	}
	sort.Strings(dupIDs)
	for _, id := range dupIDs {
		errs = append(errs, fmt.Sprintf("有 %d 个步骤用了重复的编号「%s」，请重新保存", idCount[id], id))
	}

	// 连线：指向不存在的步骤。
	dangling := map[string]bool{}
	var danglingRefs []string
	for _, e := range def.Edges {
		for _, ref := range []string{e.Source, e.Target} {
			if ref == "" {
				continue
			}
			if _, ok := nodeByID[ref]; !ok && !dangling[ref] {
				dangling[ref] = true
				danglingRefs = append(danglingRefs, ref)
			}
		}
	}
	sort.Strings(danglingRefs)
	for _, ref := range danglingRefs {
		errs = append(errs, fmt.Sprintf("连线指向了不存在的步骤「%s」，请删掉这条连线", ref))
	}

	// 循环依赖：列出参与环的所有步骤名。
	if cyclic := cycleNodeIDs(def, nodeByID); len(cyclic) > 0 {
		names := make([]string, 0, len(cyclic))
		for _, id := range cyclic {
			names = append(names, displayName(nodeByID[id]))
		}
		errs = append(errs, fmt.Sprintf("流程里存在循环依赖（%s），请去掉一条连线", strings.Join(names, "→")))
	}

	// 孤立步骤（单节点流程例外）。草稿态允许画布上刚放、还没连线的步骤，
	// 所以只在严格模式（allowIncompleteExecutor=false）下检查。
	if !allowIncompleteExecutor && len(def.Nodes) > 1 {
		for i := range def.Nodes {
			id := def.Nodes[i].ID
			if id == "" {
				continue
			}
			if len(Upstreams(def, id)) == 0 && len(Downstreams(def, id)) == 0 {
				errs = append(errs, fmt.Sprintf("步骤「%s」没有和任何步骤连线，请把它接进流程或删掉", displayName(&def.Nodes[i])))
			}
		}
	}

	for i := range def.Nodes {
		n := &def.Nodes[i]
		name := displayName(n)

		// 节点类型：只认四种。未知类型跑到执行器才炸，用户看到的是内部错误，
		// 且已经烧掉一轮 token，所以在发布/发起运行的口径上就拒绝。
		if !knownSopNodeType(n.Type) {
			errs = append(errs, fmt.Sprintf("步骤「%s」的类型「%s」不认识，请重新选择（agent / script / approval / sop）", name, n.Type))
		}

		// 失败策略：选了打回必须选上游，且目标必须真是上游。
		switch n.OnFailure {
		case types.SopOnFailureReject:
			if strings.TrimSpace(n.RejectTo) == "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」失败后选了打回上游，但没选打回哪一步", name))
			} else if _, ok := nodeByID[n.RejectTo]; !ok {
				errs = append(errs, fmt.Sprintf("步骤「%s」的打回目标「%s」不存在，请重新选择", name, n.RejectTo))
			} else if !Ancestors(def, n.ID)[n.RejectTo] {
				errs = append(errs, fmt.Sprintf("步骤「%s」的打回目标必须是该步骤的上游环节", name))
			}
		}

		// 执行器完整性（草稿态可跳过）。
		if !allowIncompleteExecutor && n.Type == types.SopNodeTypeAgent {
			if msg := executorGap(n); msg != "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」%s", name, msg))
			}
		}

		// 产物路径不得越出工作区；「包含内容」必须写关键词，否则检查会静默通过。
		for _, a := range n.ArtifactOutputs {
			if strings.TrimSpace(a.Path) == "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」的产物「%s」还没填产物路径",
					name, firstNonEmpty(a.Name, "未命名")))
				continue
			}
			if escapesWorkspace(a.Path) {
				errs = append(errs, fmt.Sprintf("步骤「%s」的产物「%s」路径不能越出工作区，请改用工作区内的相对路径",
					name, firstNonEmpty(a.Name, a.Path)))
			}
			if a.Check.Type == types.SopArtifactCheckContains && strings.TrimSpace(a.Check.Keyword) == "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」的产物「%s」选了「包含内容」，但没写要包含什么",
					name, firstNonEmpty(a.Name, a.Path)))
			}
			if a.Check.Type == types.SopArtifactCheckCommand && strings.TrimSpace(a.Check.Command) == "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」的产物「%s」选了「自定义检查」，但没写要跑的命令",
					name, firstNonEmpty(a.Name, a.Path)))
			}
			if !knownArtifactCheckType(a.Check.Type) {
				errs = append(errs, fmt.Sprintf("步骤「%s」的产物「%s」的校验方式「%s」不认识，请重新选择（exists / contains / command）",
					name, firstNonEmpty(a.Name, a.Path), a.Check.Type))
			}
		}

		// 子流程节点：必须选模板，且不能引用自身。
		if n.Type == types.SopNodeTypeSOP {
			child := strings.TrimSpace(n.ChildTemplateID)
			if child == "" {
				errs = append(errs, fmt.Sprintf("步骤「%s」是子流程步骤还没选模板，请选择一个已发布流程", name))
			} else if selfTemplateID != "" && child == selfTemplateID {
				errs = append(errs, fmt.Sprintf("步骤「%s」的子流程不能递归引用自身，请换一个模板", name))
			}
		}
	}

	return errs
}

// knownSopNodeType 判断节点类型是否为定义的四种之一（精确匹配，不做大小写归一；
// 空串视为未填，由 Normalize 补默认 agent，故放行）。
func knownSopNodeType(t string) bool {
	switch t {
	case "", types.SopNodeTypeAgent, types.SopNodeTypeScript,
		types.SopNodeTypeApproval, types.SopNodeTypeSOP:
		return true
	}
	return false
}

// knownArtifactCheckType 判断产物校验方式是否为定义的三种之一（空串由 Normalize
// 补默认 exists，故放行）。
func knownArtifactCheckType(t string) bool {
	switch t {
	case "", types.SopArtifactCheckExists, types.SopArtifactCheckContains, types.SopArtifactCheckCommand:
		return true
	}
	return false
}

// executorGap 返回执行器缺项的白话描述；配置完整返回空串。
func executorGap(n *types.SopNode) string {
	switch n.Executor.Kind {
	case types.SopExecutorPlatformAgent:
		if strings.TrimSpace(n.Executor.AgentID) == "" {
			return "还没选执行角色，请选择平台智能体"
		}
	case types.SopExecutorCLI:
		if strings.TrimSpace(n.Executor.CLIKind) == "" {
			return "还没选 CLI，请选择本机编程 CLI"
		}
	case "":
		return "还没选执行角色/CLI，请先选一个"
	default:
		return fmt.Sprintf("的执行方式「%s」不认识，请重新选择", n.Executor.Kind)
	}
	return ""
}

// cycleNodeIDs 返回参与循环依赖的节点 id（升序）：从该节点的下游出发能走回自己，
// 即说明它在环上。只收真正的环成员，不牵连环下游的节点。
func cycleNodeIDs(def *types.SopDefinition, nodeByID map[string]*types.SopNode) []string {
	var ids []string
	for id := range nodeByID {
		if reachesSelf(def, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// reachesSelf 判断从 id 的下游出发沿连线能否回到 id（自环立即命中）。
func reachesSelf(def *types.SopDefinition, id string) bool {
	queue := Downstreams(def, id)
	seen := map[string]bool{}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == id {
			return true
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		queue = append(queue, Downstreams(def, next)...)
	}
	return false
}

// escapesWorkspace 判断产物路径是否为绝对路径或含 .. 片段（越出工作区）。
func escapesWorkspace(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "~") {
		return true
	}
	// Windows 盘符（C: / C:\…）。
	if len(p) >= 2 && p[1] == ':' {
		return true
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func displayName(n *types.SopNode) string {
	if n == nil {
		return "未命名"
	}
	if strings.TrimSpace(n.Name) != "" {
		return n.Name
	}
	if n.ID != "" {
		return n.ID
	}
	return "未命名"
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
