// archive.go 是产物归档：把节点产物作为 .md 源文档写进绑定的知识库，并回填
// 产物行的 kb_id / knowledge_id / ingested_at。
//
// 三条口径（spec §8/§9）：
//   - 入库的永远是**源文档**（.md，带 frontmatter 记录来源节点/运行/sha256）；
//     HTML 渲染视图不入库——渲染层随时会变，源文档才是可追溯的事实；
//   - 幂等：已回填 knowledge_id 的产物直接返回，重复点「归档」不会灌重复文档；
//   - 入库不是门禁：自动归档失败只记日志，不影响运行结论（沉淀是锦上添花）。
package sop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ArchiveDeps 归档的外部依赖（Knowledge 用窄接口，测试可只给 stub）。
type ArchiveDeps struct {
	Runs      interfaces.SopRunRepository
	Items     interfaces.SopItemRepository
	Projects  interfaces.SopProjectRepository
	Knowledge interface {
		CreateKnowledgeFromManual(
			ctx context.Context, kbID string, payload *types.ManualKnowledgePayload, channel string,
		) (*types.Knowledge, error)
	}
}

// ArchiveService 产物归档服务（把产物写进绑定 KB 并回填归档状态）。
type ArchiveService struct {
	d ArchiveDeps
}

// NewArchiveService 构造产物归档服务。
func NewArchiveService(d ArchiveDeps) *ArchiveService { return &ArchiveService{d: d} }

// BuildArchiveMarkdown 把产物包装成带 frontmatter 的 Markdown 源文档。
//
// 已有 frontmatter 的 .md 原样保留（作者自己写的元信息比我们补的更权威）；
// 纯文本类产物包进代码块（保留原始换行与缩进）；二进制拒绝。
func BuildArchiveMarkdown(art *types.SopArtifact, nodeName, runID string, raw []byte) (string, error) {
	if art == nil {
		return "", errors.New("产物为空")
	}
	ext := strings.ToLower(filepath.Ext(art.Path))
	body := string(raw)
	switch ext {
	case ".md":
		if strings.HasPrefix(body, "---\n") {
			return body, nil
		}
	case ".txt", ".log", ".json", ".diff", ".csv":
		lang := strings.TrimPrefix(ext, ".")
		body = "```" + lang + "\n" + strings.TrimRight(body, "\n") + "\n```\n"
	default:
		return "", fmt.Errorf("该产物类型暂不支持入知识库（仅文档类产物）")
	}
	if !utf8.ValidString(body) {
		return "", fmt.Errorf("该产物类型暂不支持入知识库（内容不是文本）")
	}
	fm := fmt.Sprintf("---\ntitle: %q\nsource_node: %q\nrun: %s\nsha256: %s\ncreated: %s\n---\n\n",
		art.Name, nodeName, runID, art.SHA256, time.Now().UTC().Format(time.RFC3339))
	return fm + body, nil
}

// IngestArtifact 归档一个产物。
//
// kbID 为空时按「事项 KB → 项目 KB」兜底；都没有则给白话错误让用户去绑定。
func (s *ArchiveService) IngestArtifact(
	ctx context.Context, tenantID uint64, userID, runID, artifactID, kbID string,
) (*types.SopArtifact, error) {
	if s == nil || s.d.Runs == nil || s.d.Knowledge == nil {
		return nil, errors.New("归档服务未就绪")
	}
	run, err := s.d.Runs.GetRun(ctx, tenantID, runID)
	if err != nil || run == nil || run.ID == "" {
		return nil, fmt.Errorf("运行不存在或无权访问")
	}
	art, err := s.d.Runs.GetArtifact(ctx, tenantID, artifactID)
	if err != nil || art == nil || art.ID == "" {
		return nil, fmt.Errorf("产物不存在")
	}
	if art.RunID != run.ID {
		return nil, fmt.Errorf("产物不存在")
	}
	// 幂等：已归档过就直接返回现状（重复点击不灌重复文档）。
	if strings.TrimSpace(art.KnowledgeID) != "" {
		return art, nil
	}
	targetKB := strings.TrimSpace(kbID)
	if targetKB == "" {
		targetKB = s.resolveKBID(ctx, tenantID, run, userID)
	}
	if targetKB == "" {
		return nil, fmt.Errorf("请先在事项或项目上绑定知识库")
	}

	ws := &engine.Workspace{Root: strings.TrimSpace(run.WorkspaceDir)}
	full, err := engine.SafeWorkspacePath(ws, art.Path)
	if err != nil {
		return nil, fmt.Errorf("产物路径非法：%w", err)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("读取产物文件失败：%w", err)
	}
	md, err := BuildArchiveMarkdown(art, s.nodeName(ctx, tenantID, run, art.NodeID), run.ID, raw)
	if err != nil {
		return nil, err
	}
	kn, err := s.d.Knowledge.CreateKnowledgeFromManual(ctx, targetKB, &types.ManualKnowledgePayload{
		Title: art.Name, Content: md, Status: "publish", Channel: "sop",
	}, "sop")
	if err != nil {
		return nil, fmt.Errorf("写入知识库失败：%w", err)
	}
	now := time.Now()
	art.KBID, art.KnowledgeID, art.IngestedAt = targetKB, kn.ID, &now
	if err := s.d.Runs.UpdateArtifact(ctx, art); err != nil {
		return nil, fmt.Errorf("回填归档状态失败：%w", err)
	}
	return art, nil
}

// IngestNodeArtifacts 是引擎自动归档钩子：逐个归档，单个失败记日志继续。
func (s *ArchiveService) IngestNodeArtifacts(
	ctx context.Context, run *types.SopRun, node *types.SopNode, arts []*types.SopArtifact,
) {
	if s == nil || run == nil || len(arts) == 0 {
		return
	}
	for _, art := range arts {
		if art == nil {
			continue
		}
		if _, err := s.IngestArtifact(ctx, run.TenantID, run.CreatedBy, run.ID, art.ID, ""); err != nil {
			log.Printf("[sop-archive] 运行 %s 产物 %s 自动归档失败：%v", run.ID, art.ID, err)
		}
	}
}

// resolveKBID 按「事项 KB → 项目 KB」找绑定（都没有返回空，由调用方给白话错误）。
func (s *ArchiveService) resolveKBID(
	ctx context.Context, tenantID uint64, run *types.SopRun, _ string,
) string {
	if s.d.Items != nil && strings.TrimSpace(run.ItemID) != "" {
		if item, err := s.d.Items.GetByID(ctx, tenantID, run.ItemID); err == nil && item != nil {
			if kb := strings.TrimSpace(item.KBID); kb != "" {
				return kb
			}
		}
	}
	if s.d.Projects != nil && strings.TrimSpace(run.ProjectID) != "" {
		if proj, err := s.d.Projects.GetByID(ctx, tenantID, run.ProjectID); err == nil && proj != nil {
			if kb := strings.TrimSpace(proj.KBID); kb != "" {
				return kb
			}
		}
	}
	return ""
}

// nodeName 取节点展示名（快照里没有就退回节点 id）。
func (s *ArchiveService) nodeName(
	ctx context.Context, tenantID uint64, run *types.SopRun, nodeID string,
) string {
	for i := range run.WorkflowSnapshot.Nodes {
		n := &run.WorkflowSnapshot.Nodes[i]
		if n.ID == nodeID {
			if name := strings.TrimSpace(n.Name); name != "" {
				return name
			}
			break
		}
	}
	return nodeID
}
