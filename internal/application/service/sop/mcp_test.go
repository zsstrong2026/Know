package sop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

func newTestProvisioner(t *testing.T) (*MCPProvisioner, *gorm.DB) {
	t.Helper()
	if os.Getenv("SYSTEM_AES_KEY") == "" {
		t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.MCPEndpoint{}))
	return NewMCPProvisioner(stubEndpointStore{db: db}, "http://127.0.0.1:8080/mcp"), db
}

// stubEndpointStore 用真实 gorm 但只暴露本文件需要的三个方法。
type stubEndpointStore struct{ db *gorm.DB }

func (s stubEndpointStore) GetByName(
	ctx context.Context, tenantID uint64, name string,
) (*types.MCPEndpoint, error) {
	var ep types.MCPEndpoint
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name).First(&ep).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (s stubEndpointStore) Create(ctx context.Context, ep *types.MCPEndpoint) error {
	return s.db.WithContext(ctx).Create(ep).Error
}

func (s stubEndpointStore) Update(ctx context.Context, ep *types.MCPEndpoint) error {
	return s.db.WithContext(ctx).Save(ep).Error
}

// 确定性 + 节点隔离 + 白名单/工具集落库。
func TestEnsureNodeEndpointDeterministicAndScoped(t *testing.T) {
	p, db := newTestProvisioner(t)
	ctx := context.Background()

	url1, tok1, err := p.EnsureNodeEndpoint(ctx, 1, "run-1", "n1", []string{"kb-a"})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(tok1, "mcp_"), "token 前缀固定")
	require.Contains(t, url1, "127.0.0.1:8080/mcp")

	url2, tok2, err := p.EnsureNodeEndpoint(ctx, 1, "run-1", "n1", []string{"kb-a"})
	require.NoError(t, err)
	require.Equal(t, tok1, tok2, "确定性：重启/续跑拿到同一个 token")
	require.Equal(t, url1, url2)

	var ep types.MCPEndpoint
	require.NoError(t, db.First(&ep, "name = ?", "sop:run-1:n1").Error)
	require.Equal(t, types.StringArray{"kb-a"}, ep.KnowledgeBaseIDs, "白名单=解析后的 KB 范围")
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(tok1))), ep.TokenHash)
	require.Contains(t, []string(ep.Tools), types.MCPEndpointToolSOPWorkspaceRead)
	require.NotContains(t, []string(ep.Tools), types.MCPEndpointToolSOPCreateRun,
		"CLI 节点端点不给驱动 SOP 的工具")
	require.NotContains(t, []string(ep.Tools), types.MCPEndpointToolSOPApproveNode)

	_, tok3, err := p.EnsureNodeEndpoint(ctx, 1, "run-1", "n2", []string{"kb-a"})
	require.NoError(t, err)
	require.NotEqual(t, tok1, tok3, "节点之间互相隔离")

	// nil 白名单 = 全部自有 KB（既有语义：空=不限）
	_, _, err = p.EnsureNodeEndpoint(ctx, 1, "run-2", "n1", nil)
	require.NoError(t, err)
	var ep2 types.MCPEndpoint
	require.NoError(t, db.First(&ep2, "name = ?", "sop:run-2:n1").Error)
	require.Empty(t, ep2.KnowledgeBaseIDs)
}

// 范围变化时更新既有端点（配置改范围 → 下次执行即生效）。
func TestEnsureNodeEndpointUpdatesScope(t *testing.T) {
	p, db := newTestProvisioner(t)
	ctx := context.Background()
	_, tok1, err := p.EnsureNodeEndpoint(ctx, 1, "r", "n", []string{"kb-a"})
	require.NoError(t, err)

	_, tok2, err := p.EnsureNodeEndpoint(ctx, 1, "r", "n", []string{"kb-a", "kb-b"})
	require.NoError(t, err)
	require.Equal(t, tok1, tok2, "范围变化不改 token")

	var ep types.MCPEndpoint
	require.NoError(t, db.First(&ep, "name = ?", "sop:r:n").Error)
	require.Equal(t, types.StringArray{"kb-a", "kb-b"}, ep.KnowledgeBaseIDs)
}

// 缺 SYSTEM_AES_KEY：明确报错（不静默降级成空凭据）。
func TestEnsureNodeEndpointMissingKeyLoudError(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.MCPEndpoint{}))

	p := NewMCPProvisioner(stubEndpointStore{db: db}, "http://x/mcp")
	_, _, err = p.EnsureNodeEndpoint(context.Background(), 1, "r", "n", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SYSTEM_AES_KEY")
}
