// sop-migrate-pm 把 PM 工作站的既有数据（项目 / 需求 / 需求附件）一次性迁入 SOP 工作台。
//
// 口径（spec §11.1「手动执行约定」）：
//   - 迁移前先把三张 PM 源表全量导出成 JSON 备份，备份失败即中止，绝不先写后备份；
//   - 迁移全程单事务，任一步失败整体回滚（备份文件保留，可人工复核）；
//   - 幂等：以 domain_data.pm_source_id 为幂等键，已迁过的行跳过，可重复执行；
//   - pm_versions 等其余 PM 表不动（冻结只读归档，PmArchive 在 M9 实现）。
//
// 用法：go run ./cmd/sop-migrate-pm -dsn <gorm DSN> -backup-dir data/backups
package main

import (
	"errors"
	"flag"
	"log"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	dsn := flag.String("dsn", "", "gorm DSN（postgres://… 或 SQLite 文件路径）")
	backupDir := flag.String("backup-dir", "data/backups", "迁移前 JSON 备份目录")
	flag.Parse()

	db, err := openDB(*dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := runMigration(db, *backupDir); err != nil {
		log.Fatal(err)
	}
	log.Printf("PM 数据迁移完成，备份目录：%s", *backupDir)
}

// openDB 按 DSN 前缀选 dialector：postgres:// / postgresql:// 走 PG，其余按 SQLite 文件。
func openDB(dsn string) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("-dsn 不能为空（postgres://… 或 SQLite 文件路径）")
	}
	var dialector gorm.Dialector
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dialector = postgres.Open(dsn)
	} else {
		dialector = sqlite.Open(dsn)
	}
	return gorm.Open(dialector, &gorm.Config{})
}

// runMigration 先落 JSON 备份，再在单事务内迁移全部项目与需求。
func runMigration(db *gorm.DB, backupDir string) error {
	if _, err := writeBackup(db, backupDir); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error { return migrateProjects(tx) })
}
