package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// pmSourceIDKey 是迁移幂等键：写进 sop_projects / sop_items 的 domain_data，
// 判重一律在 Go 侧遍历比对（PG 的 JSONB 与 SQLite 的 JSON 取列语法不同，不下推 SQL）。
const pmSourceIDKey = "pm_source_id"

// migrateProjects 迁全部 PM 项目并逐项目迁需求；单事务由 runMigration 提供。
//
// 已存在的项目（domain_data.pm_source_id 命中）不重复插入，但仍继续迁其需求——
// 项目级幂等与需求级幂等各自独立，一次中断后重跑能把剩余需求补齐。
func migrateProjects(tx *gorm.DB) error {
	var pmProjects []pmProjectSource
	if err := tx.Order("created_at ASC").Find(&pmProjects).Error; err != nil {
		return fmt.Errorf("读取 pm_projects 失败：%w", err)
	}
	if len(pmProjects) == 0 {
		log.Print("没有可迁移的 PM 项目")
		return nil
	}

	existing, err := loadMigratedProjectIDs(tx)
	if err != nil {
		return err
	}
	usedCodes, err := loadUsedProjectCodes(tx)
	if err != nil {
		return err
	}

	mapping := make(map[string]*types.SopProject, len(pmProjects))
	projectRepo := repository.NewSopProjectRepo(tx)
	created := 0
	for i := range pmProjects {
		src := &pmProjects[i]
		if sp, ok := existing[src.ID]; ok {
			mapping[src.ID] = sp
			continue
		}
		sp := buildSopProject(src, usedCodes)
		if err := projectRepo.Create(context.Background(), sp); err != nil {
			return fmt.Errorf("创建 SOP 项目（源 pm_projects/%s）失败：%w", src.ID, err)
		}
		usedCodes[codeKey(sp.TenantID, sp.Code)] = true
		mapping[src.ID] = sp
		created++
	}

	itemCount, attCount, err := migrateItems(tx, mapping)
	if err != nil {
		return err
	}
	log.Printf("PM 迁移：项目 %d（新建 %d / 已存在 %d），需求 %d，附件 %d",
		len(pmProjects), created, len(pmProjects)-created, itemCount, attCount)
	return nil
}

// loadMigratedProjectIDs 读出已迁移项目（domain_data.pm_source_id 非空）的源 id → 项目。
func loadMigratedProjectIDs(tx *gorm.DB) (map[string]*types.SopProject, error) {
	var live []types.SopProject
	if err := tx.Find(&live).Error; err != nil {
		return nil, fmt.Errorf("读取 sop_projects 失败：%w", err)
	}
	out := make(map[string]*types.SopProject, len(live))
	for i := range live {
		if src, ok := live[i].DomainData[pmSourceIDKey].(string); ok && src != "" {
			out[src] = &live[i]
		}
	}
	return out, nil
}

// loadUsedProjectCodes 收集租户内已占用编号，供 code 冲突加后缀。
func loadUsedProjectCodes(tx *gorm.DB) (map[string]bool, error) {
	var rows []struct {
		TenantID uint64
		Code     string
	}
	if err := tx.Model(&types.SopProject{}).Select("tenant_id, code").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 sop_projects 编号失败：%w", err)
	}
	// 编号唯一性按租户判定，但迁移时同名不同租户各自成行；用「租户+编号」拼键避免误判。
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[codeKey(r.TenantID, r.Code)] = true
	}
	return out, nil
}

func codeKey(tenantID uint64, code string) string {
	return strconv.FormatUint(tenantID, 10) + ":" + code
}

// buildSopProject 把一条 pm_projects 行映射成 SOP 项目：
// code 由 name 派生（大写、去非字母数字、冲突加数字后缀），
// workspace_root 默认 data/sop/workspaces/<code>/，kb_owner=pm（迁移库仅解绑）。
func buildSopProject(src *pmProjectSource, usedCodes map[string]bool) *types.SopProject {
	code := uniqueProjectCode(src.TenantID, src.Name, usedCodes)
	return &types.SopProject{
		TenantID:           src.TenantID,
		OwnerUserID:        src.OwnerID,
		Name:               src.Name,
		Description:        src.Description,
		Code:               code,
		KBID:               src.KBID,
		KBOwner:            types.SopKBOwnerPM,
		DefaultExecutor:    types.SopExecutorPlatformAgent,
		NotifyOnGateOrFail: true,
		WorkspaceRoot:      "data/sop/workspaces/" + code + "/",
		Status:             types.PMProjectStatusActive,
		DomainData:         projectDomainData(src),
		CreatedAt:          src.CreatedAt,
		UpdatedAt:          src.UpdatedAt,
	}
}

// projectDomainData 保留迁移后仍需追溯的 PM 项目字段（源 id 是幂等键）。
func projectDomainData(src *pmProjectSource) map[string]any {
	return map[string]any{
		pmSourceIDKey:   src.ID,
		"pm_session_id": src.SessionID,
		"pm_status":     src.Status,
	}
}

// DeriveProjectCode 把项目名压缩成合法编号前缀：[A-Z][A-Z0-9]{1,9}。
// 无 ASCII 字母可用的名字（如纯中文）退化为 PM，避免空 code。
func DeriveProjectCode(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r < unicode.MaxASCII && (r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 10 {
			break
		}
	}
	code := b.String()
	if code == "" {
		return "PM"
	}
	if code[0] < 'A' || code[0] > 'Z' {
		code = "P" + code
	}
	if len(code) > 10 {
		code = code[:10]
	}
	for len(code) < 2 {
		code += "X"
	}
	return code
}

// uniqueProjectCode 在租户内取一个未被占用的编号：冲突时追加 2、3、4… 后缀，
// 后缀挤占的长度从基数尾部裁掉，保证总长不超 10。
func uniqueProjectCode(tenantID uint64, name string, used map[string]bool) string {
	base := DeriveProjectCode(name)
	if !used[codeKey(tenantID, base)] {
		return base
	}
	for i := 2; ; i++ {
		suffix := strconv.Itoa(i)
		head := base
		if len(head)+len(suffix) > 10 {
			head = head[:10-len(suffix)]
		}
		cand := head + suffix
		if !used[codeKey(tenantID, cand)] {
			return cand
		}
	}
}
