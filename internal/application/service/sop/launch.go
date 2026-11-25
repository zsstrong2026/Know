package sop

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// LaunchTarget 批量触发展开后的一项：待运行的事项与其模板。
type LaunchTarget struct {
	ItemID     string
	TemplateID string
}

// LaunchResult 批量触发结果：建了运行的、跳过的（运行中/无模板/不存在）、失败的。
type LaunchResult struct {
	Started []string `json:"started"`
	Skipped []string `json:"skipped"`
	Failed  []string `json:"failed"`
}

// ExpandLaunchTargets 把批量触发的入参展开成可直接建运行的事项列表。
//
// 口径（与单条「整条重触发」一致）：
//   - 大项展开为子项，父项本身不触发——父子两次运行会互相覆盖产物；
//   - 运行中/待介入、无可用模板、不存在的事项进 skipped；
//   - 子项自己的模板优先，缺失时用项目默认模板。
func (s *ItemService) ExpandLaunchTargets(
	ctx context.Context, tenantID uint64, ids []string,
) (targets []LaunchTarget, skipped []string, err error) {
	if len(ids) > sopBulkLimit {
		return nil, nil, ErrSopItemTooMany
	}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		it, err := s.items.GetByID(ctx, tenantID, id)
		if err != nil {
			skipped = append(skipped, id)
			continue
		}
		if !it.IsBig {
			if s.skipLaunch(ctx, tenantID, it) {
				skipped = append(skipped, id)
				continue
			}
			targets = append(targets, LaunchTarget{ItemID: it.ID, TemplateID: s.launchTemplate(ctx, tenantID, it)})
			continue
		}
		kids, err := s.items.ListWithDerived(ctx, tenantID, it.ProjectID,
			types.SopItemFilter{ParentID: &it.ID}, nil, nil)
		if err != nil || len(kids) == 0 {
			skipped = append(skipped, id)
			continue
		}
		for i := range kids {
			kid := &kids[i]
			if seen[kid.ID] {
				continue
			}
			seen[kid.ID] = true
			if s.skipLaunch(ctx, tenantID, &kid.SopItem) {
				skipped = append(skipped, kid.ID)
				continue
			}
			targets = append(targets, LaunchTarget{
				ItemID: kid.ID, TemplateID: s.launchTemplate(ctx, tenantID, &kid.SopItem),
			})
		}
	}
	return targets, skipped, nil
}

// skipLaunch 判断单个事项是否跳过：正在推进/待介入，或没有可用模板。
func (s *ItemService) skipLaunch(ctx context.Context, tenantID uint64, it *types.SopItem) bool {
	if s.launchTemplate(ctx, tenantID, it) == "" {
		return true
	}
	return s.isRunning(ctx, tenantID, it.ID)
}

// launchTemplate 事项自己的模板优先，缺失时回落到项目默认模板。
func (s *ItemService) launchTemplate(ctx context.Context, tenantID uint64, it *types.SopItem) string {
	if it == nil {
		return ""
	}
	if tpl := strings.TrimSpace(it.TemplateID); tpl != "" {
		return tpl
	}
	if s.projects == nil {
		return ""
	}
	proj, err := s.projects.GetByID(ctx, tenantID, it.ProjectID)
	if err != nil || proj == nil {
		return ""
	}
	return strings.TrimSpace(proj.DefaultTemplateID)
}
