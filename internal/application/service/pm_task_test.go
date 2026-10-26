package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// fakeTaskRepo 以内存 map 模拟 pm_tasks，记录写状态调用次数以验证幂等与失败路径
type fakeTaskRepo struct {
	tasks             map[string]*types.PMTask
	updateStatusCalls int
	succeedCalls      int
	failCalls         int
}

func newFakeTaskRepo(tasks ...*types.PMTask) *fakeTaskRepo {
	r := &fakeTaskRepo{tasks: make(map[string]*types.PMTask, len(tasks))}
	for _, t := range tasks {
		r.tasks[t.ID] = t
	}
	return r
}

func (r *fakeTaskRepo) Create(_ context.Context, t *types.PMTask) error {
	r.tasks[t.ID] = t
	return nil
}

// GetByID 同时校验租户，跨租户等价于不存在
func (r *fakeTaskRepo) GetByID(_ context.Context, tenantID uint64, id string) (*types.PMTask, error) {
	t, ok := r.tasks[id]
	if !ok || t.TenantID != tenantID {
		return nil, repository.ErrPMTaskNotFound
	}
	return t, nil
}

func (r *fakeTaskRepo) UpdateStatus(_ context.Context, id string, status string, progress int) error {
	t, ok := r.tasks[id]
	if !ok {
		return repository.ErrPMTaskNotFound
	}
	r.updateStatusCalls++
	t.Status, t.Progress = status, progress
	return nil
}

func (r *fakeTaskRepo) Succeed(_ context.Context, id string, result types.JSON) error {
	t, ok := r.tasks[id]
	if !ok {
		return repository.ErrPMTaskNotFound
	}
	r.succeedCalls++
	t.Status, t.Progress, t.Result = types.PMTaskStatusSucceeded, 100, result
	return nil
}

func (r *fakeTaskRepo) Fail(_ context.Context, id string, message string) error {
	t, ok := r.tasks[id]
	if !ok {
		return repository.ErrPMTaskNotFound
	}
	r.failCalls++
	t.Status, t.ErrorMessage = types.PMTaskStatusFailed, message
	return nil
}

// fakePolisher 模拟可选 LLM 润色，可注入失败以验证降级
type fakePolisher struct {
	out string
	err error
}

func (p fakePolisher) Polish(_ context.Context, _ string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return p.out, nil
}

func newPMTaskPayload(t *testing.T, p types.PMPayload) []byte {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func newPMAsynqTask(t *testing.T, taskType string, p types.PMPayload) *asynq.Task {
	t.Helper()
	return asynq.NewTask(taskType, newPMTaskPayload(t, p))
}

// 任务处理：反序列化 payload → 置 running → 执行 → 置 succeeded，并写 result
func TestProcessPMTaskSucceeds(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-1",
		TenantID: 7,
		TaskType: types.TypePMPRDGenerate,
		Status:   types.PMTaskStatusPending,
	}
	repo := newFakeTaskRepo(task)
	svc := NewPMTaskService(repo, nil, nil)

	payload := types.PMPayload{
		TenantID: 7,
		TaskID:   "task-1",
		Title:    "v1.0.0 版本 PRD",
		Sections: []types.PMPayloadSection{{Heading: "背景", Body: "替换旧版审批流"}},
	}
	if err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMPRDGenerate, payload)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if task.Status != types.PMTaskStatusSucceeded || task.Progress != 100 {
		t.Fatalf("want succeeded/100, got %s/%d", task.Status, task.Progress)
	}
	if repo.updateStatusCalls != 1 {
		t.Fatalf("want one running update, got %d", repo.updateStatusCalls)
	}

	var result map[string]any
	if err := json.Unmarshal(task.Result, &result); err != nil {
		t.Fatalf("result is not json: %v", err)
	}
	content, _ := result["content"].(string)
	if !strings.Contains(content, "v1.0.0 版本 PRD") || !strings.Contains(content, "替换旧版审批流") {
		t.Fatalf("template content missing title or section: %q", content)
	}
}

