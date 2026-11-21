package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newMigrateTestDB 建 sqlite :memory: 库：PM 源表用 Raw DDL 按 types/pm.go 的实际列建最小列集
// （不 AutoMigrate 源 struct，确保镜像 tag 与真实 schema 的偏差会被测试暴露），
// SOP 目标表走 AutoMigrate。
func newMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	for _, ddl := range []string{
		`CREATE TABLE pm_projects (
			id VARCHAR(36) PRIMARY KEY, tenant_id INTEGER NOT NULL, name VARCHAR(255) NOT NULL,
			description TEXT NOT NULL DEFAULT '', kb_id VARCHAR(36) NOT NULL DEFAULT '',
			session_id VARCHAR(36) NOT NULL DEFAULT '', status VARCHAR(16) NOT NULL DEFAULT 'active',
			owner_id VARCHAR(36) NOT NULL DEFAULT '', created_by VARCHAR(36) NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE pm_requirements (
			id VARCHAR(36) PRIMARY KEY, tenant_id INTEGER NOT NULL, project_id VARCHAR(36) NOT NULL,
			title VARCHAR(255) NOT NULL, description TEXT NOT NULL DEFAULT '',
			priority VARCHAR(8) NOT NULL DEFAULT 'P1', status VARCHAR(32) NOT NULL DEFAULT 'pending_analysis',
			source VARCHAR(32) NOT NULL DEFAULT 'other', proposer VARCHAR(128) NOT NULL DEFAULT '',
			ai_analysis JSON NOT NULL DEFAULT '{}', design_plan TEXT NOT NULL DEFAULT '',
			session_id VARCHAR(36) NOT NULL DEFAULT '', knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
			actual_launch_date DATE, effect_notes TEXT NOT NULL DEFAULT '',
			value_achieved VARCHAR(16) NOT NULL DEFAULT '', retro_notes TEXT NOT NULL DEFAULT '',
			reject_reason TEXT NOT NULL DEFAULT '', value_achieved_at DATETIME,
			owner_id VARCHAR(36) NOT NULL DEFAULT '', created_by VARCHAR(36) NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE pm_requirement_attachments (
			id VARCHAR(36) PRIMARY KEY, tenant_id INTEGER NOT NULL, requirement_id VARCHAR(36) NOT NULL,
			file_name VARCHAR(255) NOT NULL DEFAULT '', content_type VARCHAR(128) NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0, text_content TEXT NOT NULL DEFAULT '', data BLOB NOT NULL,
			created_by VARCHAR(36) NOT NULL DEFAULT '', created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	require.NoError(t, db.AutoMigrate(
		&types.SopProject{}, &types.SopItem{}, &types.SopItemAttachment{},
		&types.SopItemActivity{}, &types.SopItemClarification{},
	))
	return db
}

// seedPMFixtures 造 1 个项目 2 条需求：launched（含复盘字段 + 附件）与 rejected（含 reject_reason）。
func seedPMFixtures(t *testing.T, db *gorm.DB) {
	t.Helper()
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	launchDate := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec(`INSERT INTO pm_projects
		(id, tenant_id, name, description, kb_id, session_id, status, owner_id, created_by, created_at, updated_at)
		VALUES (?, 7, 'Know 协作平台', '知识库产品线', 'kb-1', 'sess-1', 'active', 'u1', 'u1', ?, ?)`,
		"pm-p1", base, base).Error)
	require.NoError(t, db.Exec(`INSERT INTO pm_requirements
		(id, tenant_id, project_id, title, description, priority, status, source, proposer, ai_analysis,
		 design_plan, session_id, knowledge_id, actual_launch_date, effect_notes, value_achieved, retro_notes,
		 reject_reason, owner_id, created_by, created_at, updated_at)
		VALUES (?, 7, ?, ?, '已上线需求描述', 'P0', 'launched', 'customer', '张三',
		        '{"conclusion":"可行"}', '先做搜索后做推荐', 'sess-r1', 'k-1', ?, '转化 +8%', 'achieved',
		        '提前两周上线', '', 'u2', 'u1', ?, ?)`,
		"pm-r1", "pm-p1", "需求一", launchDate, base, base.Add(time.Hour)).Error)
	require.NoError(t, db.Exec(`INSERT INTO pm_requirements
		(id, tenant_id, project_id, title, description, priority, status, source, proposer, ai_analysis,
		 design_plan, session_id, knowledge_id, effect_notes, value_achieved, retro_notes,
		 reject_reason, owner_id, created_by, created_at, updated_at)
		VALUES (?, 7, ?, ?, '被驳回需求描述', 'P2', 'rejected', 'internal', '李四',
		        '{}', '', '', '', '', '', '', '不匹配本季度目标', '', 'u1', ?, ?)`,
		"pm-r2", "pm-p1", "需求二", base.Add(2*time.Hour), base.Add(2*time.Hour)).Error)
	require.NoError(t, db.Exec(`INSERT INTO pm_requirement_attachments
		(id, tenant_id, requirement_id, file_name, content_type, size, text_content, data, created_by, created_at, updated_at)
		VALUES (?, 7, ?, 'spec.md', 'text/markdown', 11, '附件正文样本', ?, 'u1', ?, ?)`,
		"pm-a1", "pm-r1", []byte("attachment!"), base, base).Error)
}

// statusOf 按源需求 id 找到迁后事项的状态。
func statusOf(t *testing.T, db *gorm.DB, sourceLabel string) string {
	t.Helper()
	var items []types.SopItem
	require.NoError(t, db.Find(&items).Error)
	for _, it := range items {
		if it.DomainData["pm_source_id"] == sourceLabel {
			return it.Status
		}
	}
	require.Failf(t, "事项未迁移", "pm_source_id=%s 无对应 SOP 事项", sourceLabel)
	return ""
}

func TestMigratePMIdempotentAndLossless(t *testing.T) {
	db := newMigrateTestDB(t)
	seedPMFixtures(t, db)
	dir := t.TempDir()
	require.NoError(t, runMigration(db, dir))

	// 备份存在且覆盖三张源表
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	raw, err := os.ReadFile(dir + string(os.PathSeparator) + entries[0].Name())
	require.NoError(t, err)
	for _, table := range []string{"pm_projects", "pm_requirements", "pm_requirement_attachments"} {
		require.Truef(t, strings.Contains(string(raw), table), "备份缺少 %s", table)
	}

	// 项目：code 由名称派生、workspace_root 默认、kb 归属 pm、domain_data 留源 id
	var projects []types.SopProject
	require.NoError(t, db.Find(&projects).Error)
	require.Len(t, projects, 1)
	require.Equal(t, "KNOW", projects[0].Code)
	require.Equal(t, "data/sop/workspaces/KNOW/", projects[0].WorkspaceRoot)
	require.Equal(t, types.SopKBOwnerPM, projects[0].KBOwner)
	require.Equal(t, "pm-p1", projects[0].DomainData["pm_source_id"])
	require.Equal(t, "kb-1", projects[0].KBID)
	require.Equal(t, uint64(7), projects[0].TenantID)

	// 事项数=2、编号按创建时间顺序；状态映射
	var items []types.SopItem
	require.NoError(t, db.Order("no ASC").Find(&items).Error)
	require.Len(t, items, 2)
	require.Equal(t, "KNOW-1", items[0].No)
	require.Equal(t, "KNOW-2", items[1].No)
	require.Equal(t, types.SopItemStatusDone, statusOf(t, db, "pm-r1"))
	require.Equal(t, types.SopItemStatusClosed, statusOf(t, db, "pm-r2"))

	// 无损：domain_data 保留分析/方案/会话/知识/驳回原因，outcome 保留复盘字段
	require.Equal(t, "先做搜索后做推荐", items[0].DomainData["design_plan"])
	require.Equal(t, "sess-r1", items[0].DomainData["session_id"])
	require.Equal(t, "k-1", items[0].DomainData["knowledge_id"])
	require.NotNil(t, items[0].DomainData["ai_analysis"])
	require.Equal(t, "不匹配本季度目标", items[1].DomainData["reject_reason"])
	require.Equal(t, "achieved", items[0].Outcome["value_achieved"])
	require.Equal(t, "转化 +8%", items[0].Outcome["effect_notes"])
	require.Equal(t, "提前两周上线", items[0].Outcome["retro_notes"])
	require.Equal(t, "2026-04-20", items[0].Outcome["actual_date"])

	// 附件同构直迁
	var atts []types.SopItemAttachment
	require.NoError(t, db.Find(&atts).Error)
	require.Len(t, atts, 1)
	require.Equal(t, items[0].ID, atts[0].ItemID)
	require.Equal(t, "spec.md", atts[0].Name)
	require.Equal(t, "text/markdown", atts[0].MediaType)
	require.Equal(t, []byte("attachment!"), atts[0].Data)
	require.Equal(t, "附件正文样本", atts[0].TextContent)

	// 每行一条审计活动
	var acts []types.SopItemActivity
	require.NoError(t, db.Find(&acts).Error)
	require.Len(t, acts, 2)
	require.Equal(t, types.SopActivityKindAudit, acts[0].Kind)
	require.Equal(t, "PM迁移", acts[0].Action)

	// 幂等：再跑一次不新增
	require.NoError(t, runMigration(db, dir))
	var itemCount, projectCount, actCount int64
	require.NoError(t, db.Model(&types.SopItem{}).Count(&itemCount).Error)
	require.NoError(t, db.Model(&types.SopProject{}).Count(&projectCount).Error)
	require.NoError(t, db.Model(&types.SopItemActivity{}).Count(&actCount).Error)
	require.Equal(t, int64(2), itemCount)
	require.Equal(t, int64(1), projectCount)
	require.Equal(t, int64(2), actCount)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2, "每次执行都应留下独立备份，不覆盖既有备份")
}

func TestMigratePMProjectCodeCollisionGetsSuffix(t *testing.T) {
	db := newMigrateTestDB(t)
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{"pm-p1", "pm-p2"} {
		require.NoError(t, db.Exec(`INSERT INTO pm_projects
			(id, tenant_id, name, created_at, updated_at) VALUES (?, 7, ?, ?, ?)`,
			id, "Know 平台", base.Add(time.Duration(i)*time.Hour), base).Error)
	}
	require.NoError(t, runMigration(db, t.TempDir()))

	var projects []types.SopProject
	require.NoError(t, db.Order("code ASC").Find(&projects).Error)
	require.Len(t, projects, 2)
	require.Equal(t, "KNOW", projects[0].Code)
	require.Equal(t, "KNOW2", projects[1].Code)
	require.Equal(t, "data/sop/workspaces/KNOW2/", projects[1].WorkspaceRoot)
}

func TestMigratePMRollsBackOnFailureButKeepsBackup(t *testing.T) {
	db := newMigrateTestDB(t)
	seedPMFixtures(t, db)
	<-makeSopItemsUnwritable(t, db)
	dir := t.TempDir()

	require.Error(t, runMigration(db, dir))
	var n int64
	require.NoError(t, db.Model(&types.SopProject{}).Count(&n).Error)
	require.Zero(t, n, "失败必须整体回滚，不留半成品项目")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "回滚不影响已落盘的备份")
}

// makeSopItemsUnwritable 用 BEFORE INSERT 触发器让事项写入失败，模拟事务中途出错。
func makeSopItemsUnwritable(t *testing.T, db *gorm.DB) <-chan struct{} {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_sop_item_insert BEFORE INSERT ON sop_items
		BEGIN SELECT RAISE(ABORT, 'sop_items unwritable'); END`).Error)
	done := make(chan struct{})
	close(done)
	return done
}

