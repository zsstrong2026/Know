package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// childItemsFile 是模板节点声明 produces_child_items=true 时的产物路径（spec §16.24）。
const childItemsFile = "out/child-items.json"

// childItemsLimit 是单节点一次产出的子事项上限（超限即失败，绝不静默截断）。
const childItemsLimit = 20

// childItemsDoc 是 out/child-items.json 的结构。
type childItemsDoc struct {
	Children []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Kind        string `json:"kind"`
		TemplateID  string `json:"template_id"`
	} `json:"children"`
}

// maybeCreateChildItems 在门禁通过后按节点声明产出子事项（spec §16.24）。
//
// 口径：
//   - 只在门禁 pass 且节点声明 produces_child_items 时执行；
//   - 清单格式不对 / 超 20 条 → 节点 failed（原因白话），不部分创建；
//   - 运行没有事项（脚本直跑）→ 跳过，只记日志（没有可挂载的父项）。
func (e *Engine) maybeCreateChildItems(ctx context.Context, run *types.SopRun, node *types.SopNode, ws *Workspace) error {
	if !node.ProducesChildItems {
		return nil
	}
	if run.ItemID == "" {
		log.Printf("[sop-engine] 运行 %s 节点 %s 声明产出子事项，但运行没有关联事项，已跳过", run.ID, node.ID)
		return nil
	}
	if e.deps.CreateItems == nil {
		log.Printf("[sop-engine] 运行 %s 节点 %s 需要建子事项，但引擎未接线 CreateItems，已跳过", run.ID, node.ID)
		return nil
	}
	full, err := SafeWorkspacePath(ws, childItemsFile)
	if err != nil {
		return fmt.Errorf("子事项清单路径非法：%w", err)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("子事项清单不存在：需要 %s，格式 {\"children\":[{\"title\":\"…\"}]}", childItemsFile)
	}
	var doc childItemsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("子事项清单格式不对：需要 {\"children\":[{\"title\":\"…\",\"description\":\"…\"}]}（%v）", err)
	}
	if len(doc.Children) > childItemsLimit {
		return fmt.Errorf("子事项清单超过 %d 条上限（实际 %d 条），请精简后重跑", childItemsLimit, len(doc.Children))
	}
	specs := make([]types.SopChildItemSpec, 0, len(doc.Children))
	for _, c := range doc.Children {
		if strings.TrimSpace(c.Title) == "" {
			continue
		}
		specs = append(specs, types.SopChildItemSpec{
			Title: c.Title, Description: c.Description, Kind: c.Kind, TemplateID: c.TemplateID,
		})
	}
	if len(specs) == 0 {
		return fmt.Errorf("子事项清单为空：至少需要一条带 title 的子事项")
	}
	if err := e.deps.CreateItems(ctx, run, specs); err != nil {
		return fmt.Errorf("创建子事项失败：%w", err)
	}
	return nil
}