// 终态幂等：已 succeeded 的任务重复消费直接返回 nil，不重复落库
func TestProcessPMTaskIdempotentOnSucceeded(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-2",
		TenantID: 7,
		TaskType: types.TypePMPRDGenerate,
		Status:   types.PMTaskStatusSucceeded,
	}
	repo := newFakeTaskRepo(task)
	svc := NewPMTaskService(repo, nil, nil)

	payload := types.PMPayload{TenantID: 7, TaskID: "task-2", Title: "已完成的 PRD"}
	if err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMPRDGenerate, payload)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if repo.updateStatusCalls != 0 || repo.succeedCalls != 0 {
		t.Fatalf("terminal task should not be rewritten, got running=%d succeed=%d",
			repo.updateStatusCalls, repo.succeedCalls)
	}
}

// 失败：写 error_message 与 finished_at，进度停在失败点，不抛 panic
func TestProcessPMTaskFailureRecordsError(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-3",
		TenantID: 7,
		TaskType: types.TypePMPRDGenerate,
		Status:   types.PMTaskStatusPending,
	}
	repo := newFakeTaskRepo(task)
	svc := NewPMTaskService(repo, nil, nil)

	// 标题缺失视为输入不合法，渲染直接失败
	payload := types.PMPayload{TenantID: 7, TaskID: "task-3"}
	err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMPRDGenerate, payload))
	if !errors.Is(err, ErrPMTaskTitleRequired) {
		t.Fatalf("want ErrPMTaskTitleRequired, got %v", err)
	}
	if task.Status != types.PMTaskStatusFailed || task.ErrorMessage == "" {
		t.Fatalf("want failed with message, got %s/%q", task.Status, task.ErrorMessage)
	}
	// 失败点必须已有进度，便于前端展示「卡在第 N 步」
	if task.Progress == 0 {
		t.Fatalf("progress should keep failure point, got 0")
	}
	if repo.failCalls != 1 || repo.succeedCalls != 0 {
		t.Fatalf("want one fail and no succeed, got fail=%d succeed=%d", repo.failCalls, repo.succeedCalls)
	}
}

// 进度轮询：按 task_id + tenant_id 查询，跨租户返回 NotFound
func TestGetTaskCrossTenantNotFound(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-4",
		TenantID: 7,
		TaskType: types.TypePMPRDGenerate,
		Status:   types.PMTaskStatusRunning,
	}
	svc := NewPMTaskService(newFakeTaskRepo(task), nil, nil)

	if _, err := svc.GetTask(context.Background(), 7, "task-4"); err != nil {
		t.Fatalf("owner tenant should read task: %v", err)
	}
	if _, err := svc.GetTask(context.Background(), 8, "task-4"); !errors.Is(err, repository.ErrPMTaskNotFound) {
		t.Fatalf("cross tenant want ErrPMTaskNotFound, got %v", err)
	}
}

// LLM 润色失败时必须降级为模板结果，任务仍 succeeded
func TestProcessPMTaskFallsBackWhenPolisherFails(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-5",
		TenantID: 7,
		TaskType: types.TypePMMaterialGenerate,
		Status:   types.PMTaskStatusPending,
	}
	repo := newFakeTaskRepo(task)
	svc := NewPMTaskService(repo, fakePolisher{err: errors.New("model unavailable")}, nil)

	payload := types.PMPayload{
		TenantID: 7,
		TaskID:   "task-5",
		Title:    "项目周报草稿",
		Sections: []types.PMPayloadSection{{Heading: "进展", Body: "完成版本评审"}},
	}
	if err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMMaterialGenerate, payload)); err != nil {
		t.Fatalf("polish failure must not fail task: %v", err)
	}
	if task.Status != types.PMTaskStatusSucceeded {
		t.Fatalf("want succeeded, got %s", task.Status)
	}

	var result map[string]any
	if err := json.Unmarshal(task.Result, &result); err != nil {
		t.Fatalf("result is not json: %v", err)
	}
	if polished, _ := result["polished"].(bool); polished {
		t.Fatalf("fallback result should mark polished=false")
	}
	content, _ := result["content"].(string)
	if !strings.Contains(content, "完成版本评审") {
		t.Fatalf("want template content, got %q", content)
	}
}

