package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PMDirCapability 知识库多级目录能力（可选）。
// WeKnora 知识库当前只有知识点标题，不实现该接口即降级为命名前缀模式。
type PMDirCapability interface {
	EnsureDirs(ctx context.Context, tenantID uint64, kbID string, dirs []types.PMDir) error
}

type pmKBDirService struct {
	capability PMDirCapability
}

// NewPMKBDirService capability 为 nil 表示平台目录能力受限，全部走命名前缀降级
func NewPMKBDirService(capability PMDirCapability) interfaces.PMKBDirService {
	return &pmKBDirService{capability: capability}
}

// EnsureProjectDirs 初始化项目产物目录；前缀模式下无需预建，直接返回
func (s *pmKBDirService) EnsureProjectDirs(ctx context.Context, tenantID uint64, kbID string) error {
	if s.capability == nil {
		return nil
	}
	return s.capability.EnsureDirs(ctx, tenantID, kbID, types.PMDirs)
}

// Resolve 逻辑目录 + 名称 → 实际落库标题/前缀。
// 纯函数、无副作用；名称为空时返回目录本名，供目录占位或建库后展示。
func (s *pmKBDirService) Resolve(dir types.PMDir, name string) string {
	name = strings.TrimSpace(name)
	base, prefix := pmDirMeta(dir)
	if name == "" {
		return base
	}
	if prefix == "" {
		return name
	}
	return prefix + name
}

// pmDirMeta 目录名与降级前缀的唯一映射表，新增目录只需改这里
func pmDirMeta(dir types.PMDir) (base string, prefix string) {
	switch dir {
	case types.PMDirRequirement:
		return "需求", "REQ-"
	case types.PMDirPlanHistory:
		return "规划历史", "ROADMAP-HIS-"
	case types.PMDirPRD:
		return "PRD", "PRD-"
	case types.PMDirPrototype:
		return "原型", "PROTO-"
	case types.PMDirMaterial:
		return "资料", "MAT-"
	default:
		return string(dir), ""
	}
}
