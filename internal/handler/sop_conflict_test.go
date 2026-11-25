package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubSopProjectRepo 让「预检通过但落库撞唯一索引」可复现：
// 并发场景下先查后写之间没有约束兜底，驱动错误必须被翻译成 400。
type stubSopProjectRepo struct {
	interfaces.SopProjectRepository
	one       *types.SopProject
	others    []types.SopProject
	createErr error
	updateErr error
}

func (s *stubSopProjectRepo) GetByID(
	_ context.Context, _ uint64, _ string,
) (*types.SopProject, error) {
	return s.one, nil
}

func (s *stubSopProjectRepo) ListByTenant(_ context.Context, _ uint64) ([]types.SopProject, error) {
	return s.others, nil
}

func (s *stubSopProjectRepo) Create(_ context.Context, _ *types.SopProject) error {
	return s.createErr
}

func (s *stubSopProjectRepo) Update(_ context.Context, _ *types.SopProject) error {
	return s.updateErr
}

// 并发建项目撞唯一索引：必须 400 +「已被占用」，不能落成 500 + 英文内部错误。
func TestProjectConflictMapsTo400OnCreate(t *testing.T) {
	repo := &stubSopProjectRepo{createErr: errors.New(`UNIQUE constraint failed: sop_projects.code`)}
	r := newSOPTestRouterWith(t, NewSOPHandler(SOPHandlerDeps{Projects: repo}))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"name":"并发项目","code":"CC"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "已被占用")
}

// 并发改名撞唯一索引：此时对方的行已经提交，复述冲突应直接点出「已存在」。
func TestProjectConflictMapsTo400OnUpdate(t *testing.T) {
	repo := &stubSopProjectRepo{
		one:       &types.SopProject{ID: "p1", TenantID: 1, Name: "原名", Code: "CC"},
		others:    []types.SopProject{{ID: "p2", TenantID: 1, Name: "撞名", Code: "DD"}},
		updateErr: errors.New(`ERROR: duplicate key value violates unique constraint "ux_sop_projects_tenant_name"`),
	}
	r := newSOPTestRouterWith(t, NewSOPHandler(SOPHandlerDeps{Projects: repo}))

	w := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/p1", `{"name":"撞名"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "已存在")
}

// 非唯一约束错误仍走 500：翻译只针对并发冲突，不能把内部错误放大成 400。
func TestProjectNonConflictErrorStays500(t *testing.T) {
	repo := &stubSopProjectRepo{createErr: errors.New("connection refused")}
	r := newSOPTestRouterWith(t, NewSOPHandler(SOPHandlerDeps{Projects: repo}))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"name":"内部故障","code":"IE"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
}
