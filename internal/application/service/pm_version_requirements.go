package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// editableVersionRequirement 需求集合操作公共前置：
// 版本必须处于规划中/评审中，且需求确实关联在当前版本内
func (s *pmVersionService) editableVersionRequirement(
	ctx context.Context, tenantID uint64, versionID string, requirementID string,
) (*types.PMVersion, *types.PMRequirement, error) {
	v, err := s.deps.Repo.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, nil, err
	}
	if v.Status != types.VersionStatusPlanning && v.Status != types.VersionStatusReviewing {
		return nil, nil, fmt.Errorf("%w: %s", ErrVersionNotEditable, v.Status)
	}
	if s.deps.RequirementRepo == nil {
		return nil, nil, ErrVersionRequirementInvalid
	}
	ids, err := s.deps.Repo.ListRequirementIDs(ctx, v.ID)
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, id := range ids {
		if id == requirementID {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("%w: %s", ErrVersionRequirementNotFound, requirementID)
	}
	req, err := s.deps.RequirementRepo.GetByID(ctx, tenantID, requirementID)
	if err != nil {
		return nil, nil, err
	}
	return v, req, nil
}

// RemoveRequirement 从当前版本移出需求：
// 仍关联其它版本时保持现状；不再关联任何版本且已排期时回退为已确认。
func (s *pmVersionService) RemoveRequirement(
	ctx context.Context, tenantID uint64, versionID string, requirementID string,
) error {
	_, req, err := s.editableVersionRequirement(ctx, tenantID, versionID, requirementID)
	if err != nil {
		return err
	}
	ids, err := s.deps.Repo.ListRequirementIDs(ctx, versionID)
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != requirementID {
			kept = append(kept, id)
		}
	}
	if err := s.deps.Repo.ReplaceRequirements(ctx, versionID, kept); err != nil {
		return err
	}
	return s.rollbackIfDetached(ctx, tenantID, req)
}

// rollbackIfDetached 需求不再关联任何版本时，把已排期回退为已确认
func (s *pmVersionService) rollbackIfDetached(
	ctx context.Context, tenantID uint64, req *types.PMRequirement,
) error {
	if req.Status != types.RequirementStatusScheduled {
		return nil
	}
	count, err := s.deps.Repo.CountVersionsByRequirementID(ctx, req.ID)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	req.Status = types.RequirementStatusConfirmed
	return s.deps.RequirementRepo.Update(ctx, req)
}

// ShelveRequirement 搁置需求：写搁置原因、移出全部版本、状态置为已拒绝
func (s *pmVersionService) ShelveRequirement(
	ctx context.Context, tenantID uint64, versionID string, requirementID string, reason string,
) error {
	_, req, err := s.editableVersionRequirement(ctx, tenantID, versionID, requirementID)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrVersionShelveReasonRequired
	}
	if utf8.RuneCountInString(reason) > pmVersionShelveMaxRunes {
		return fmt.Errorf("%w: reason exceeds %d characters",
			ErrVersionRequirementInvalid, pmVersionShelveMaxRunes)
	}
	// 先解除全部版本关联，避免搁置后的需求仍出现在其它版本需求集合中
	if err := s.deps.Repo.RemoveRequirementFromAllVersions(ctx, requirementID); err != nil {
		return err
	}
	req.Status = types.RequirementStatusRejected
	req.RejectReason = reason
	return s.deps.RequirementRepo.Update(ctx, req)
}

// ChangeRequirement 修改版本内需求的优先级与描述，空串表示保持原值
func (s *pmVersionService) ChangeRequirement(
	ctx context.Context, tenantID uint64, versionID string, requirementID string,
	priority string, description string,
) error {
	_, req, err := s.editableVersionRequirement(ctx, tenantID, versionID, requirementID)
	if err != nil {
		return err
	}
	priority = strings.TrimSpace(priority)
	if priority != "" && !types.IsValidRequirementPriority(priority) {
		return fmt.Errorf("%w: priority=%s", ErrVersionRequirementInvalid, priority)
	}
	if priority == "" && strings.TrimSpace(description) == "" {
		return ErrVersionRequirementInvalid
	}
	if utf8.RuneCountInString(description) > 2000 {
		return fmt.Errorf("%w: description exceeds 2000 characters", ErrVersionRequirementInvalid)
	}
	if priority != "" {
		req.Priority = priority
	}
	if strings.TrimSpace(description) != "" {
		req.Description = description
	}
	return s.deps.RequirementRepo.Update(ctx, req)
}

// ReplaceRequirement 用项目内已确认需求替换版本内旧需求：
// 位置保持不变，旧需求按「是否仍有关联版本」决定回退，新需求置为已排期。
func (s *pmVersionService) ReplaceRequirement(
	ctx context.Context, tenantID uint64, versionID string, oldRequirementID string,
	newRequirementID string,
) error {
	_, oldReq, err := s.editableVersionRequirement(ctx, tenantID, versionID, oldRequirementID)
	if err != nil {
		return err
	}
	if oldRequirementID == newRequirementID {
		return fmt.Errorf("%w: same requirement", ErrVersionRequirementInvalid)
	}
	newReq, err := s.deps.RequirementRepo.GetByID(ctx, tenantID, newRequirementID)
	if err != nil {
		return err
	}
	if newReq.ProjectID != oldReq.ProjectID {
		return fmt.Errorf("%w: requirement belongs to another project", ErrVersionRequirementInvalid)
	}
	if newReq.Status != types.RequirementStatusConfirmed {
		return fmt.Errorf("%w: target requirement must be confirmed", ErrVersionRequirementInvalid)
	}

	ids, err := s.deps.Repo.ListRequirementIDs(ctx, versionID)
	if err != nil {
		return err
	}
	next := make([]string, 0, len(ids))
	replaced := false
	for _, id := range ids {
		switch id {
		case oldRequirementID:
			next = append(next, newRequirementID)
			replaced = true
		case newRequirementID:
			// 新需求已在集合内时去重，避免同一版本内重复关联
		default:
			next = append(next, id)
		}
	}
	if !replaced {
		return fmt.Errorf("%w: %s", ErrVersionRequirementNotFound, oldRequirementID)
	}
	if err := s.deps.Repo.ReplaceRequirements(ctx, versionID, next); err != nil {
		return err
	}

	if err := s.rollbackIfDetached(ctx, tenantID, oldReq); err != nil {
		return err
	}
	// 新需求进入版本即视为已排期
	newReq.Status = types.RequirementStatusScheduled
	return s.deps.RequirementRepo.Update(ctx, newReq)
}
