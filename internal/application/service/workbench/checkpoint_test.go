package workbench

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveOnce 用指定基线保存一次，返回结果。
func saveOnce(t *testing.T, svc *Service, baseVersion int, checkpoint bool) *types.SaveWorkbenchContentResult {
	t.Helper()
	req := validSaveRequest(baseVersion)
	req.Checkpoint = checkpoint
	result, err := svc.SaveContent(newTestContext(), "item-1", req)
	require.NoError(t, err)
	return result
}

// 首个版本必然写检查点：此时还没有任何历史，不写就永远没有可回滚的起点。
func TestCheckpointWrittenOnFirstVersion(t *testing.T) {
	repo := newStubRepo()
	repo.item.SnapshotVersion = 0
	svc := NewService(repo)

	result := saveOnce(t, svc, 0, false)
	assert.True(t, result.Checkpointed, "version=1 必须写检查点")
	require.Len(t, repo.versions, 1)
	assert.Equal(t, result.SnapshotVersion, repo.versions[0].Version)
}

// 距上次检查点不足 10 分钟且未显式要求时，普通保存不写版本行。
func TestCheckpointSkippedWithinInterval(t *testing.T) {
	repo := newStubRepo()
	repo.item.SnapshotVersion = 0
	svc := NewService(repo)

	saveOnce(t, svc, 0, false)                    // version 1，写检查点
	repo.versions[0].CreatedAt = time.Now().UTC() // 刚刚写过

	result := saveOnce(t, svc, 1, false)
	assert.False(t, result.Checkpointed, "10 分钟内不重复写检查点")
	assert.Len(t, repo.versions, 1)
}

// 超过 10 分钟自动补一个检查点。
func TestCheckpointWrittenAfterInterval(t *testing.T) {
	repo := newStubRepo()
	repo.item.SnapshotVersion = 0
	svc := NewService(repo)

	saveOnce(t, svc, 0, false)
	repo.versions[0].CreatedAt = time.Now().UTC().Add(-11 * time.Minute)

	result := saveOnce(t, svc, 1, false)
	assert.True(t, result.Checkpointed, "超过 10 分钟应补检查点")
	assert.Len(t, repo.versions, 2)
}

// 显式 checkpoint=true 时即使刚写过也要写。
func TestCheckpointExplicitAlwaysWrites(t *testing.T) {
	repo := newStubRepo()
	repo.item.SnapshotVersion = 0
	svc := NewService(repo)

	saveOnce(t, svc, 0, false)
	repo.versions[0].CreatedAt = time.Now().UTC()

	result := saveOnce(t, svc, 1, true)
	assert.True(t, result.Checkpointed, "用户点“保存版本”必须写")
	assert.Len(t, repo.versions, 2)
}

// force 覆盖前必须先把旧内容留档，否则旧内容永久丢失。
func TestForceSaveCheckpointsPreviousState(t *testing.T) {
	repo := newStubRepo()
	repo.item.SnapshotVersion = 1
	repo.item.YDoc = []byte("old-doc")
	repo.item.TextContent = "old-text"
	svc := NewService(repo)

	req := validSaveRequest(0) // 过期基线
	req.Force = true
	result, err := svc.SaveContent(newTestContext(), "item-1", req)
	require.NoError(t, err)
	assert.True(t, result.Checkpointed, "force 覆盖必须产生检查点")

	require.Len(t, repo.versions, 1)
	assert.Equal(t, 1, repo.versions[0].Version, "检查点应记录被覆盖的旧版本号")
	assert.Equal(t, []byte("old-doc"), repo.versions[0].YDoc, "检查点必须保存旧内容")

	// 随后 force 时不再重复写同内容的检查点也无所谓，但不能丢失旧字节。
	assert.Equal(t, "hello", repo.item.TextContent)
}
