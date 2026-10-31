package repository

import (
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// workbenchRepository 是工作台仓储实现，一个结构体实现三个窄接口
// （条目 / 版本 / 文件夹），方法分散在同包 workbench_item.go、workbench_version.go、
// workbench_folder.go。
type workbenchRepository struct {
	db *gorm.DB
}

// NewWorkbenchRepository 构造工作台仓储（组合接口）。
func NewWorkbenchRepository(db *gorm.DB) interfaces.WorkbenchRepository {
	return &workbenchRepository{db: db}
}
