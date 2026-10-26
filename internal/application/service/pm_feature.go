package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 功能清单错误哨兵：handler 用 errors.Is 映射 400
var (
	ErrFeaturePathInvalid     = errors.New("pm feature: module name is required")
	ErrFeatureOperationOrphan = errors.New("pm feature: operation name requires feature name")
	ErrFeatureVersionRequired = errors.New("pm feature: version id is required")
)

// pmFeatureService 功能清单业务：双来源同步 + 多对多迭代。
// versionRepo 仅用于树节点回填版本号/状态，可为 nil。
type pmFeatureService struct {
	repo        interfaces.PMFeatureRepository
	versionRepo interfaces.PMVersionRepository
}

func NewPMFeatureService(
	repo interfaces.PMFeatureRepository,
	versionRepo interfaces.PMVersionRepository,
) interfaces.PMFeatureService {
	return &pmFeatureService{repo: repo, versionRepo: versionRepo}
}

// SyncFromRoadmap 路线图 → 功能清单（source=roadmap）
// 以三级路径为自然键去重，避免重复生成产生重复节点
func (s *pmFeatureService) SyncFromRoadmap(
	ctx context.Context, tenantID uint64, projectID string, items []types.PMFeature,
) error {
	index, err := s.projectIndex(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err := s.ensureNode(
			ctx, tenantID, projectID, types.FeatureSourceRoadmap, item, index); err != nil {
			return err
		}
	}
	return nil
}

// SyncFromPRD 版本 PRD → 功能清单（source=prd）
// versionID 参与比对：同名操作归属变化时写 change 迭代
func (s *pmFeatureService) SyncFromPRD(
	ctx context.Context, tenantID uint64, projectID, versionID string, items []types.PMFeature,
) error {
	if strings.TrimSpace(versionID) == "" {
		return ErrFeatureVersionRequired
	}
	index, err := s.projectIndex(ctx, tenantID, projectID)
	if err != nil {
		return err
	}

	present := make(map[string]bool, len(items))
	for _, item := range items {
		node, err := s.ensureNode(
			ctx, tenantID, projectID, types.FeatureSourcePRD, item, index)
		if err != nil {
			return err
		}
		present[node.ID] = true
		if err := s.syncIteration(ctx, tenantID, node.ID, versionID); err != nil {
			return err
		}
	}

	// 移除判定：只回收「最新归属当前版本、本次却未出现」的节点，避免误伤其它版本功能
	for _, node := range index {
		if present[node.ID] {
			continue
		}
		last, err := s.latestIteration(ctx, node.ID)
		if err != nil {
			return err
		}
		if last == nil || last.VersionID != versionID {
			continue
		}
		if err := s.recordIteration(
			ctx, tenantID, node.ID, versionID, types.FeatureChangeRemove, "PRD 同步移除"); err != nil {
			return err
		}
	}
	return nil
}

// CreateManual 人工补充：可同时指定版本，写一条 add 迭代
func (s *pmFeatureService) CreateManual(
	ctx context.Context, tenantID uint64, in types.CreatePMFeatureInput,
) (*types.PMFeature, error) {
	if in.Feature == nil {
		return nil, ErrFeaturePathInvalid
	}
	index, err := s.projectIndex(ctx, tenantID, in.Feature.ProjectID)
	if err != nil {
		return nil, err
	}
	node, err := s.ensureNode(
		ctx, tenantID, in.Feature.ProjectID, types.FeatureSourceManual, *in.Feature, index)
	if err != nil {
		return nil, err
	}
	if in.VersionID != "" {
		if err := s.recordIteration(
			ctx, tenantID, node.ID, in.VersionID, types.FeatureChangeAdd, "人工补充"); err != nil {
			return nil, err
		}
	}
	return node, nil
}

func (s *pmFeatureService) ListByProject(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMFeature, error) {
	return s.repo.ListByProject(ctx, tenantID, projectID)
}

// ListIterations 排序契约由仓储保证（版本计划日升序，无版本最后），服务层透传
func (s *pmFeatureService) ListIterations(
	ctx context.Context, featureID string,
) ([]*types.PMFeatureIteration, error) {
	return s.repo.ListIterations(ctx, featureID)
}

// ListTree 返回三级树，节点上携带最新迭代版本号与状态
func (s *pmFeatureService) ListTree(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMFeatureNode, error) {
	list, err := s.repo.ListByProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	nodes := make([]*types.PMFeatureNode, 0, len(list))
	byID := make(map[string]*types.PMFeatureNode, len(list))
	for _, f := range list {
		n := &types.PMFeatureNode{PMFeature: f}
		nodes = append(nodes, n)
		byID[f.ID] = n
	}
	if err := s.attachLatestVersion(ctx, tenantID, nodes); err != nil {
		return nil, err
	}
	return buildFeatureTree(nodes, byID), nil
}

// ---- 内部实现 ----

// syncIteration 依据最新迭代判定本次变更类型：
// 无历史 → add；最新归属其它版本 → change；同版本重复同步不重复记账。
func (s *pmFeatureService) syncIteration(
	ctx context.Context, tenantID uint64, featureID, versionID string,
) error {
	last, err := s.latestIteration(ctx, featureID)
	if err != nil {
		return err
	}
	if last != nil && last.VersionID == versionID {
		return nil
	}
	if last == nil {
		return s.recordIteration(
			ctx, tenantID, featureID, versionID, types.FeatureChangeAdd,
			fmt.Sprintf("PRD 新增（%s）", versionID))
	}
	return s.recordIteration(
		ctx, tenantID, featureID, versionID, types.FeatureChangeChange,
		fmt.Sprintf("PRD 变更（%s → %s）", last.VersionID, versionID))
}

