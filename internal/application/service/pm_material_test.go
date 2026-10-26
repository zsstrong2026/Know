package service

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeMaterialRepo 以内存 map 模拟 pm_materials，按类型过滤与租户校验对齐仓储实现
type fakeMaterialRepo struct {
	items map[string]*types.PMMaterial
}

func newFakeMaterialRepo(items ...*types.PMMaterial) *fakeMaterialRepo {
	r := &fakeMaterialRepo{items: make(map[string]*types.PMMaterial, len(items))}
	for _, m := range items {
		r.items[m.ID] = m
	}
	return r
}

func (r *fakeMaterialRepo) Create(_ context.Context, m *types.PMMaterial) error {
	r.items[m.ID] = m
	return nil
}

func (r *fakeMaterialRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.PMMaterial, error) {
	m, ok := r.items[id]
	if !ok || m.TenantID != tenantID {
		return nil, repository.ErrPMMaterialNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeMaterialRepo) List(
	_ context.Context, tenantID uint64, projectID string, materialType string,
) ([]*types.PMMaterial, error) {
	var list []*types.PMMaterial
	for _, m := range r.items {
		if m.TenantID != tenantID || m.ProjectID != projectID {
			continue
		}
		if materialType != "" && m.MaterialType != materialType {
			continue
		}
		list = append(list, m)
	}
	return list, nil
}

func (r *fakeMaterialRepo) Update(_ context.Context, m *types.PMMaterial) error {
	if _, ok := r.items[m.ID]; !ok {
		return repository.ErrPMMaterialNotFound
	}
	cp := *m
	r.items[m.ID] = &cp
	return nil
}

func (r *fakeMaterialRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	m, ok := r.items[id]
	if !ok || m.TenantID != tenantID {
		return repository.ErrPMMaterialNotFound
	}
	delete(r.items, id)
	return nil
}

// fakeMaterialKnowledge 记录发布落库的知识库调用，验证通道与正文
type fakeMaterialKnowledge struct {
	kbID     string
	passages []string
	channels []string
	err      error
}

func (f *fakeMaterialKnowledge) CreateKnowledgeFromPassageSync(
	_ context.Context, kbID string, passage []string, channel string,
) (*types.Knowledge, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.kbID = kbID
	f.passages = append(f.passages, passage...)
	f.channels = append(f.channels, channel)
	return &types.Knowledge{ID: "k-" + strconv.Itoa(len(f.passages))}, nil
}

// fakeTaskEnqueuer 记录投递的 asynq 任务，验证生成走异步而非同步渲染
type fakeTaskEnqueuer struct {
	tasks []*asynq.Task
}

var _ interfaces.TaskEnqueuer = (*fakeTaskEnqueuer)(nil)

func (e *fakeTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{ID: task.Type()}, nil
}

// 发布：draft → published，旧版自动 archived（不删除，保留可追溯），正文落库知识库
func TestPublishMaterialArchivesPrevious(t *testing.T) {
	old := &types.PMMaterial{
		ID: "m-old", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-1", Title: "操作手册", Status: types.MaterialStatusPublished,
	}
	cur := &types.PMMaterial{
		ID: "m-cur", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-2", Title: "操作手册", Status: types.MaterialStatusDraft,
		Content: "# 操作手册\n\n1. 登录系统",
	}
	// 其他类型的已发布资料不受影响
	other := &types.PMMaterial{
		ID: "m-ppt", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypePPT,
		VersionID: "v-1", Title: "产品介绍", Status: types.MaterialStatusPublished,
	}
	repo := newFakeMaterialRepo(old, cur, other)
	knowledge := &fakeMaterialKnowledge{}
	projects := &fakePMProjectRepo{items: []*types.PMProject{
		{ID: "p-1", TenantID: 1, KBID: "kb-1"},
	}}
	svc := NewPMMaterialService(PMMaterialDeps{
		Repo: repo, ProjectRepo: projects, Knowledge: knowledge,
	})
	ctx := context.Background()

	got, err := svc.Publish(ctx, 1, "m-cur")
	require.NoError(t, err)
	assert.Equal(t, types.MaterialStatusPublished, got.Status)
	assert.False(t, got.Archived)

	assert.True(t, repo.items["m-old"].Archived, "同类型旧发布版应归档")
	assert.False(t, repo.items["m-old"].DeletedAt.Valid, "归档不删除，保留可追溯")
	assert.False(t, repo.items["m-ppt"].Archived, "其他类型不受影响")

	assert.Equal(t, "kb-1", knowledge.kbID, "落库到项目关联知识库")
	assert.Equal(t, pmMaterialChannel, knowledge.channels[0])
	assert.Contains(t, knowledge.passages[0], "MAT-操作手册", "标题按资料目录前缀约定")
	assert.Contains(t, knowledge.passages[0], "登录系统", "正文必须落库")
	assert.Equal(t, "k-1", got.KnowledgeID, "回填知识库 ID 供追溯")
}

// 发布前置校验：正文为空 / 知识库未装配时给出明确错误
func TestPublishMaterialRejectsEmptyContent(t *testing.T) {
	repo := newFakeMaterialRepo(&types.PMMaterial{
		ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-1", Title: "操作手册", Status: types.MaterialStatusDraft,
	})
	svc := NewPMMaterialService(PMMaterialDeps{Repo: repo})
	_, err := svc.Publish(context.Background(), 1, "m-1")
	assert.ErrorIs(t, err, ErrMaterialContentEmpty)

	req := &types.PMMaterial{
		ID: "m-2", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-1", Title: "操作手册", Status: types.MaterialStatusDraft, Content: "正文",
	}
	svc2 := NewPMMaterialService(PMMaterialDeps{Repo: newFakeMaterialRepo(req)})
	_, err = svc2.Publish(context.Background(), 1, "m-2")
	assert.ErrorIs(t, err, ErrMaterialKnowledgeUnready)
}

// 生成回写：仅草稿接受正文，已发布记录不被覆盖
func TestSaveGeneratedContentOnlyDraft(t *testing.T) {
	draft := &types.PMMaterial{
		ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-1", Title: "操作手册", Status: types.MaterialStatusDraft,
	}
	published := &types.PMMaterial{
		ID: "m-2", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
		VersionID: "v-1", Title: "已发布手册", Status: types.MaterialStatusPublished,
		Content: "线上正文",
	}
	repo := newFakeMaterialRepo(draft, published)
	svc := NewPMMaterialService(PMMaterialDeps{Repo: repo})
	ctx := context.Background()

	require.NoError(t, svc.SaveGeneratedContent(ctx, 1, "m-1", "# 操作手册\n\n新正文"))
	assert.Contains(t, repo.items["m-1"].Content, "新正文")

	require.NoError(t, svc.SaveGeneratedContent(ctx, 1, "m-2", "覆盖内容"))
	assert.Equal(t, "线上正文", repo.items["m-2"].Content, "已发布资料不被生成任务覆盖")

	assert.ErrorIs(t, svc.SaveGeneratedContent(ctx, 9, "m-1", "x"), repository.ErrPMMaterialNotFound)
}

// 删除策略：草稿可删，已发布/已归档拒绝
func TestDeleteMaterialOnlyDraft(t *testing.T) {
	repo := newFakeMaterialRepo(
		&types.PMMaterial{
			ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
			Status: types.MaterialStatusDraft,
		},
		&types.PMMaterial{
			ID: "m-2", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
			Status: types.MaterialStatusPublished,
		},
	)
	svc := NewPMMaterialService(PMMaterialDeps{Repo: repo})
	ctx := context.Background()

	require.NoError(t, svc.DeleteMaterial(ctx, 1, "m-1"))
	assert.NotContains(t, repo.items, "m-1")

	assert.ErrorIs(t, svc.DeleteMaterial(ctx, 1, "m-2"), ErrMaterialReadOnly)
	assert.Contains(t, repo.items, "m-2")
}

// 下载导出：手册 → docx，PPT → pptx，正文缺失拒绝导出
func TestDownloadMaterialByType(t *testing.T) {
	repo := newFakeMaterialRepo(
		&types.PMMaterial{
			ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
			Title: "操作手册", Content: "# 操作手册\n\n## 登录\n\n1. 输入账号",
		},
		&types.PMMaterial{
			ID: "m-2", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypePPT,
			Title: "产品介绍", Content: "# 产品介绍\n\n## 亮点\n\n- 快",
		},
		&types.PMMaterial{
			ID: "m-3", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
			Title: "空资料",
		},
	)
	svc := NewPMMaterialService(PMMaterialDeps{Repo: repo})
	ctx := context.Background()

	docx, err := svc.DownloadMaterial(ctx, 1, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "操作手册.docx", docx.FileName)
	assert.Contains(t, docx.ContentType, "wordprocessingml")
	assert.True(t, strings.HasPrefix(string(docx.Data), "PK"), "docx 应为 zip 包")

	pptx, err := svc.DownloadMaterial(ctx, 1, "m-2")
	require.NoError(t, err)
	assert.Equal(t, "产品介绍.pptx", pptx.FileName)
	assert.Contains(t, pptx.ContentType, "presentationml")

	_, err = svc.DownloadMaterial(ctx, 1, "m-3")
	assert.ErrorIs(t, err, ErrMaterialContentEmpty)
}

// 更新提示：最新版本号 > 资料关联版本号时，列表返回 needs_update=true
func TestMaterialNeedsUpdate(t *testing.T) {
	versions := newFakeVersionRepo(
		&types.PMVersion{ID: "v-1", TenantID: 1, ProjectID: "p-1", VersionNo: "V1.0"},
		&types.PMVersion{ID: "v-2", TenantID: 1, ProjectID: "p-1", VersionNo: "V1.1"},
	)
	repo := newFakeMaterialRepo(
		&types.PMMaterial{
			ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeManual,
			VersionID: "v-1", Title: "操作手册", Status: types.MaterialStatusPublished,
		},
		&types.PMMaterial{
			ID: "m-2", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypePPT,
			VersionID: "v-2", Title: "产品介绍", Status: types.MaterialStatusPublished,
		},
	)
	svc := NewPMMaterialService(PMMaterialDeps{Repo: repo, VersionRepo: versions})
	ctx := context.Background()

	list, err := svc.ListMaterials(ctx, 1, "p-1", "")
	require.NoError(t, err)
	require.Len(t, list, 2)

	byID := make(map[string]*types.PMMaterial, len(list))
	for _, m := range list {
		byID[m.ID] = m
	}
	assert.True(t, byID["m-1"].NeedsUpdate, "关联旧版本应提示有新版本")
	assert.Equal(t, "V1.0", byID["m-1"].VersionNo)
	assert.Equal(t, "V1.1", byID["m-1"].LatestVersionNo)

	assert.False(t, byID["m-2"].NeedsUpdate, "关联最新版本无需更新")
	assert.Equal(t, "V1.1", byID["m-2"].VersionNo)
}

// 生成走异步任务，不得同步阻塞请求
func TestGenerateReturnsTask(t *testing.T) {
	repo := newFakeMaterialRepo(&types.PMMaterial{
		ID: "m-1", TenantID: 1, ProjectID: "p-1", MaterialType: types.MaterialTypeBrochure,
		VersionID: "v-1", Title: "产品宣传册", Status: types.MaterialStatusDraft,
	})
	taskRepo := newFakeTaskRepo()
	enqueuer := &fakeTaskEnqueuer{}
	svc := NewPMMaterialService(PMMaterialDeps{
		Repo:        repo,
		TaskRepo:    taskRepo,
		Enqueuer:    enqueuer,
		VersionRepo: newFakeVersionRepo(),
	})
	ctx := context.Background()

	task, err := svc.GenerateMaterial(ctx, 1, "m-1")
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.NotEmpty(t, task.ID, "调用后应立即返回 task_id")
	assert.Equal(t, types.PMTaskStatusPending, task.Status, "任务尚未执行即返回")
	assert.Equal(t, types.TypePMMaterialGenerate, task.TaskType)
	assert.Equal(t, types.MaterialStatusDraft, repo.items["m-1"].Status, "生成不改变资料状态")

	require.Len(t, enqueuer.tasks, 1, "生成必须投递异步任务")
	assert.Equal(t, types.TypePMMaterialGenerate, enqueuer.tasks[0].Type())
}
