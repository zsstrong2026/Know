package sop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func newCredTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.SopCLICredential{}))
	return db
}

// PATH 探测：装了的报版本，没装的给安装指引；总数恒为 10。
func TestCLIStatusDetectsPATH(t *testing.T) {
	if os.PathSeparator != '/' {
		// Windows 上 fake 的 shell 脚本不能直接执行；用 .bat 模拟「可执行 + --version」。
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "zcode.bat"),
			[]byte("@echo off\r\necho zcode 1.2.3\r\n"), 0o755))
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else {
		dir := t.TempDir()
		path := filepath.Join(dir, "zcode")
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho zcode 1.2.3\n"), 0o755))
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	svc := NewCredentialService(nil)
	st := svc.Status(context.Background())
	require.Len(t, st, 10, "10 种 CLI 全列出来")

	var z, c *CLIStatus
	for i := range st {
		switch st[i].Kind {
		case "zcode":
			z = &st[i]
		case "claude":
			c = &st[i]
		}
	}
	require.NotNil(t, z)
	require.NotNil(t, c)
	require.True(t, z.Installed, "zcode 在 PATH 上")
	require.Contains(t, z.Version, "1.2.3")
	require.False(t, c.Installed, "测试清空 PATH 后 claude 不该被误判已装")
	require.Contains(t, c.Guidance, "安装")
	require.Equal(t, "claude", c.Binary)
}

// 写入加密 + 列表脱敏（尾 4 位）+ 空密钥沿用旧值。
func TestCredentialUpsertEncryptsAndMasks(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
	db := newCredTestDB(t)
	svc := NewCredentialService(repository.NewSopCredentialRepo(db))
	ctx := context.Background()

	const secret = "sk-plain-secret-1234"
	view, err := svc.Upsert(ctx, 1, "u1", "kimi", "我的 kimi", secret, "", "")
	require.NoError(t, err)
	require.NotEqual(t, secret, view.APIKeyTail, "视图不得回明文")
	require.Equal(t, "1234", view.APIKeyTail, "只回尾 4 位")
	require.Equal(t, "我的 kimi", view.Name)

	// 库内密文
	var raw string
	require.NoError(t, db.Raw("SELECT api_key FROM sop_cli_credentials WHERE cli_kind='kimi'").
		Scan(&raw).Error)
	require.NotContains(t, raw, secret)

	list, err := svc.ListMasked(ctx, 1, "u1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "1234", list[0].APIKeyTail)

	// 空密钥：沿用旧值（尾号不变）
	again, err := svc.Upsert(ctx, 1, "u1", "kimi", "", "", "https://proxy", "")
	require.NoError(t, err)
	require.Equal(t, "1234", again.APIKeyTail, "留空=沿用旧密钥")
	require.Equal(t, "https://proxy", again.BaseURL)

	// 删除
	require.NoError(t, svc.Delete(ctx, 1, "u1", "kimi"))
	list, err = svc.ListMasked(ctx, 1, "u1")
	require.NoError(t, err)
	require.Empty(t, list)
}

// 缺 kind 时给白话错误；仓储未注入时也报未就绪。
func TestCredentialServiceValidation(t *testing.T) {
	svc := NewCredentialService(nil)
	_, err := svc.Upsert(context.Background(), 1, "u1", "  ", "", "k", "", "")
	require.Error(t, err)
	_, err = svc.ListMasked(context.Background(), 1, "u1")
	require.Error(t, err)
}
