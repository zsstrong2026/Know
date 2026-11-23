// admin.go 是管理端点的业务层：PM→SOP 迁移归档摘要、CLI 凭据删除。
//
// 归档摘要是纯读：SOP 事项若带 domain_data.pm_source_id 即视为「从 PM 迁移而来」，
// 计数按项目/事项/运行/附件四层汇总。之所以在 Go 侧过滤而不是写 SQL：JSON 列在
// PostgreSQL 与 SQLite 的取值语法不同，两个 dialect 各写一份容易漂移，而迁移期
// 数据量在千级以内，全量读再过滤完全够用。
package sop

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PMArchiveCounts 迁移归档计数。
type PMArchiveCounts struct {
	Projects    int `json:"projects"`
	Items       int `json:"items"`
	Runs        int `json:"runs"`
	Attachments int `json:"attachments"`
}

// ArchiveService PM 迁移归档摘要。
type ArchiveService struct {
	projects interfaces.SopProjectRepository
	items    interfaces.SopItemRepository
	runs     interfaces.SopRunRepository
}

// NewArchiveService 构造归档摘要服务。
func NewArchiveService(
	projects interfaces.SopProjectRepository, items interfaces.SopItemRepository,
	runs interfaces.SopRunRepository,
) *ArchiveService {
	return &ArchiveService{projects: projects, items: items, runs: runs}
}

// Summary 汇总本租户「从 PM 迁移而来」的项目/事项/运行/附件数量。
//
// 判定：事项的 domain_data 里带 pm_source_id（T7 迁移写入的幂等键）。
// 项目只要有一个迁移事项就算迁移项目；运行与附件跟随其事项。
func (s *ArchiveService) Summary(ctx context.Context, tenantID uint64) (*PMArchiveCounts, error) {
	if s == nil || s.projects == nil || s.items == nil {
		return &PMArchiveCounts{}, nil
	}
	projects, err := s.projects.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	counts := &PMArchiveCounts{}
	migratedProjects := map[string]bool{}
	for i := range projects {
		proj := &projects[i]
		items, err := s.items.ListByProjectNo(ctx, tenantID, proj.ID)
		if err != nil {
			continue // 单个项目读失败不影响整体摘要
		}
		for j := range items {
			it := &items[j]
			if !isPMMigrated(it.DomainData) {
				continue
			}
			counts.Items++
			migratedProjects[proj.ID] = true
			if s.runs != nil {
				runs, err := s.runs.ListRuns(ctx, tenantID, types.SopRunFilter{ItemID: it.ID}, 0, 0)
				if err == nil {
					counts.Runs += len(runs)
				}
			}
			atts, err := s.items.ListAttachments(ctx, tenantID, it.ID)
			if err == nil {
				counts.Attachments += len(atts)
			}
		}
	}
	counts.Projects = len(migratedProjects)
	return counts, nil
}

// isPMMigrated 判断事项是否来自 PM 迁移（domain_data 带非空 pm_source_id）。
func isPMMigrated(domainData map[string]any) bool {
	if domainData == nil {
		return false
	}
	switch v := domainData["pm_source_id"].(type) {
	case string:
		return v != ""
	case nil:
		return false
	default:
		return true
	}
}

// CredentialDeleter 删除某条 CLI 凭据（实现 = 仓储的 Delete）。
type CredentialDeleter interface {
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// DeleteCredential 删除凭据：仓储没配删除能力时返回白话未就绪错误。
func DeleteCredential(
	ctx context.Context, tenantID uint64, id string, deleter CredentialDeleter,
) error {
	if deleter == nil {
		return errors.New("凭据删除通道未就绪")
	}
	if id == "" {
		return gorm.ErrRecordNotFound
	}
	return deleter.Delete(ctx, tenantID, id)
}
