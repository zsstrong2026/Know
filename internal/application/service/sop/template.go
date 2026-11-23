package sop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrSopTemplateReadonly 内置模板只读（修改路径是 Copy 出独立草稿）。
var ErrSopTemplateReadonly = errors.New("内置模板只读，请先创建副本再改")

// ErrSopTemplateInvalid 发布校验未通过（逐条白话错误拼在消息里）。
var ErrSopTemplateInvalid = errors.New("流程校验未通过")

// ErrSopTemplateNameRequired 模板名称不能为空。
var ErrSopTemplateNameRequired = errors.New("模板名称不能为空")

// TemplateService 模板业务：保存草稿（派生门禁重算）、发布（白话校验）、副本。
type TemplateService struct {
	templates interfaces.SopTemplateRepository
	projects  interfaces.SopProjectRepository
}

// NewTemplateService 构造模板服务。
func NewTemplateService(
	templates interfaces.SopTemplateRepository, projects interfaces.SopProjectRepository,
) *TemplateService {
	return &TemplateService{templates: templates, projects: projects}
}

// TemplateInput 新建/保存草稿的入参。
type TemplateInput struct {
	ProjectID      string
	Name           string
	Description    string
	MetricCategory string
	Definition     types.SopDefinition
}

// Create 新建草稿模板（编号/发布态由模型补齐；定义先归一化并重算派生门禁）。
func (s *TemplateService) Create(
	ctx context.Context, tenantID uint64, userID string, in TemplateInput,
) (*types.SopTemplate, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrSopTemplateNameRequired
	}
	if strings.TrimSpace(in.ProjectID) != "" {
		if _, err := s.projects.GetByID(ctx, tenantID, in.ProjectID); err != nil {
			return nil, err
		}
	}
	def := in.Definition
	types.NormalizeSopDefinition(&def)
	types.EnsureSopDefinitionSlices(&def)
	engine.RefreshDerived(&def)

	tpl := &types.SopTemplate{
		TenantID: tenantID, ProjectID: strings.TrimSpace(in.ProjectID), Name: name,
		Description: in.Description, MetricCategory: in.MetricCategory, Definition: def,
		Status: types.SopTemplateStatusDraft, Version: 1,
	}
	if err := s.templates.Create(ctx, tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

// List 列可用模板（本租户全局 + 项目 + 内置）。
func (s *TemplateService) List(
	ctx context.Context, tenantID uint64, projectID string,
) ([]types.SopTemplate, error) {
	list, err := s.templates.ListByTenant(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []types.SopTemplate{}
	}
	return list, nil
}

// Get 模板详情（内置跨租户只读可见）。
func (s *TemplateService) Get(ctx context.Context, tenantID uint64, id string) (*types.SopTemplate, error) {
	return s.templates.GetByID(ctx, tenantID, id)
}

// Save 保存草稿：归一化默认值 → 重算派生门禁命令 → 落库（内置拒绝）。
//
// 派生命令（gate_commands）永远由定义现算：前端/AI 传来的值是旧的或乱写的，
// 存进去会在运行时执行一条没人写过的命令。
func (s *TemplateService) Save(ctx context.Context, tenantID uint64, tpl *types.SopTemplate) error {
	if tpl == nil {
		return fmt.Errorf("模板不存在")
	}
	if tpl.IsBuiltin {
		return ErrSopTemplateReadonly
	}
	if strings.TrimSpace(tpl.Name) == "" {
		return ErrSopTemplateNameRequired
	}
	types.NormalizeSopDefinition(&tpl.Definition)
	types.EnsureSopDefinitionSlices(&tpl.Definition)
	engine.RefreshDerived(&tpl.Definition)
	if err := s.templates.Update(ctx, tpl); err != nil {
		return wrapTemplateRepoErr(err)
	}
	return nil
}

// Publish 发布：先校验定义（内置豁免执行器完整性），错误逐条白话返回。
//
// 内置模板同样允许发布：它们是平台上随版本下发的流程样例，发布态用于「按此模板
// 发起运行」，缺执行器等配置由发起时的节点设置补齐（澄清 17）。
func (s *TemplateService) Publish(
	ctx context.Context, tenantID uint64, id, userID string,
) (*types.SopTemplateRevision, error) {
	tpl, err := s.templates.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	types.NormalizeSopDefinition(&tpl.Definition)
	errs := engine.ValidateDefinitionWithSelf(&tpl.Definition, tpl.ID, tpl.IsBuiltin)
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w：%s", ErrSopTemplateInvalid, strings.Join(errs, "；"))
	}
	rev, err := s.templates.Publish(ctx, tenantID, id, userID)
	if err != nil {
		return nil, wrapTemplateRepoErr(err)
	}
	return rev, nil
}

// Delete 软删模板（内置拒绝）。
func (s *TemplateService) Delete(ctx context.Context, tenantID uint64, id string) error {
	if err := s.templates.Delete(ctx, tenantID, id); err != nil {
		return wrapTemplateRepoErr(err)
	}
	return nil
}

// Copy 复制出独立草稿（内置模板唯一的修改路径）。
func (s *TemplateService) Copy(
	ctx context.Context, tenantID uint64, id, name string,
) (*types.SopTemplate, error) {
	cp, err := s.templates.Copy(ctx, tenantID, id, strings.TrimSpace(name))
	if err != nil {
		return nil, wrapTemplateRepoErr(err)
	}
	return cp, nil
}

// ListRevisions 列模板的发布历史（revision 降序）。
func (s *TemplateService) ListRevisions(
	ctx context.Context, tenantID uint64, templateID string,
) ([]types.SopTemplateRevision, error) {
	if _, err := s.templates.GetByID(ctx, tenantID, templateID); err != nil {
		return nil, err
	}
	list, err := s.templates.ListRevisions(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []types.SopTemplateRevision{}
	}
	return list, nil
}

// GetRevision 取某条冻结快照。
func (s *TemplateService) GetRevision(
	ctx context.Context, tenantID uint64, id string,
) (*types.SopTemplateRevision, error) {
	return s.templates.GetRevision(ctx, tenantID, id)
}

// wrapTemplateRepoErr 把仓储的「只读」哨兵翻译成本层哨兵（handler 据此映射 400）。
func wrapTemplateRepoErr(err error) error {
	if errors.Is(err, apprepo.ErrSopTemplateBuiltinReadonly) {
		return ErrSopTemplateReadonly
	}
	return err
}
