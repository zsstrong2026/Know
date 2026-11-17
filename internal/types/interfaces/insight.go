package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// InsightRepository 是洞察模块的仓储契约。
//
// 与工作台一致：每个方法都显式接收 tenantID + ownerID 并写进 WHERE，
// 不存在“只按 id 查”的方法，越权在类型层面无法表达。
type InsightRepository interface {
	// 主题
	ListThemes(ctx context.Context, tenantID uint64, ownerID string) ([]*types.InsightTheme, error)
	GetTheme(ctx context.Context, tenantID uint64, ownerID, id string) (*types.InsightTheme, error)
	CreateTheme(ctx context.Context, tenantID uint64, ownerID string, theme *types.InsightTheme) error
	UpdateTheme(ctx context.Context, tenantID uint64, ownerID string, theme *types.InsightTheme) error
	DeleteTheme(ctx context.Context, tenantID uint64, ownerID, id string) error
	CountReportsByTheme(ctx context.Context, tenantID uint64, ownerID, themeID string) (int64, error)
	LatestReportByTheme(ctx context.Context, tenantID uint64, ownerID, themeID string) (*types.InsightReport, error)

	// 运行
	CreateRun(ctx context.Context, tenantID uint64, ownerID string, run *types.InsightRun) error
	GetRun(ctx context.Context, tenantID uint64, ownerID, id string) (*types.InsightRun, error)
	UpdateRunFields(
		ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]interface{},
	) error
	LatestRunByTheme(ctx context.Context, tenantID uint64, ownerID, themeID string) (*types.InsightRun, error)
	ListRunsByTheme(
		ctx context.Context, tenantID uint64, ownerID, themeID string, limit int,
	) ([]*types.InsightRun, error)
	DeleteRun(ctx context.Context, tenantID uint64, ownerID, id string) error

	// 证据
	CreateEvidenceBatch(ctx context.Context, tenantID uint64, ownerID string, items []*types.InsightEvidence) error
	ListEvidence(ctx context.Context, tenantID uint64, ownerID, runID string) ([]*types.InsightEvidence, error)
	ListEvidenceForReport(
		ctx context.Context, tenantID uint64, ownerID, runID string, limit int,
	) ([]*types.InsightEvidence, error)
	SetEvidenceExcluded(
		ctx context.Context, tenantID uint64, ownerID, runID string, ids []string, excluded bool,
	) error
	DeleteEvidenceByRun(ctx context.Context, tenantID uint64, ownerID, runID string) error

	// 报告
	CreateReport(ctx context.Context, tenantID uint64, ownerID string, report *types.InsightReport) error
	GetReport(ctx context.Context, tenantID uint64, ownerID, id string) (*types.InsightReport, error)
	ListReportsByTheme(ctx context.Context, tenantID uint64, ownerID, themeID string) ([]*types.InsightReport, error)
	DeleteReport(ctx context.Context, tenantID uint64, ownerID, id string) error
	CountReports(ctx context.Context, tenantID uint64, ownerID string) (int64, error)

	// 报告内追问会话
	ListReportMessages(
		ctx context.Context, tenantID uint64, ownerID, reportID string, limit int,
	) ([]*types.InsightReportMessage, error)
	CreateReportMessage(
		ctx context.Context, tenantID uint64, ownerID string, msg *types.InsightReportMessage,
	) error
	DeleteReportMessages(ctx context.Context, tenantID uint64, ownerID, reportID string) error

	// 自我洞悉
	GetSelf(ctx context.Context, tenantID uint64, ownerID string) (*types.InsightSelf, error)
	SaveSelf(ctx context.Context, tenantID uint64, ownerID string, self *types.InsightSelf) error

	// 自我洞悉追问会话
	ListSelfMessages(
		ctx context.Context, tenantID uint64, ownerID string, limit int,
	) ([]*types.InsightSelfMessage, error)
	CreateSelfMessage(
		ctx context.Context, tenantID uint64, ownerID string, msg *types.InsightSelfMessage,
	) error
	DeleteSelfMessages(ctx context.Context, tenantID uint64, ownerID string) error

	// 导出层脱敏设置
	GetPrivacy(ctx context.Context, tenantID uint64, ownerID string) (*types.InsightPrivacy, error)
	SavePrivacy(
		ctx context.Context, tenantID uint64, ownerID string, privacy *types.InsightPrivacy,
	) error
}
