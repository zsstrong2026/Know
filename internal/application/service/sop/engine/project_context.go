package engine

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// projectContextCap 是「项目现状」块的硬顶（汉字），超过按优先级递减。
const projectContextCap = 1500

// BriefProvider 是「项目简报正文」的读取面（真实实现 = sop.BriefService）。
type BriefProvider interface {
	ProjectBrief(ctx context.Context, tenantID uint64, projectID string) (string, error)
}

// projectBriefPath 是简报物化到工作区后的相对路径（太长的完整简报放这里）。
const projectBriefPath = "inputs/project-brief.md"

// BuildProjectContext 组装任务书里的「项目现状」块（纯函数，硬顶 1500 汉字）。
//
// 三段，越靠前越重要：
//   - 最近决策：置顶的全带 + 其余按 seq 降序，最多 6 条（标题 + 一句话正文）；
//   - 兄弟事项进度：`3/7 已完成`，再列 3 条正在推进的事项编号与标题；
//   - 完整简报：指向 workarea 里的 inputs/project-brief.md（节点可自行读取）。
//
// 超出硬顶时先减决策条数，再减兄弟事项，最后截断整段。
func BuildProjectContext(items []types.SopItem, notes []types.SopProjectNote, briefPath string) string {
	if len(items) == 0 && len(notes) == 0 {
		return ""
	}
	body := renderProjectContext(items, notes, briefPath, 6, 3)
	if len([]rune(body)) <= projectContextCap {
		return body
	}
	for _, lim := range []struct{ notes, items int }{{4, 3}, {2, 2}, {1, 0}, {0, 0}} {
		body = renderProjectContext(items, notes, briefPath, lim.notes, lim.items)
		if len([]rune(body)) <= projectContextCap {
			return body
		}
	}
	return clipBlock(body, projectContextCap)
}

// renderProjectContext 按给定预算渲染（决策与兄弟事项各限条数）。
func renderProjectContext(
	items []types.SopItem, notes []types.SopProjectNote, briefPath string, maxNotes, maxItems int,
) string {
	var b strings.Builder

	if maxNotes > 0 {
		decisions := selectDecisionNotes(notes, maxNotes)
		if len(decisions) > 0 {
			b.WriteString("最近决策：\n")
			for _, n := range decisions {
				title := strings.TrimSpace(n.Title)
				if title == "" {
					title = firstLineOf(n.Body)
				}
				fmt.Fprintf(&b, "- %s：%s\n", title, firstLineOf(n.Body))
			}
		}
	}

	if maxItems > 0 && len(items) > 0 {
		done, total := 0, 0
		active := make([]types.SopItem, 0, len(items))
		for _, it := range items {
			total++
			if it.Status == types.SopItemStatusDone || it.Status == types.SopItemStatusClosed {
				done++
				continue
			}
			active = append(active, it)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "兄弟事项进度：%d/%d 已完成", done, total)
		if len(active) > 0 {
			sort.SliceStable(active, func(i, j int) bool { return active[i].No < active[j].No })
			if len(active) > maxItems {
				active = active[:maxItems]
			}
			parts := make([]string, 0, len(active))
			for _, it := range active {
				parts = append(parts, it.No+" "+it.Title)
			}
			fmt.Fprintf(&b, "；进行中：%s", strings.Join(parts, "、"))
		}
		b.WriteString("\n")
	}

	if strings.TrimSpace(briefPath) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "项目简报全文：%s（含项目背景、风险与运行结论，需要细节就打开它）\n", briefPath)
	}
	return strings.TrimRight(b.String(), "\n")
}

// selectDecisionNotes 选决策/风险类条目：置顶的全带，其余按 seq 降序补足。
func selectDecisionNotes(notes []types.SopProjectNote, max int) []types.SopProjectNote {
	var typed []types.SopProjectNote
	for _, n := range notes {
		if n.Kind == types.SopNoteKindDecision || n.Kind == types.SopNoteKindRisk {
			typed = append(typed, n)
		}
	}
	sort.SliceStable(typed, func(i, j int) bool { return typed[i].Seq > typed[j].Seq })
	var pinned, rest []types.SopProjectNote
	for _, n := range typed {
		if n.Pinned {
			pinned = append(pinned, n)
		} else {
			rest = append(rest, n)
		}
	}
	if len(rest) > max {
		rest = rest[:max]
	}
	return append(pinned, rest...)
}

// firstLineOf 取首行（任务书里的每条决策只占一行）。
func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return clipBlock(s, 80)
}

// projectContextFor 拉项目简报 → 物化到 inputs/project-brief.md → 组装「项目现状」块。
//
// 三层降级，任何一层失败都只让这一段变空，绝不影响节点执行：
//   - 运行/项目为空 → 空；
//   - BriefProvider 未接线或报错 → 空（记日志）；
//   - 物化失败 → 仍用「最近决策/进度」两段（只是少了指路链接）。
func (e *Engine) projectContextFor(ctx context.Context, run *types.SopRun, ws *Workspace) string {
	if run == nil || strings.TrimSpace(run.ProjectID) == "" || e.deps.BriefProvider == nil {
		return ""
	}
	md, err := e.deps.BriefProvider.ProjectBrief(ctx, run.TenantID, run.ProjectID)
	if err != nil || strings.TrimSpace(md) == "" {
		if err != nil {
			log.Printf("[sop-engine] 运行 %s 读取项目简报失败：%v", run.ID, err)
		}
		return ""
	}
	briefPath := ""
	if p, err := ws.MaterializeProjectBrief(md); err == nil {
		briefPath = projectBriefPath
		_ = p
	} else {
		log.Printf("[sop-engine] 运行 %s 物化项目简报失败：%v", run.ID, err)
	}
	items, notes := e.projectSnapshot(ctx, run)
	return BuildProjectContext(items, notes, briefPath)
}

// projectSnapshot 取「兄弟事项 + 黑板」用于项目现状（读取失败按空处理）。
func (e *Engine) projectSnapshot(ctx context.Context, run *types.SopRun) ([]types.SopItem, []types.SopProjectNote) {
	var items []types.SopItem
	if e.deps.Items != nil {
		if list, err := e.deps.Items.ListWithDerived(ctx, run.TenantID, run.ProjectID,
			types.SopItemFilter{}, nil, nil); err == nil {
			for _, it := range list {
				items = append(items, it.SopItem)
			}
		}
	}
	var notes []types.SopProjectNote
	if e.deps.Notes != nil {
		if list, err := e.deps.Notes.List(ctx, run.TenantID, run.ProjectID, "", nil, 20); err == nil {
			notes = list
		}
	}
	return items, notes
}
