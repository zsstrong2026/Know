// run.go 是运行（run）的业务层：从已发布模板创建运行、统计摘要。
//
// 两条口径：
//   - 运行只认冻结快照：创建时把模板的 published revision 深拷贝进
//     run.workflow_snapshot，之后改草稿不影响已在跑/已跑完的运行；
//   - 输入按模板声明的 InputSchema 校验：缺必填键在创建时就拒绝，别让运行跑到
//     一半才因为没拿到输入而失败。
package sop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 运行创建相关的业务错误。
var (
	ErrSopTemplateUnpublished = errors.New("该模板尚未发布，不能发起运行")
	ErrSopRunInputMissing     = errors.New("运行输入缺少必填项")
	// ErrSopRunDefinitionInvalid 发布快照里的流程定义不合法（未知节点/校验类型等），
	// 运行前就拒绝，避免执行到一半才在引擎里炸。
	ErrSopRunDefinitionInvalid = errors.New("流程定义不合法")
)

// CreateRunInput 创建运行的入参。
type CreateRunInput struct {
	TemplateID string
	ItemID     string
	Input      map[string]any
	// TriggerKind 空=manual；schedule/api/child_run 由 T35 等调用方显式给。
	TriggerKind  string
	TriggerRefID string
}

// RunService 运行业务：创建（快照发布 revision）与统计。
type RunService struct {
	templates interfaces.SopTemplateRepository
	runs      interfaces.SopRunRepository
	projects  interfaces.SopProjectRepository
	items     interfaces.SopItemRepository
}

// NewRunService 构造运行服务。
func NewRunService(
	templates interfaces.SopTemplateRepository, runs interfaces.SopRunRepository,
	projects interfaces.SopProjectRepository, items interfaces.SopItemRepository,
) *RunService {
	return &RunService{templates: templates, runs: runs, projects: projects, items: items}
}

// Create 从已发布模板创建运行（状态 queued，等调度器认领）。
func (s *RunService) Create(
	ctx context.Context, tenantID uint64, userID string, in CreateRunInput,
) (*types.SopRun, error) {
	tplID := strings.TrimSpace(in.TemplateID)
	if tplID == "" {
		return nil, fmt.Errorf("请选择流程模板")
	}
	tpl, err := s.templates.GetByID(ctx, tenantID, tplID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tpl.PublishedRevisionID) == "" ||
		tpl.Status != types.SopTemplateStatusPublished {
		return nil, ErrSopTemplateUnpublished
	}
	rev, err := s.templates.GetRevision(ctx, tenantID, tpl.PublishedRevisionID)
	if err != nil {
		return nil, fmt.Errorf("读取发布版本失败：%w", err)
	}
	def, err := cloneDefinition(rev.Definition)
	if err != nil {
		return nil, err
	}
	// 发布快照可能来自旧版本/手改库：解析期就校验结构与类型，不要等到执行器里
	// 才因为未知类型报内部错误。
	if errs := engine.ValidateDefinition(&def, false); len(errs) > 0 {
		return nil, fmt.Errorf("%w：%s", ErrSopRunDefinitionInvalid, strings.Join(errs, "；"))
	}
	if err := validateRunInput(def.InputSchema, in.Input); err != nil {
		return nil, err
	}

	run := &types.SopRun{
		TenantID:         tenantID,
		TemplateID:       tpl.ID,
		RevisionID:       rev.ID,
		WorkflowSnapshot: def,
		ProjectID:        tpl.ProjectID,
		ItemID:           strings.TrimSpace(in.ItemID),
		TriggerKind:      firstNonEmptyStr(in.TriggerKind, types.SopRunTriggerManual),
		TriggerRefID:     in.TriggerRefID,
		Status:           types.SopRunQueued,
		CreatedBy:        userID,
	}
	if in.Input != nil {
		run.Input = in.Input
	}
	// 事项可选：指定时校验归属，并抄下项目的 KB 关联便于执行（Resolver 也查，
	// 这里只是提前在创建期拦住「拿别人事项 id 发起运行」这类错误）。
	if run.ItemID != "" {
		item, err := s.items.GetByID(ctx, tenantID, run.ItemID)
		if err != nil {
			return nil, fmt.Errorf("事项不存在或无权访问")
		}
		if run.ProjectID == "" {
			run.ProjectID = item.ProjectID
		} else if item.ProjectID != run.ProjectID {
			return nil, fmt.Errorf("该事项不属于模板所在项目")
		}
	}
	if err := s.runs.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

// validateRunInput 校验运行输入：InputSchema 里的每个键都必须拿到非空值。
func validateRunInput(schema []string, input map[string]any) error {
	var missing []string
	for _, key := range schema {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		v, ok := input[k]
		if !ok || v == nil {
			missing = append(missing, k)
			continue
		}
		if text, isStr := v.(string); isStr && strings.TrimSpace(text) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w：%s", ErrSopRunInputMissing, strings.Join(missing, "、"))
	}
	return nil
}

// Get 取运行详情。
func (s *RunService) Get(ctx context.Context, tenantID uint64, id string) (*types.SopRun, error) {
	return s.runs.GetRun(ctx, tenantID, id)
}

// List 列运行（过滤 + 分页）。
func (s *RunService) List(
	ctx context.Context, tenantID uint64, f types.SopRunFilter, page, pageSize int,
) ([]types.SopRun, error) {
	list, err := s.runs.ListRuns(ctx, tenantID, f, page, pageSize)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []types.SopRun{}
	}
	return list, nil
}

// Stats 统计摘要（projectID 空=全租户）。
func (s *RunService) Stats(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.SopRunStats, error) {
	return s.runs.StatsSummary(ctx, tenantID, projectID)
}

// cloneDefinition 用 JSON 往返做定义深拷贝（与仓储同口径，避免共享底层切片）。
func cloneDefinition(src types.SopDefinition) (types.SopDefinition, error) {
	raw, err := json.Marshal(src)
	if err != nil {
		return types.SopDefinition{}, fmt.Errorf("复制流程定义失败：%w", err)
	}
	var out types.SopDefinition
	if err := json.Unmarshal(raw, &out); err != nil {
		return types.SopDefinition{}, fmt.Errorf("复制流程定义失败：%w", err)
	}
	types.EnsureSopDefinitionSlices(&out)
	return out, nil
}
