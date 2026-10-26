package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakePMDirCapability struct {
	tenantID uint64
	kbID     string
	dirs     []types.PMDir
	err      error
}

func (f *fakePMDirCapability) EnsureDirs(
	_ context.Context, tenantID uint64, kbID string, dirs []types.PMDir,
) error {
	f.tenantID, f.kbID, f.dirs = tenantID, kbID, dirs
	return f.err
}

// 目录路径解析：逻辑目录 → 实际落库标题/前缀，降级模式必须稳定可预测
func TestPMKBDirResolve(t *testing.T) {
	svc := NewPMKBDirService(nil)

	cases := []struct {
		dir  types.PMDir
		name string
		want string
	}{
		{types.PMDirRequirement, "", "需求"},
		{types.PMDirRequirement, "登录优化", "REQ-登录优化"},
		{types.PMDirPlanHistory, "首期规划", "ROADMAP-HIS-首期规划"},
		{types.PMDirPRD, "v1.2.0", "PRD-v1.2.0"},
		{types.PMDirPrototype, "v1.2.0", "PROTO-v1.2.0"},
		{types.PMDirMaterial, "brochure-产品宣传册", "MAT-brochure-产品宣传册"},
		{types.PMDirPlanHistory, "  首期规划  ", "ROADMAP-HIS-首期规划"},
		{types.PMDir("unknown"), "x", "x"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, svc.Resolve(c.dir, c.name), "dir=%s name=%s", c.dir, c.name)
	}
}

func TestPMKBDirEnsureProjectDirs(t *testing.T) {
	// 目录能力缺失：降级为前缀模式，不预建也不应报错
	require.NoError(t, NewPMKBDirService(nil).EnsureProjectDirs(context.Background(), 1, "kb-1"))

	cap := &fakePMDirCapability{}
	svc := NewPMKBDirService(cap)
	require.NoError(t, svc.EnsureProjectDirs(context.Background(), 7, "kb-1"))
	assert.Equal(t, uint64(7), cap.tenantID)
	assert.Equal(t, "kb-1", cap.kbID)
	assert.Equal(t, types.PMDirs, cap.dirs)
}