func TestPMStatusToSopStatusMapping(t *testing.T) {
	cases := map[string]string{
		types.RequirementStatusPendingAnalysis: types.SopItemStatusBacklog,
		"analyzing":                            types.SopItemStatusBacklog,
		types.RequirementStatusConfirmed:       types.SopItemStatusTodo,
		types.RequirementStatusScheduled:       types.SopItemStatusInProgress,
		types.RequirementStatusDeveloping:      types.SopItemStatusInProgress,
		types.RequirementStatusLaunched:        types.SopItemStatusDone,
		types.RequirementStatusRejected:        types.SopItemStatusClosed,
		types.RequirementStatusClosed:          types.SopItemStatusClosed,
	}
	for in, want := range cases {
		require.Equalf(t, want, pmStatusToSopStatus(in), "状态 %s 映射错误", in)
	}
}

func TestDeriveProjectCodeFallsBackAndFitsTenChars(t *testing.T) {
	require.Equal(t, "KNOW", DeriveProjectCode("Know 协作平台"))
	require.Equal(t, "PM", DeriveProjectCode("知识库"))
	require.Equal(t, "PM", DeriveProjectCode(""))
	require.Equal(t, "ABCDEFGHIJ", DeriveProjectCode("abcdefghijklmn"))
	require.Equal(t, "P123", DeriveProjectCode("123"))
}