func (s *pmFeatureService) recordIteration(
	ctx context.Context, tenantID uint64, featureID, versionID, changeType, note string,
) error {
	return s.repo.CreateIteration(ctx, &types.PMFeatureIteration{
		TenantID:   tenantID,
		FeatureID:  featureID,
		VersionID:  versionID,
		ChangeType: changeType,
		ChangeNote: note,
	})
}

// latestIteration 取最近一次迭代；仓储按计划日升序返回，末位即最新
func (s *pmFeatureService) latestIteration(
	ctx context.Context, featureID string,
) (*types.PMFeatureIteration, error) {
	list, err := s.repo.ListIterations(ctx, featureID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[len(list)-1], nil
}

// projectIndex 以三级路径为自然键索引项目下已有节点
func (s *pmFeatureService) projectIndex(
	ctx context.Context, tenantID uint64, projectID string,
) (map[string]*types.PMFeature, error) {
	list, err := s.repo.ListByProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	index := make(map[string]*types.PMFeature, len(list))
	for _, f := range list {
		// 复用路线图的自然键规则，保证草稿合并与清单同步判重口径一致
		index[roadmapKey(*f)] = f
	}
	return index, nil
}

// ensureNode 按「模块/功能/操作」逐级 upsert 并返回叶子节点。
// 已存在的层级只校正父子挂载，不覆盖来源与状态。
func (s *pmFeatureService) ensureNode(
	ctx context.Context, tenantID uint64, projectID, source string,
	item types.PMFeature, index map[string]*types.PMFeature,
) (*types.PMFeature, error) {
	module := strings.TrimSpace(item.ModuleName)
	feature := strings.TrimSpace(item.FeatureName)
	operation := strings.TrimSpace(item.OperationName)
	if module == "" {
		return nil, ErrFeaturePathInvalid
	}
	if feature == "" && operation != "" {
		return nil, ErrFeatureOperationOrphan
	}

	levels := []types.PMFeature{{ModuleName: module}}
	if feature != "" {
		levels = append(levels, types.PMFeature{ModuleName: module, FeatureName: feature})
	}
	if operation != "" {
		levels = append(levels, types.PMFeature{
			ModuleName: module, FeatureName: feature, OperationName: operation})
	}

	var node *types.PMFeature
	var parentID string
	for i, level := range levels {
		next, err := s.upsertLevel(
			ctx, tenantID, projectID, source, level, parentID,
			i == len(levels)-1, item, index)
		if err != nil {
			return nil, err
		}
		node, parentID = next, next.ID
	}
	return node, nil
}

// upsertLevel 单级 upsert：命中自然键则校正父级，未命中则新建并挂到 parentID
func (s *pmFeatureService) upsertLevel(
	ctx context.Context, tenantID uint64, projectID, source string,
	level types.PMFeature, parentID string, isLeaf bool, seed types.PMFeature,
	index map[string]*types.PMFeature,
) (*types.PMFeature, error) {
	key := roadmapKey(level)
	if existing, ok := index[key]; ok {
		if existing.ParentID != parentID {
			existing.ParentID = parentID
			if err := s.repo.Update(ctx, existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}

	node := &types.PMFeature{
		TenantID:      tenantID,
		ProjectID:     projectID,
		ParentID:      parentID,
		ModuleName:    level.ModuleName,
		FeatureName:   level.FeatureName,
		OperationName: level.OperationName,
		Source:        source,
		Status:        types.FeatureStatusPlanned,
	}
	// 需求关联只落在叶子（真正被关联的功能点）上
	if isLeaf {
		node.RequirementID = seed.RequirementID
	}
	if err := s.repo.Create(ctx, node); err != nil {
		return nil, err
	}
	index[key] = node
	return node, nil
}

// attachLatestVersion 为每个节点回填最近一次迭代所属版本的版本号与状态
func (s *pmFeatureService) attachLatestVersion(
	ctx context.Context, tenantID uint64, nodes []*types.PMFeatureNode,
) error {
	if s.versionRepo == nil {
		return nil
	}
	cache := make(map[string]*types.PMVersion)
	for _, node := range nodes {
		last, err := s.latestIteration(ctx, node.ID)
		if err != nil {
			return err
		}
		if last == nil || last.VersionID == "" {
			continue
		}
		version, ok := cache[last.VersionID]
		if !ok {
			version, err = s.versionRepo.GetByID(ctx, tenantID, last.VersionID)
			if err != nil {
				// 版本已删除时只跳过版本回填，不影响清单展示
				if errors.Is(err, repository.ErrPMVersionNotFound) {
					continue
				}
				return err
			}
			cache[last.VersionID] = version
		}
		node.LatestVersionNo = version.VersionNo
		node.LatestVersionStatus = version.Status
	}
	return nil
}

// buildFeatureTree 按 parent_id 组树；父节点缺失时降级为根，避免数据丢失
func buildFeatureTree(
	nodes []*types.PMFeatureNode, byID map[string]*types.PMFeatureNode,
) []*types.PMFeatureNode {
	roots := make([]*types.PMFeatureNode, 0, len(nodes))
	for _, node := range nodes {
		parent, ok := byID[node.ParentID]
		if node.ParentID == "" || !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots
}
