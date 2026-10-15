package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- M6 权限隔离验收测试（T6.4；沿用 meeting_solution_test.go 的嵌入接口风格）----
// fake 仓储严格按真实仓储的 WHERE 语义过滤 tenant_id/created_by（跨租户/他人视同不存在）。

// isolationRoomRepo 内存房间仓储：GetByID 按 tenant 过滤，ListByCreator 按 tenant+created_by(+status) 过滤
type isolationRoomRepo struct {
	interfaces.MeetingRoomRepository
	rooms map[string]*types.MeetingRoom
}

func (f *isolationRoomRepo) GetByID(_ context.Context, id string, tenantID uint64) (*types.MeetingRoom, error) {
	r, ok := f.rooms[id]
	if !ok || r.TenantID != tenantID { // 跨租户视同不存在
		return nil, errors.New("record not found")
	}
	return r, nil
}

func (f *isolationRoomRepo) ListByCreator(_ context.Context, tenantID uint64, createdBy, status string, _, _ int) ([]*types.MeetingRoom, int64, error) {
	var out []*types.MeetingRoom
	for _, r := range f.rooms {
		if r.TenantID != tenantID || r.CreatedBy != createdBy {
			continue // 仅创建者可见
		}
		if status != "" && r.Status != status {
			continue
		}
		out = append(out, r)
	}
	return out, int64(len(out)), nil
}

// isolationFixture 组装 meetingService：roomRepo 为本组隔离仓储，participantRepo 复用现成 fake
//（GetMeeting 成功路径会查参与者列表）；跨租户/归档场景在触达 messageRepo/engine 前即早退，二者无需注入。
func isolationFixture(rooms ...*types.MeetingRoom) (*meetingService, *isolationRoomRepo) {
	m := make(map[string]*types.MeetingRoom, len(rooms))
	for _, r := range rooms {
		m[r.ID] = r
	}
	repo := &isolationRoomRepo{rooms: m}
	return &meetingService{roomRepo: repo, participantRepo: &fakeParticipantRepoForTurn{}}, repo
}

// TestGetMeetingCrossTenantNotFound 跨租户取详情必须视同不存在（防水平越权读）
func TestGetMeetingCrossTenantNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := isolationFixture(&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive})

	// 同租户可读
	_, _, err := svc.GetMeeting(ctx, 1, "r1")
	require.NoError(t, err)

	// 其他租户与不存在 ID 一律 not found，且不泄漏参与者信息
	_, _, err = svc.GetMeeting(ctx, 2, "r1")
	assert.Error(t, err)
	_, _, err = svc.GetMeeting(ctx, 1, "ghost")
	assert.Error(t, err)
}

// TestListMeetingsOnlyOwnRooms 列表仅返回本人创建的会议，且 status 过滤生效
func TestListMeetingsOnlyOwnRooms(t *testing.T) {
	ctx := context.Background()
	svc, _ := isolationFixture(
		&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive},
		&types.MeetingRoom{ID: "r2", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusArchived},
		&types.MeetingRoom{ID: "r3", TenantID: 1, CreatedBy: "user-b", Status: types.MeetingStatusActive},
		&types.MeetingRoom{ID: "r4", TenantID: 2, CreatedBy: "user-a", Status: types.MeetingStatusActive}, // 别的租户同名用户
	)

	// user-a 全量：仅 r1/r2（user-b 的 r3 与租户 2 的 r4 均不可见）
	rooms, total, err := svc.ListMeetings(ctx, 1, "user-a", "", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	ids := make([]string, 0, len(rooms))
	for _, r := range rooms {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{"r1", "r2"}, ids)

	// status=active 过滤：仅 r1
	rooms, total, err = svc.ListMeetings(ctx, 1, "user-a", types.MeetingStatusActive, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rooms, 1)
	assert.Equal(t, "r1", rooms[0].ID)

	// user-b 全量：仅 r3
	_, total, err = svc.ListMeetings(ctx, 1, "user-b", "", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

// TestArchivedRoomRejectsMessage 归档房间拒绝用户发言（归档守卫只读语义）
func TestArchivedRoomRejectsMessage(t *testing.T) {
	ctx := context.Background()
	svc, _ := isolationFixture(&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusArchived})

	// 归档房间：发言被守卫拦截，返回 ErrRoomArchived（早退于落库与讨论轮触发之前）
	_, err := svc.SendUserMessage(ctx, 1, "r1", "user-a", "还能说话吗", nil)
	assert.ErrorIs(t, err, ErrRoomArchived)

	// 跨租户发言：被租户校验拦截（GetByID 早退于归档守卫之前），错误非归档语义
	_, err = svc.SendUserMessage(ctx, 2, "r1", "user-a", "越权发言", nil)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrRoomArchived)
}
