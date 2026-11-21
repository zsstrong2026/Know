package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
)

// pmBackup 是迁移前的全量快照：三张 PM 源表的原始行 + 导出时间。
// 备份先于任何写入完成，迁移失败时它是唯一的人工恢复依据，故不裁剪字段
// （含租户、软删行与附件字节；[]byte 由 encoding/json 自动 base64）。
type pmBackup struct {
	ExportedAt    time.Time             `json:"exported_at"`
	Projects      []pmProjectSource     `json:"pm_projects"`
	Requirements  []pmRequirementSource `json:"pm_requirements"`
	Attachments   []pmAttachmentSource  `json:"pm_requirement_attachments"`
	SourceVersion string                `json:"source_version"`
}

// 备份文件名里的时间戳把 ':' 换成 '-'：RFC3339 的冒号在 Windows 文件名里非法，
// 换掉后仍是可排序、可读回的时间串。
const backupTimeLayout = "2006-01-02T15-04-05Z07-00"

// writeBackup 导出 PM 源表到 <backupDir>/pm-migration-<RFC3339>.json，返回文件路径。
// 目录不存在时先 MkdirAll；同名文件已存在则追加 -2/-3 后缀，绝不覆盖既有备份。
func writeBackup(db *gorm.DB, backupDir string) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录 %s 失败：%w", backupDir, err)
	}
	b := pmBackup{
		ExportedAt:    time.Now().UTC(),
		SourceVersion: "pm-v1",
	}
	// 备份必须包含软删行：软删不等于可以丢，恢复时也需要完整快照。
	for _, q := range []struct {
		name string
		dest any
	}{
		{"pm_projects", &b.Projects},
		{"pm_requirements", &b.Requirements},
		{"pm_requirement_attachments", &b.Attachments},
	} {
		if err := db.Unscoped().Order("created_at ASC").Find(q.dest).Error; err != nil {
			return "", fmt.Errorf("导出 %s 失败：%w", q.name, err)
		}
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化备份失败：%w", err)
	}
	path := uniqueBackupPath(backupDir, b.ExportedAt)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("写入备份 %s 失败：%w", path, err)
	}
	return path, nil
}

// uniqueBackupPath 生成不覆盖既有文件的备份路径。
func uniqueBackupPath(dir string, at time.Time) string {
	base := filepath.Join(dir, "pm-migration-"+at.Format(backupTimeLayout))
	path := base + ".json"
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		path = fmt.Sprintf("%s-%d.json", base, i)
	}
}