// fakeMaterialSink 记录资料正文回写调用，可模拟回写失败
type fakeMaterialSink struct {
	tenantID uint64
	id       string
	content  string
	calls    int
	err      error
}

func (f *fakeMaterialSink) SaveGeneratedContent(
	_ context.Context, tenantID uint64, id string, content string,
) error {
	f.calls++
	f.tenantID, f.id, f.content = tenantID, id, content
	return f.err
}

// 资料生成成功后应把正文回写到目标资料，且任务本身仍 succeeded
func TestProcessPMTaskPersistsGeneratedContent(t *testing.T) {
	task := &types.PMTask{
		ID:       "task-6",
		TenantID: 7,
		TaskType: types.TypePMMaterialGenerate,
		Status:   types.PMTaskStatusPending,
	}
	sink := &fakeMaterialSink{}
	svc := NewPMTaskService(newFakeTaskRepo(task), nil, sink)

	payload := types.PMPayload{
		TenantID: 7,
		TaskID:   "task-6",
		TargetID: "m-1",
		Title:    "操作手册",
		Sections: []types.PMPayloadSection{{Heading: "登录", Body: "输入账号密码"}},
	}
	if err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMMaterialGenerate, payload)); err != nil {
		t.Fatalf("process task: %v", err)
	}
	if task.Status != types.PMTaskStatusSucceeded {
		t.Fatalf("want succeeded, got %s", task.Status)
	}
	if sink.calls != 1 {
		t.Fatalf("want 1 sink call, got %d", sink.calls)
	}
	if sink.tenantID != 7 || sink.id != "m-1" {
		t.Fatalf("unexpected sink target: tenant=%d id=%s", sink.tenantID, sink.id)
	}
	if !strings.Contains(sink.content, "输入账号密码") {
		t.Fatalf("sink should receive generated body, got %q", sink.content)
	}
}

// 回写失败不回滚任务；非资料任务不触发回写
func TestProcessPMTaskSinkFailureAndSkip(t *testing.T) {
	materialTask := &types.PMTask{
		ID:       "task-7",
		TenantID: 7,
		TaskType: types.TypePMMaterialGenerate,
		Status:   types.PMTaskStatusPending,
	}
	sink := &fakeMaterialSink{err: errors.New("db down")}
	svc := NewPMTaskService(newFakeTaskRepo(materialTask), nil, sink)
	payload := types.PMPayload{TenantID: 7, TaskID: "task-7", TargetID: "m-1", Title: "操作手册"}
	if err := svc.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMMaterialGenerate, payload)); err != nil {
		t.Fatalf("sink failure must not fail task: %v", err)
	}
	if materialTask.Status != types.PMTaskStatusSucceeded {
		t.Fatalf("want succeeded, got %s", materialTask.Status)
	}

	prdTask := &types.PMTask{
		ID:       "task-8",
		TenantID: 7,
		TaskType: types.TypePMPRDGenerate,
		Status:   types.PMTaskStatusPending,
	}
	sink2 := &fakeMaterialSink{}
	svc2 := NewPMTaskService(newFakeTaskRepo(prdTask), nil, sink2)
	prdPayload := types.PMPayload{TenantID: 7, TaskID: "task-8", TargetID: "m-1", Title: "需求文档"}
	if err := svc2.ProcessTask(context.Background(), newPMAsynqTask(t, types.TypePMPRDGenerate, prdPayload)); err != nil {
		t.Fatalf("process prd task: %v", err)
	}
	if sink2.calls != 0 {
		t.Fatalf("non-material task should not persist content, got %d calls", sink2.calls)
	}
}
