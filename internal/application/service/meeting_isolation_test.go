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

// isolationRoomRepo 内存房间仓储：GetByID 按 tenant 过滤，ListByCreator 按 tenant+created_by(+status) 过滤；
// Update/Delete 对齐 GORM 语义（Save 直写；软删后记录在默认作用域内不可见，0 行影响不报错）
type isolationRoomRepo struct {
	interfaces.MeetingRoomRepository
	rooms       map[string]*types.MeetingRoom
	updateCount int      // Update 调用次数（幂等断言用）
	deletedIDs  []string // 软删标记（对齐 deleted_at 非空）
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

func (f *isolationRoomRepo) Create(_ context.Context, room *types.MeetingRoom) error {
	f.rooms[room.ID] = room
	return nil
}

func (f *isolationRoomRepo) Update(_ context.Context, room *types.MeetingRoom) error {
	if _, ok := f.rooms[room.ID]; !ok {
		return errors.New("record not found")
	}
	f.rooms[room.ID] = room
	f.updateCount++
	return nil
}

func (f *isolationRoomRepo) Delete(_ context.Context, id string, tenantID uint64) error {
	r, ok := f.rooms[id]
	if !ok || r.TenantID != tenantID {
		return nil // GORM WHERE 未命中 0 行不影响，不报错
	}
	f.deletedIDs = append(f.deletedIDs, id)
	delete(f.rooms, id) // 软删后默认作用域不可见
	return nil
}

// isolationFixture 组装 meetingService：roomRepo 为本组隔离仓储，participantRepo 复用现成 fake
//（GetMeeting 成功路径会查参与者列表），messageRepo 供归档系统消息落库断言；
// 跨租户/归档/删除场景在触达 engine 前即早退，engine 无需注入。
type isolationFixture struct {
	svc   *meetingService
	rooms *isolationRoomRepo
	msgs  *fakeMeetingMessageRepo
}

func newIsolationFixture(rooms ...*types.MeetingRoom) *isolationFixture {
	m := make(map[string]*types.MeetingRoom, len(rooms))
	for _, r := range rooms {
		m[r.ID] = r
	}
	repo := &isolationRoomRepo{rooms: m}
	msgs := &fakeMeetingMessageRepo{}
	return &isolationFixture{
		svc:   &meetingService{roomRepo: repo, participantRepo: &fakeParticipantRepoForTurn{}, messageRepo: msgs},
		rooms: repo,
		msgs:  msgs,
	}
}

// TestGetMeetingCrossTenantNotFound 跨租户取详情必须视同不存在（防水平越权读）
func TestGetMeetingCrossTenantNotFound(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive})

	// 同租户可读
	_, _, err := f.svc.GetMeeting(ctx, 1, "r1")
	require.NoError(t, err)

	// 其他租户与不存在 ID 一律 not found，且不泄漏参与者信息
	_, _, err = f.svc.GetMeeting(ctx, 2, "r1")
	assert.Error(t, err)
	_, _, err = f.svc.GetMeeting(ctx, 1, "ghost")
	assert.Error(t, err)
}

// TestListMeetingsOnlyOwnRooms 列表仅返回本人创建的会议，且 status 过滤生效
func TestListMeetingsOnlyOwnRooms(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(
		&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive},
		&types.MeetingRoom{ID: "r2", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusArchived},
		&types.MeetingRoom{ID: "r3", TenantID: 1, CreatedBy: "user-b", Status: types.MeetingStatusActive},
		&types.MeetingRoom{ID: "r4", TenantID: 2, CreatedBy: "user-a", Status: types.MeetingStatusActive}, // 别的租户同名用户
	)

	// user-a 全量：仅 r1/r2（user-b 的 r3 与租户 2 的 r4 均不可见）
	rooms, total, err := f.svc.ListMeetings(ctx, 1, "user-a", "", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	ids := make([]string, 0, len(rooms))
	for _, r := range rooms {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{"r1", "r2"}, ids)

	// status=active 过滤：仅 r1
	rooms, total, err = f.svc.ListMeetings(ctx, 1, "user-a", types.MeetingStatusActive, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rooms, 1)
	assert.Equal(t, "r1", rooms[0].ID)

	// user-b 全量：仅 r3
	_, total, err = f.svc.ListMeetings(ctx, 1, "user-b", "", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

// TestArchivedRoomRejectsMessage 归档房间拒绝用户发言（归档守卫只读语义）
func TestArchivedRoomRejectsMessage(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusArchived})

	// 归档房间：发言被守卫拦截，返回 ErrRoomArchived（早退于落库与讨论轮触发之前）
	_, err := f.svc.SendUserMessage(ctx, 1, "r1", "user-a", "还能说话吗", nil)
	assert.ErrorIs(t, err, ErrRoomArchived)

	// 跨租户发言：被租户校验拦截（GetByID 早退于归档守卫之前），错误非归档语义
	_, err = f.svc.SendUserMessage(ctx, 2, "r1", "user-a", "越权发言", nil)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrRoomArchived)
}

// TestArchiveRoomIdempotent M6 验收等价（归档只读）：归档落库状态+系统消息；
// 重复归档幂等零副作用（对应接口层再归档返回 200）；跨租户归档视同不存在
func TestArchiveRoomIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive})

	// 首次归档：状态写回 + 一条系统消息落库
	require.NoError(t, f.svc.ArchiveRoom(ctx, 1, "r1"))
	assert.Equal(t, types.MeetingStatusArchived, f.rooms.rooms["r1"].Status)
	stored := f.msgs.snapshot()
	require.Len(t, stored, 1)
	assert.Equal(t, types.MeetingSenderSystem, stored[0].SenderKind)

	// 重复归档：幂等早退，不再 Update、不再落系统消息
	require.NoError(t, f.svc.ArchiveRoom(ctx, 1, "r1"))
	assert.Equal(t, 1, f.rooms.updateCount)
	assert.Len(t, f.msgs.snapshot(), 1)

	// 跨租户归档：GetByID 早退报错
	assert.Error(t, f.svc.ArchiveRoom(ctx, 2, "r1"))
}

// TestDeleteMeetingSoftDelete M6 验收等价（删除）：本人删除成功且详情不可再读（对应 404）；
// 跨租户/重复删除 0 行不影响不报错（对齐 GORM 软删语义）
func TestDeleteMeetingSoftDelete(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(
		&types.MeetingRoom{ID: "r1", TenantID: 1, CreatedBy: "user-a", Status: types.MeetingStatusActive},
		&types.MeetingRoom{ID: "r2", TenantID: 2, CreatedBy: "user-a", Status: types.MeetingStatusActive}, // 别的租户
	)

	// 本人删除：成功，列表/详情均不可见
	require.NoError(t, f.svc.DeleteMeeting(ctx, 1, "r1"))
	assert.Equal(t, []string{"r1"}, f.rooms.deletedIDs) // 软删标记而非物理移除
	_, _, err := f.svc.GetMeeting(ctx, 1, "r1")
	assert.Error(t, err)

	// 跨租户删除：WHERE 未命中，不报错但不触碰数据
	assert.NoError(t, f.svc.DeleteMeeting(ctx, 1, "r2"))
	assert.Equal(t, []string{"r1"}, f.rooms.deletedIDs)

	// 重复删除：同样 0 行不影响
	assert.NoError(t, f.svc.DeleteMeeting(ctx, 1, "r1"))
	assert.Equal(t, []string{"r1"}, f.rooms.deletedIDs)
}
