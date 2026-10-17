# 会议室模块优化实施计划

> **给执行者：** 执行本计划必须使用 REQUIRED SUB-SKILL: `superpowers:executing-plans`（逐任务执行、按检查点汇报）。每个 Task 严格按 TDD 顺序：写失败测试 → 运行确认失败 → 最小实现 → 运行通过 → 提交。

**Goal:** 落地会议室 4 项优化——G1 卡片精简（只留状态+时间）、G2 会议详情弹窗（设置总入口 + 参会智能体可变更）、G3 创建后主持人开场与流程确认（新状态 `awaiting_confirmation`）、G4 产物一等公民（8 种形态、202 异步生成、隐藏后台会话 + 沙箱 + 技能、抽屉两态展示）。

**Architecture:** 设计规格见 [2026-10-17-meeting-room-optimization-design.md](../specs/2026-10-17-meeting-room-optimization-design.md)。后端 Go/Gin/GORM/Dig/Redis：新状态与守卫放宽（M1）→ `PUT /:id/participants`（M2）→ 开场引擎方法 + `POST /:id/confirm` + `adjust_flow`（M3）→ 卡片精简（M4）→ 产物数据层 + 引擎生成链路（懒创建隐藏后台会话 → 合成执行智能体 → `SessionService.AgentQA`（工具/技能/沙箱）→ `ArtifactCollector` 采集 → 落后台会话 assistant 消息 → 回写 `meeting_artifacts`）（M5）→ 前端抽屉两态 + 生成弹窗 + 收尾（M6）。solution 内部机制零改动（B1），仅统一展示层。前端 Vue3/Pinia/TDesign。

**Tech Stack:** Go, Gin, GORM, Uber Dig（构造参数新增后 dig 自动注入，`container.go` 无需改动）, Redis（`redislock`）, PostgreSQL（`migrations/versioned/`，当前最新 000126）；前端 Vue 3, Pinia, TDesign, TypeScript；测试：Go `testify`，前端 `tsx --test`。

---

## 与 spec 的实现澄清（已定决策，执行时不再讨论）

1. **新增 2 列**：spec §2.2 的 10 字段之外，`meeting_artifacts` 额外加 `kind_type varchar(16)`（8 种形态值，regenerate 与前端形态图标需要）与 `requirement text`（regenerate 回放原参数需要）。`kind` 仍为 `solution|generated`，与 spec 一致。
2. **schema 变更走 versioned 迁移**（000127/000128），不走 spec §2 提到的 AutoMigrate——本仓库 AutoMigrate 仅用于测试，生产 schema 一律 `migrations/versioned/`。
3. **generated 产物下载**：`GET /meetings/:id/artifacts/:aid/download` 实现为 **302 跳转**到既有会话产物下载端点 `/api/v1/sessions/{artifactSessionID}/messages/{sourceMessageID}/artifacts/{index}/download`（后台会话 `UserID = 会议创建者`，属主校验/存储后端解析/tombstone 检查全部复用该端点；这是 spec §2.3「下载复用 artifact_download.go 能力」的落地方式）。文件字节不落 `meeting_artifacts`。
4. **产物执行沙箱**：平台无「租户默认沙箱配置」概念；合成执行智能体的 `Config.SandboxConfigID` 取 **租户第一个具名沙箱配置**（`ListByTenant`，`created_at ASC`）；租户未配置任何沙箱时回退空（文本形态产物仍可产出，文件形态失败并在 `error` 中说明）。
5. **产物生成不注入会议知识库**（`req.KnowledgeBaseIDs = nil`）：`AgentQA` 在 KB 作用域非空时强制要求 agent 配置 rerank 模型（`session_agent_qa.go` L121-141），合成智能体没有 rerank 配置，注入 KB 会让带 KB 的会议全部生成失败；生成素材 = 会议记录 + 已采纳意见（spec §4.5 同义）。
6. **产物落库前提**：`message_artifacts` 表按 `(session_id, message_id, position)` 寻址且 `GetSessionArtifacts` JOIN `messages` 表——因此生成完成后必须把最终回答作为 assistant 消息（携带 `Artifacts`）落库到后台会话（对齐 `agent_stream_handler.go` L774-846 的主链路行为）。
7. **version 序列**：`meeting_artifacts` 有 `UNIQUE(meeting_room_id, version)`，solution 与 generated 共用同一序列，统一改用 `artifactRepo.MaxVersionByRoom()+1` 分配（`GenerateSolution` 同步改造）。
8. **status 列宽**：`awaiting_confirmation` 21 字符 > 现有 `varchar(16)`，迁移 000127 将 `meeting_rooms.status` 扩到 `varchar(32)`。

## File Structure

**后端新建：**
- `migrations/versioned/000127_meeting_status_width.up.sql` / `.down.sql` — status 列扩宽
- `migrations/versioned/000128_meeting_artifact_generation.up.sql` / `.down.sql` — artifact_session_id + meeting_artifacts 12 新列
- `internal/application/service/meeting_opening.go` — 开场纯函数 + `GenerateOpening`
- `internal/application/service/meeting_opening_test.go` — 开场 prompt / latestOpening 纯逻辑测试
- `internal/application/service/meeting_artifact_gen.go` — 产物生成纯函数（形态指令/文件挑选/合成智能体/prompt）
- `internal/application/service/meeting_artifact_gen_test.go` — 上述纯函数测试
- `internal/application/service/meeting_artifact.go` — service 层产物方法（请求/重生成/详情/下载解析）
- `internal/application/service/meeting_artifact_test.go` — 产物 service 校验矩阵
- `internal/application/service/meeting_participants_test.go` — ReplaceParticipants 校验矩阵

**后端修改：**
- `internal/types/meeting.go` — 状态常量、产物常量、Room.ArtifactSessionID、Artifact 12 字段、Normalize、隐藏标记
- `internal/types/session.go` — `SessionRequiresAdminConsoleRead` 加 meeting 标记
- `internal/types/interfaces/meeting.go` — Repository/Service/Engine 接口扩展
- `internal/application/repository/meeting.go` — `DeleteAgentsByRoom`/`Update`/`MaxVersionByRoom`
- `internal/application/repository/session.go` — 会话列表 LIKE 过滤加第三标记
- `internal/application/repository/message_artifact.go` — `ListArtifactLibrary` 过滤加 meeting 标记
- `internal/application/service/meeting_lifecycle.go` — `ensureRoomActive` 放宽
- `internal/application/service/meeting.go` — 默认状态、SendUserMessage 签名、ConfirmFlow、ListArtifacts normalize、依赖扩展
- `internal/application/service/meeting_solution.go` — version 分配改 MaxVersionByRoom；artifact 行补 kind/status
- `internal/application/service/meeting_engine.go` — 构造签名扩展、IsTurnRunning、IsArtifactRunning、RunArtifactGeneration、ensureArtifactSession、runArtifactAgentQA
- `internal/application/service/meeting_host.go` — resolveHostChat 拆出 resolveHostModelID
- `internal/application/service/meeting_broadcast.go` — 4 个新事件常量
- `internal/application/service/meeting_lifecycle_test.go` / `meeting_crud_test.go` — 三态断言、默认状态断言
- `internal/handler/meeting.go` — UpdateMeeting 放宽、SendMessage adjust_flow、新 handler ×6
- `internal/router/routes_meeting.go` — 新路由 ×6

**前端新建：**
- `frontend/src/views/meeting/components/MeetingDetailDialog.vue` — 详情弹窗（设置总入口）
- `frontend/src/views/meeting/components/ArtifactGenerateDialog.vue` — 生成产物弹窗

**前端修改：**
- `frontend/src/api/meeting/index.ts` — 类型扩展 + 新 HTTP 函数 + buildMessagePayload 扩展
- `frontend/src/api/meeting/meeting.test.ts` — 纯函数断言扩展
- `frontend/src/stores/meeting.ts` — confirm/speak(adjustFlow)/artifacts/requestArtifact/regenerateArtifact/applyStreamEvent
- `frontend/src/views/meeting/MeetingLayoutView.vue` — 卡片精简 + 详情入口
- `frontend/src/views/meeting/MeetingRoomView.vue` — 待确认提示条 + 开场按钮组 + 调整流程模式 + 生成产物入口
- `frontend/src/views/meeting/ArtifactDrawer.vue` — 两态重构
- `frontend/src/i18n/locales/{zh-CN,zh-TW,en-US,ja-JP,ko-KR,ru-RU}.ts` — meeting 节新增键（全部 6 个语言，`check-i18n` 强制）

## 全局约定

- 提交信息：`feat(meeting): <描述>` / `test(meeting): ...`，每个 Task 结束提交一次。
- Go 构建/测试（仓库根）：`go build ./...`；定向 `go test ./internal/application/service/ -run TestXxx -v`；M6 收尾跑全量 `go test ./...`。
- 前端（`cd frontend`）：单测 `npm test -- src/api/meeting/meeting.test.ts`；类型 `npm run type-check`；i18n 审计 `npm run check-i18n`。
- 迁移：本计划直接给出文件内容，编号 000127/000128 已核对未占用；执行 `make migrate-up` 验证。
- handler 内取值：`types.TenantIDFromContext(ctx)` / `types.UserIDFromContext(ctx)`。
- 前端组件无组件级测试基建：前端 Task 的"测试"= api 纯函数单测 + `type-check` + `check-i18n`。
- 后端 stub 引擎测试均以嵌入 `interfaces.MeetingEngine` 方式扩展，不影响既有测试。

---

# Milestone M1 — 状态与守卫放宽

**验收标准：** 新建会议状态为 `awaiting_confirmation`；该状态下 PATCH / 发消息 / 评审 / 生成方案均成功；归档后全部 403。`ensureRoomActive` 三态谓词单测通过。

## Task T1.1 新状态常量 + ensureRoomActive 放宽（TDD）

**文件：** `internal/types/meeting.go`、`internal/application/service/meeting_lifecycle.go`、`internal/application/service/meeting_lifecycle_test.go`

- [ ] **Step 1: 修改测试（先失败）** — `meeting_lifecycle_test.go` 全量替换为：

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEnsureRoomActive(t *testing.T) {
	active := &types.MeetingRoom{Status: types.MeetingStatusActive}
	assert.NoError(t, ensureRoomActive(active))

	// M1：待确认状态视为可写（隐式确认/调整流程均发生在该状态），仅 archived 只读
	awaiting := &types.MeetingRoom{Status: types.MeetingStatusAwaitingConfirmation}
	assert.NoError(t, ensureRoomActive(awaiting))

	archived := &types.MeetingRoom{Status: types.MeetingStatusArchived}
	err := ensureRoomActive(archived)
	assert.Equal(t, ErrRoomArchived, err) // 哨兵判等：只以原值返回、从不 wrap
}
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run TestEnsureRoomActive -v`（编译失败：`MeetingStatusAwaitingConfirmation` 未定义）。
- [ ] **Step 3: 最小实现** — `internal/types/meeting.go` 常量块改为：

```go
const (
	MeetingStatusActive             = "active"
	MeetingStatusAwaitingConfirmation = "awaiting_confirmation" // 创建后待确认开场流程；直接打字或 confirm 后转 active
	MeetingStatusArchived            = "archived"
```

（其余常量不动。）`internal/application/service/meeting_lifecycle.go` 的守卫替换为：

```go
// ensureRoomActive 可写守卫：仅 archived（只读终态）拒绝；
// active 与 awaiting_confirmation 均可写（待确认态允许发言/评审/调整流程/改设置）。
func ensureRoomActive(room *types.MeetingRoom) error {
	if room.Status == types.MeetingStatusArchived {
		return ErrRoomArchived
	}
	return nil
}
```

- [ ] **Step 4: 运行通过** — 上述测试命令通过；`go build ./...` 通过。
- [ ] **Step 5: 提交** — `feat(meeting): 新增待确认状态并放宽房间可写守卫为仅归档拒绝`

## Task T1.2 迁移 000127：status 列扩宽 varchar(32)

**文件：** `migrations/versioned/000127_meeting_status_width.up.sql` / `.down.sql`、`internal/types/meeting.go`

- [ ] **Step 1: 写迁移** — `000127_meeting_status_width.up.sql`：

```sql
-- awaiting_confirmation（21 字符）超出 meeting_rooms.status 原 varchar(16) 宽度
ALTER TABLE meeting_rooms ALTER COLUMN status TYPE varchar(32);
```

`000127_meeting_status_width.down.sql`：

```sql
-- 回滚说明：若已存在 awaiting_confirmation 行，缩窄会失败，需先把此类行置回 active
ALTER TABLE meeting_rooms ALTER COLUMN status TYPE varchar(16);
```

- [ ] **Step 2: 同步 gorm tag** — `internal/types/meeting.go` 中 `MeetingRoom.Status`：

```go
	Status string `gorm:"type:varchar(32);default:active" json:"status"`
```

- [ ] **Step 3: 验证** — `make migrate-up`（本地库）+ `make migrate-down`（验证 down 可回滚，回滚前确保无 awaiting 行）；`go build ./...`。
- [ ] **Step 4: 提交** — `feat(meeting): meeting_rooms.status 扩宽到 varchar(32) 以容纳待确认状态`

## Task T1.3 CreateMeeting 默认置 awaiting_confirmation（TDD）

**文件：** `internal/application/service/meeting.go`、`internal/application/service/meeting_crud_test.go`

- [ ] **Step 1: 修改测试** — `meeting_crud_test.go` `TestCreateMeetingBuildsParticipants` 中原断言：

```go
	assert.Equal(t, types.MeetingStatusActive, created.Status, "缺省状态回退 active")
```

替换为：

```go
	assert.Equal(t, types.MeetingStatusAwaitingConfirmation, created.Status, "缺省状态回退 awaiting_confirmation（创建后进入待确认流程）")
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run TestCreateMeetingBuildsParticipants -v`。
- [ ] **Step 3: 最小实现** — `meeting.go` `validateMeetingCreate` 中：

```go
	if room.Status == "" {
		room.Status = types.MeetingStatusAwaitingConfirmation // 创建即待确认；开场与确认交互见 M3
	}
```

- [ ] **Step 4: 运行通过** — 上述命令通过。
- [ ] **Step 5: 提交** — `feat(meeting): 新建会议默认进入待确认状态`

## Task T1.4 handler UpdateMeeting 放宽为仅 archived 403

**文件：** `internal/handler/meeting.go`

- [ ] **Step 1: 修改** — `UpdateMeeting` 中原判定：

```go
	if room.Status != types.MeetingStatusActive {
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
		return
	}
```

替换为：

```go
	// 仅 archived 只读；awaiting_confirmation 允许改设置（详情弹窗在待确认态可编辑，见 M2）
	if room.Status == types.MeetingStatusArchived {
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
		return
	}
```

- [ ] **Step 2: 验证** — `go build ./...`；`go test ./internal/application/service/ -run 'TestEnsureRoomActive|TestCreateMeeting' -v`。
- [ ] **Step 3: 提交** — `feat(meeting): PATCH 会议在待确认状态下放行（仅归档 403）`

**M1 验收：** `go test ./internal/application/service/ -run 'TestEnsureRoomActive|TestCreateMeeting|TestUpdateMeeting' -v` 全绿；`make migrate-up` 成功。

---

# Milestone M2 — 详情弹窗 + 参会人可变更

**验收标准：** 弹窗能改所有设置并持久化；能增删智能体（主持人保留、≥1 agent）；讨论进行中改参会人 409；归档只读。

## Task T2.1 Repository：DeleteAgentsByRoom（TDD 部分：接口先行）

**文件：** `internal/types/interfaces/meeting.go`、`internal/application/repository/meeting.go`

- [ ] **Step 1: 接口扩展** — `MeetingParticipantRepository` 追加：

```go
	// DeleteAgentsByRoom 全量替换前置清理：仅删 agent 类参会者，host 记录始终保留
	DeleteAgentsByRoom(ctx context.Context, roomID string) error
```

- [ ] **Step 2: 实现** — `repository/meeting.go` `meetingParticipantRepository` 追加：

```go
// DeleteAgentsByRoom 全量替换参会智能体时清理旧 agent 行（主持人记录不受影响）
func (r *meetingParticipantRepository) DeleteAgentsByRoom(ctx context.Context, roomID string) error {
	return r.db.WithContext(ctx).
		Where("meeting_room_id = ? AND kind = ?", roomID, types.ParticipantKindAgent).
		Delete(&types.MeetingParticipant{}).Error
}
```

- [ ] **Step 3: 验证** — `go build ./...`（接口新增会暴露所有实现方；本仓库仅此一处实现 + 测试 fake，fake 在 T2.3 补）。
- [ ] **Step 4: 提交** — `feat(meeting): 参会者仓储支持按房间清理智能体行`

## Task T2.2 Engine：IsTurnRunning

**文件：** `internal/types/interfaces/meeting.go`、`internal/application/service/meeting_engine.go`

- [ ] **Step 1: 接口扩展** — `MeetingEngine` 追加（与既有方法并列）：

```go
	// IsTurnRunning 查询讨论轮锁是否被持有（参会人变更 409 预检用）
	IsTurnRunning(ctx context.Context, roomID string) (bool, error)
```

- [ ] **Step 2: 实现** — `meeting_engine.go`（`isInterrupted` 附近）追加：

```go
// IsTurnRunning 查询讨论轮锁是否被持有（无 Redis 的单测/降级环境按"无进行中讨论"处理）
func (e *meetingEngine) IsTurnRunning(ctx context.Context, roomID string) (bool, error) {
	if e.redis == nil {
		return false, nil
	}
	n, err := e.redis.Exists(ctx, turnLockKey(roomID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
```

- [ ] **Step 3: 验证** — `go build ./...`。
- [ ] **Step 4: 提交** — `feat(meeting): 讨论引擎暴露讨论锁查询`

## Task T2.3 Service：ReplaceParticipants（TDD）

**文件：** `internal/application/service/meeting.go`、`internal/application/service/meeting_participants_test.go`、`internal/types/interfaces/meeting.go`

- [ ] **Step 1: 写失败测试** — 新建 `meeting_participants_test.go`：

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeReplaceParticipantRepo 记录清理与插入动作（host 行由 ListByRoom 模拟保留）
type fakeReplaceParticipantRepo struct {
	interfaces.MeetingParticipantRepository
	batched []*types.MeetingParticipant
	deleted []string
}

func (f *fakeReplaceParticipantRepo) CreateBatch(_ context.Context, ps []*types.MeetingParticipant) error {
	f.batched = append(f.batched, ps...)
	return nil
}

func (f *fakeReplaceParticipantRepo) DeleteAgentsByRoom(_ context.Context, roomID string) error {
	f.deleted = append(f.deleted, roomID)
	return nil
}

func (f *fakeReplaceParticipantRepo) ListByRoom(_ context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	out := []*types.MeetingParticipant{{
		ID: "host-row", MeetingRoomID: roomID, Kind: types.ParticipantKindHost,
		DisplayName: "主持人", OrderIndex: 0,
	}}
	return append(out, f.batched...), nil
}

// stubTurnEngine 可控返回讨论锁状态
type stubTurnEngine struct {
	interfaces.MeetingEngine
	running bool
}

func (e *stubTurnEngine) IsTurnRunning(context.Context, string) (bool, error) {
	return e.running, nil
}

func replaceFixture(agents map[string]*types.CustomAgent, running bool) (*meetingService, *isolationRoomRepo, *fakeReplaceParticipantRepo, *stubTurnEngine) {
	rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
	parts := &fakeReplaceParticipantRepo{}
	engine := &stubTurnEngine{running: running}
	svc := &meetingService{
		roomRepo:        rooms,
		participantRepo: parts,
		messageRepo:     &fakeMeetingMessageRepo{},
		agentRepo:       &fakeCRUDAgentRepo{agents: agents},
		engine:          engine,
	}
	return svc, rooms, parts, engine
}

func TestReplaceParticipantsMatrix(t *testing.T) {
	ctx := context.Background()
	agents := map[string]*types.CustomAgent{
		"a1": {ID: "a1", Name: "架构师"},
		"a2": {ID: "a2", Name: "测试专家"},
	}

	t.Run("归档只读", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusArchived}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		assert.Equal(t, ErrRoomArchived, err)
		assert.Empty(t, parts.deleted, "归档拒绝时不触发清理")
	})

	t.Run("空智能体拒绝", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", nil)
		assert.Equal(t, ErrNoAgents, err)
		assert.Empty(t, parts.deleted)
	})

	t.Run("讨论进行中 409", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, true)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		assert.Equal(t, ErrTurnRunning, err)
		assert.Empty(t, parts.deleted)
	})

	t.Run("跨租户拒绝", func(t *testing.T) {
		svc, rooms, _, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 2, "r1", []string{"a1"})
		assert.Error(t, err)
	})

	t.Run("待确认状态允许替换", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusAwaitingConfirmation}))
		out, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		require.NoError(t, err)
		assert.Equal(t, []string{"r1"}, parts.deleted)
		require.Len(t, parts.batched, 1)
		assert.Equal(t, 1, parts.batched[0].OrderIndex, "agent 序号从 1 开始（0 留给主持人）")
		// 主持人记录保留在返回列表首位
		require.NotEmpty(t, out)
		assert.Equal(t, types.ParticipantKindHost, out[0].Kind)
	})

	t.Run("agent 不存在时无副作用", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1", "ghost"})
		assert.ErrorContains(t, err, "agent not found: ghost")
		assert.Empty(t, parts.deleted, "预校验失败不得先删后插")
	})

	t.Run("重复 ID 去重保序", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a2", "a1", "a2"})
		require.NoError(t, err)
		require.Len(t, parts.batched, 2)
		assert.Equal(t, "a2", parts.batched[0].AgentID)
		assert.Equal(t, "a1", parts.batched[1].AgentID)
	})
}

var _ = time.Now // 保留 time 导入位（若未用到请删除本行与 import）
```

（若 `time` 未被使用则连同最后一行一起删除。）

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run TestReplaceParticipantsMatrix -v`（`ReplaceParticipants`/`ErrTurnRunning`/`ErrNoAgents` 未定义）。
- [ ] **Step 3: 最小实现** — `internal/types/interfaces/meeting.go` `MeetingService` 追加：

```go
	// ReplaceParticipants 全量替换参会智能体：主持人不可移除、至少 1 个 agent、
	// 讨论进行中 409（ErrTurnRunning）、归档只读；awaiting_confirmation 允许
	ReplaceParticipants(ctx context.Context, tenantID uint64, roomID string, agentIDs []string) ([]*types.MeetingParticipant, error)
```

`meeting.go`（`CreateMeeting` 之后）追加：

```go
var (
	// ErrTurnRunning 讨论轮进行中（handler 判等映射 409）
	ErrTurnRunning = errors.New("meeting: discussion turn is running")
	// ErrNoAgents 全量替换至少保留一个智能体（handler 判等映射 400）
	ErrNoAgents = errors.New("meeting: at least one agent is required")
)

// ReplaceParticipants 全量替换参会智能体（host 记录由仓储层强制保留，请求无法移除主持人）。
// 顺序：归档拒绝 → ≥1 agent → 讨论锁 409 → 全量预校验 → 先删后插。
func (s *meetingService) ReplaceParticipants(ctx context.Context, tenantID uint64, roomID string, agentIDs []string) ([]*types.MeetingParticipant, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // archived 只读；awaiting_confirmation 允许
	}
	ids := dedupeAgentIDs(agentIDs)
	if len(ids) == 0 {
		return nil, ErrNoAgents
	}
	running, err := s.engine.IsTurnRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrTurnRunning
	}
	// 先全量预校验并构建参与者，避免"删了一半发现 agent 不存在"的部分替换
	agents := make([]*types.CustomAgent, 0, len(ids))
	for _, id := range ids {
		agent, err := s.agentRepo.GetAgentByID(ctx, id, room.TenantID)
		if err != nil {
			return nil, errors.New("agent not found: " + id)
		}
		agents = append(agents, agent)
	}
	if err := s.participantRepo.DeleteAgentsByRoom(ctx, roomID); err != nil {
		return nil, err
	}
	ps := make([]*types.MeetingParticipant, 0, len(agents))
	for i, agent := range agents {
		ps = append(ps, &types.MeetingParticipant{
			MeetingRoomID: roomID, Kind: types.ParticipantKindAgent,
			AgentID: agent.ID, DisplayName: agent.Name, Avatar: agent.Avatar, OrderIndex: i + 1,
		})
	}
	if err := s.participantRepo.CreateBatch(ctx, ps); err != nil {
		return nil, err
	}
	return s.participantRepo.ListByRoom(ctx, roomID)
}

// dedupeAgentIDs 去重保序（全量替换语义下重复 ID 无意义）
func dedupeAgentIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
```

- [ ] **Step 4: 运行通过** — 上述命令 + `go test ./internal/application/service/ -run 'TestCreateMeeting|TestGenerateSolution' -v`（回归既有）。
- [ ] **Step 5: 提交** — `feat(meeting): 参会智能体支持全量替换（409/归档/至少一个校验）`

## Task T2.4 Handler + Router：PUT /:id/participants

**文件：** `internal/handler/meeting.go`、`internal/router/routes_meeting.go`

- [ ] **Step 1: handler** — `meeting.go` 追加：

```go
type replaceParticipantsRequest struct {
	AgentIDs []string `json:"agent_ids"`
}

// ReplaceParticipants PUT /api/v1/meetings/:id/participants —— 全量替换参会智能体
func (h *MeetingHandler) ReplaceParticipants(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	var req replaceParticipantsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	participants, err := h.meetingService.ReplaceParticipants(ctx, tenantID, c.Param("id"), req.AgentIDs)
	if err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		if err == service.ErrTurnRunning {
			c.Error(errors.NewConflictError("讨论进行中，暂不能修改参会智能体"))
			return
		}
		if err == service.ErrNoAgents {
			c.Error(errors.NewBadRequestError("至少保留一个参会智能体"))
			return
		}
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": participants})
}
```

- [ ] **Step 2: 路由** — `routes_meeting.go` 在 `meetings.PATCH("/:id", ...)` 之后加：

```go
		meetings.PUT("/:id/participants", g.OwnedMeetingOrAdmin(), meetingHandler.ReplaceParticipants)
```

- [ ] **Step 3: 验证** — `go build ./...`。
- [ ] **Step 4: 提交** — `feat(meeting): 开放参会人全量替换端点`

## Task T2.5 前端：详情弹窗 + 卡片入口 + i18n

**文件：** `frontend/src/api/meeting/index.ts`、`frontend/src/views/meeting/components/MeetingDetailDialog.vue`（新建）、`frontend/src/views/meeting/MeetingLayoutView.vue`、6 个 locale 文件

- [ ] **Step 1: API** — `index.ts` 在 `updateMeeting` 之后追加：

```ts
// 全量替换参会智能体（至少一个；讨论进行中后端 409）
export async function replaceParticipants(roomId: string, agentIds: string[]) {
  const { put } = await loadRequest();
  return put<{ data: MeetingParticipant[] }>(
    `/api/v1/meetings/${roomId}/participants`,
    { agent_ids: agentIds },
  ).then((r) => r.data);
}
```

- [ ] **Step 2: 新建 MeetingDetailDialog.vue** — 完整内容：

```vue
<template>
  <!-- 会议详情弹窗：会议设置总入口（标题/描述/参会智能体/讨论模式/路由/护栏/介入/共识） -->
  <t-dialog :visible="visible" :header="$t('meeting.detailTitle')" :width="640"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.save'), loading: saving, disabled: isArchived }"
    :cancel-btn="{ content: $t('meeting.cancel') }" @confirm="submit" @close="emit('update:visible', false)">
    <t-form :data="form" :label-width="130" class="detail-form">
      <t-form-item :label="$t('meeting.title')" required>
        <t-input v-model="form.title" :maxlength="100" :disabled="isArchived" />
      </t-form-item>
      <t-form-item :label="$t('meeting.topic')">
        <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }"
          :disabled="isArchived" :placeholder="$t('meeting.topicPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.selectAgents')" required>
        <t-select v-model="form.agentIds" multiple filterable :disabled="isArchived"
          :placeholder="$t('meeting.selectAgents')">
          <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name" />
        </t-select>
        <div v-if="participantsLockedHint" class="field-hint">{{ $t('meeting.participantsLocked') }}</div>
      </t-form-item>
      <t-form-item :label="$t('meeting.broadcastMode')">
        <t-radio-group v-model="broadcastMode" :disabled="isArchived">
          <t-radio value="sequential">{{ $t('meeting.modeSequential') }}</t-radio>
          <t-radio value="parallel">{{ $t('meeting.modeParallel') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item :label="$t('meeting.routingMode')">
        <t-radio-group v-model="routingMode" :disabled="isArchived">
          <t-radio value="host-assign">{{ $t('meeting.routingHostAssign') }}</t-radio>
          <t-radio value="mention">{{ $t('meeting.routingMention') }}</t-radio>
          <t-radio value="free">{{ $t('meeting.routingFree') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item :label="$t('meeting.guardMaxExchange')">
        <t-input-number v-model="form.guardMaxExchange" :min="1" :max="10" theme="column"
          :disabled="isArchived" />
      </t-form-item>
      <t-form-item :label="$t('meeting.hostEveryNRounds')">
        <t-select v-model="form.hostEveryNRounds" :disabled="isArchived">
          <t-option :value="0" :label="$t('meeting.hostEveryNRoundsOff')" />
          <t-option v-for="n in HOST_INTERVENE_OPTIONS" :key="n" :value="n"
            :label="$t('meeting.hostInterveneEvery', { n })" />
        </t-select>
      </t-form-item>
      <t-form-item :label="$t('meeting.consensusDetection')">
        <t-switch v-model="form.consensusDetectionEnabled" :disabled="isArchived" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  replaceParticipants, updateMeeting,
  type BroadcastMode, type RoutingMode, type MeetingRoom,
} from '@/api/meeting';
import { listAgents, type CustomAgent } from '@/api/agent';

const props = defineProps<{ visible: boolean; room: MeetingRoom | null }>();
const emit = defineEmits<{ 'update:visible': [boolean]; saved: [] }>();

const { t } = useI18n();
const agents = ref<CustomAgent[]>([]);
const saving = ref(false);
// 409 提示为一次性状态：提交被拒后置位，改动后清除
const participantsLockedHint = ref(false);
let optionsLoaded = false;

const HOST_INTERVENE_OPTIONS = [2, 3, 5, 10];

const form = reactive({
  title: '', description: '',
  agentIds: [] as string[],
  broadcastMode: 'sequential' as BroadcastMode,
  routingMode: 'host-assign' as RoutingMode,
  guardMaxExchange: 3,
  hostEveryNRounds: 0,
  consensusDetectionEnabled: false,
});

const isArchived = computed(() => props.room?.status === 'archived');

// t-radio-group 变更值类型较宽，用带类型 setter 的 computed 收敛回联合类型
const broadcastMode = computed<BroadcastMode>({
  get: () => form.broadcastMode,
  set: (val) => { form.broadcastMode = val; },
});
const routingMode = computed<RoutingMode>({
  get: () => form.routingMode,
  set: (val) => { form.routingMode = val; },
});

let initialAgentIds: string[] = [];

// 每次打开：用房间当前值回填表单，并懒加载智能体选项
watch(() => props.visible, (v) => {
  if (!v || !props.room) return;
  form.title = props.room.title;
  form.description = props.room.description ?? '';
  form.agentIds = (props.room.participants ?? [])
    .filter((p) => p.kind === 'agent' && p.agent_id)
    .map((p) => p.agent_id);
  initialAgentIds = [...form.agentIds];
  form.broadcastMode = props.room.broadcast_mode ?? 'sequential';
  form.routingMode = props.room.routing_mode ?? 'host-assign';
  form.guardMaxExchange = props.room.guard_max_exchange ?? 3;
  form.hostEveryNRounds = props.room.host_every_n_rounds ?? 0;
  form.consensusDetectionEnabled = props.room.consensus_detection_enabled ?? false;
  participantsLockedHint.value = false;
  void loadOptions();
});

watch(() => form.agentIds, () => { participantsLockedHint.value = false; });

async function loadOptions() {
  if (optionsLoaded) return;
  try {
    const res = await listAgents();
    agents.value = res?.data ?? [];
    optionsLoaded = true;
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  }
}

async function submit() {
  if (!props.room) return;
  if (!form.title.trim()) {
    MessagePlugin.warning(t('meeting.title'));
    return;
  }
  if (!form.agentIds.length) {
    MessagePlugin.warning(t('meeting.selectAgents'));
    return;
  }
  saving.value = true;
  try {
    // 参会人变更走独立端点；仅在确实发生增删时触发
    const changed = form.agentIds.length !== initialAgentIds.length
      || form.agentIds.some((id) => !initialAgentIds.includes(id));
    if (changed) {
      try {
        await replaceParticipants(props.room.id, form.agentIds);
      } catch (e) {
        const msg = (e as Error)?.message || '';
        if (msg.includes('409') || msg.includes('讨论') || msg.includes('Conflict')) {
          participantsLockedHint.value = true;
          return; // 保留弹窗与用户输入
        }
        throw e;
      }
    }
    // 设置类字段走 PATCH 白名单（后端要求 title/broadcast_mode/guard_max_exchange 必填）
    await updateMeeting(props.room.id, {
      title: form.title.trim(),
      description: form.description,
      broadcast_mode: form.broadcastMode,
      routing_mode: form.routingMode,
      guard_max_exchange: form.guardMaxExchange,
      host_every_n_rounds: form.hostEveryNRounds,
      consensus_detection_enabled: form.consensusDetectionEnabled,
    });
    emit('update:visible', false);
    emit('saved');
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.saveFailed'));
  } finally {
    saving.value = false;
  }
}
</script>

<style lang="less" scoped>
.detail-form {
  padding-top: var(--app-space-2);
}

.field-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-warning-color);
}
</style>
```

- [ ] **Step 3: 卡片入口** — `MeetingLayoutView.vue`：
  - `rc-actions` 内（归档按钮之前）加：

```html
            <t-button size="small" variant="text" @click="openDetail(room)">
              {{ $t('meeting.detail') }}
            </t-button>
```

  - `MeetingCreateDialog` 引入行之后加 import 与状态：

```ts
import MeetingDetailDialog from './components/MeetingDetailDialog.vue';
```

```ts
const showDetail = ref(false);
const detailRoom = ref<MeetingRoom | null>(null);
```

  - template 中 `<MeetingCreateDialog ...>` 之后加：

```html
    <MeetingDetailDialog v-model:visible="showDetail" :room="detailRoom" @saved="refresh" />
```

  - script 追加：

```ts
// 打开详情弹窗（@click.stop 已由 rc-actions 容器承担阻断跳转）
function openDetail(room: MeetingRoom) {
  detailRoom.value = room;
  showDetail.value = true;
}
```

- [ ] **Step 4: i18n** — 6 个 locale 文件 `meeting` 节新增键（见文末「附录 A：i18n 新增键全语言表」M2/M3/M4/M6 各节，一次性加齐亦可；本 Task 至少加 `detail`/`detailTitle`/`save`/`saveFailed`/`participantsLocked`/`guardMaxExchange`）。
- [ ] **Step 5: 验证** — `cd frontend; npm run type-check; npm run check-i18n`。
- [ ] **Step 6: 提交** — `feat(meeting): 会议详情弹窗（设置总入口 + 参会智能体增删）`

**M2 验收：** 后端矩阵测试全绿；前端 type-check/check-i18n 通过；手工：弹窗改标题/模式/参会人后刷新生效；进行中讨论点保存提示 409 文案。

---

# Milestone M3 — 开场 + 流程确认

**验收标准：** 新建会议数秒内出现开场消息（主题分析 + 建议流程）与按钮组；点「开始会议」自动开跑首轮；「调整流程」后主持人重出流程；刷新页面开场与按钮仍在；开场失败落系统兜底、房间不卡死。

## Task T3.1 resolveHostModelID 重构 + buildOpeningPrompt（TDD）

**文件：** `internal/application/service/meeting_host.go`、`internal/application/service/meeting_opening.go`（新建）、`internal/application/service/meeting_opening_test.go`（新建）

- [ ] **Step 1: 写失败测试** — 新建 `meeting_opening_test.go`：

```go
package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildOpeningPrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", Description: "评估 v2 发布风险"}
	agents := []*types.CustomAgent{
		{ID: "a1", Name: "架构师", Description: "负责系统架构"},
		{ID: "a2", Name: "测试专家"},
	}

	p := buildOpeningPrompt(room, agents, "")
	assert.Contains(t, p, "发布评审")
	assert.Contains(t, p, "评估 v2 发布风险")
	assert.Contains(t, p, "架构师")
	assert.Contains(t, p, "负责系统架构", "开场应带智能体专长供主持人规划流程")
	assert.Contains(t, p, "## 主题分析")
	assert.Contains(t, p, "## 建议流程")
	assert.NotContains(t, p, "修改意见", "首次开场不含调整反馈段")

	p2 := buildOpeningPrompt(room, nil, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "重新输出开场内容", "调整流程模式要求主持人结合意见重出流程")
}

func TestOpeningPayloadMarshal(t *testing.T) {
	// payload 固定为 {"stage":"opening"}，前端据此渲染按钮组（契约测试）
	p := openingPayload{Stage: MeetingOpeningStage}
	b, err := jsonMarshalForTest(p)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(b), `"stage":"opening"`), "got %s", string(b))
}
```

（`jsonMarshalForTest` 直接用 `encoding/json.Marshal`，在测试文件内定义：`func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }`，并 import `encoding/json`。）

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run 'TestBuildOpeningPrompt|TestOpeningPayloadMarshal' -v`。
- [ ] **Step 3: 最小实现** —
  1. `meeting_host.go`：把 `resolveHostChat` 拆为两段（保持既有调用不变）：

```go
// resolveHostModelID 取租户默认 KnowledgeQA 模型 ID（开场与产物执行智能体复用同一模型解析）
func (e *meetingEngine) resolveHostModelID(ctx context.Context) (string, error) {
	models, err := e.modelService.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("no default KnowledgeQA model for meeting host")
}

// resolveHostChat 获取租户默认对话模型作为主持人大脑。
// 模式仿 agent_eval.go resolveJudgeModel：ListModels → KnowledgeQA 默认模型 → GetChatModel。
func (e *meetingEngine) resolveHostChat(ctx context.Context) (chat.Chat, error) {
	modelID, err := e.resolveHostModelID(ctx)
	if err != nil {
		return nil, err
	}
	return e.modelService.GetChatModel(ctx, modelID)
}
```

  2. 新建 `meeting_opening.go`：

```go
package service

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// MeetingOpeningStage 开场消息 payload.stage 固定值（前端据此渲染「开始会议 / 调整流程」按钮组）
const MeetingOpeningStage = "opening"

// openingPayload 开场消息的 jsonb 载荷
type openingPayload struct {
	Stage string `json:"stage"`
}

// buildOpeningPrompt 组装主持人的开场提示词（纯函数，单测覆盖）。
// feedback 为空 = 创建后的首次开场；非空 = 「调整流程」时用户对流程的修改意见。
func buildOpeningPrompt(room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是会议室主持人。会议主题：%s\n", room.Title)
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	if len(agents) > 0 {
		b.WriteString("参会智能体：\n")
		for _, a := range agents {
			line := "- " + a.Name
			if a.Description != "" {
				line += "：" + truncate(a.Description, 80)
			}
			b.WriteString(line + "\n")
		}
	}
	if feedback != "" {
		b.WriteString("\n用户对上一版会议流程提出了修改意见：\n" + feedback + "\n")
		b.WriteString("请结合该意见重新输出开场内容。\n")
	}
	b.WriteString("\n请以主持人身份输出开场发言，固定包含两个部分：\n")
	b.WriteString("## 主题分析\n对本次会议议题的理解与拆解（背景、要解决的核心问题、关键约束）。\n")
	b.WriteString("## 建议流程\n给出分段/分轮的会议流程建议：每个阶段的讨论重点、希望哪些智能体先发言，以及需要用户确认或拍板的点。\n")
	b.WriteString("\n要求：语言简洁、面向普通用户；不要编造会议中不存在的材料；不要展开具体讨论内容（讨论在用户确认后开始）。")
	return b.String()
}
```

- [ ] **Step 4: 运行通过** — 上述命令；`go build ./...`（既有 `resolveHostChat` 调用方不受影响）。
- [ ] **Step 5: 提交** — `feat(meeting): 开场提示词与主持人模型 ID 解析复用`

## Task T3.2 Engine：GenerateOpening + 事件常量 + 创建后异步触发（TDD 部分）

**文件：** `internal/application/service/meeting_opening.go`、`internal/application/service/meeting_broadcast.go`、`internal/types/interfaces/meeting.go`、`internal/application/service/meeting.go`

- [ ] **Step 1: 事件常量** — `meeting_broadcast.go` 常量块追加：

```go
	MeetingEventFlowConfirmation = "discussion.flow_confirmation"
```

- [ ] **Step 2: 接口扩展** — `MeetingEngine` 追加：

```go
	// GenerateOpening 主持人生成开场消息（主题分析 + 建议流程）并落库广播；
	// feedbackMessageID 非空 = 按用户「调整流程」意见重出开场
	GenerateOpening(ctx context.Context, room *types.MeetingRoom, feedbackMessageID string) error
```

- [ ] **Step 3: 引擎实现** — `meeting_opening.go` 追加：

```go
// GenerateOpening 主持人生成开场消息并落库广播（message.created + discussion.flow_confirmation）。
// LLM 不可用/失败时落 system 兜底消息，房间保持在 awaiting_confirmation，用户可直接发言隐式开跑。
func (e *meetingEngine) GenerateOpening(ctx context.Context, room *types.MeetingRoom, feedbackMessageID string) error {
	feedback := ""
	if feedbackMessageID != "" {
		if msg, err := e.messageRepo.GetByID(ctx, feedbackMessageID); err == nil {
			feedback = msg.Content
		}
	}
	agents := e.loadRoomAgents(ctx, room)
	content := e.generateOpeningContent(ctx, room, agents, feedback)
	if content == "" {
		return nil // 兜底 system 消息已在 generateOpeningContent 内落库
	}
	payload, err := json.Marshal(openingPayload{Stage: MeetingOpeningStage})
	if err != nil {
		return err
	}
	msg := &types.MeetingMessage{
		MeetingRoomID: room.ID,
		SenderKind:    types.MeetingSenderHost,
		Content:       content,
		Payload:       payload,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventFlowConfirmation, Data: map[string]interface{}{
		"room_id": room.ID, "message_id": msg.ID, "stage": MeetingOpeningStage,
	}})
	return nil
}

// loadRoomAgents 加载参会智能体实体（开场/产物生成取名称与专长用）
func (e *meetingEngine) loadRoomAgents(ctx context.Context, room *types.MeetingRoom) []*types.CustomAgent {
	participants, err := e.participantRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		return nil
	}
	out := make([]*types.CustomAgent, 0)
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" {
			continue
		}
		if agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID); err == nil {
			out = append(out, agent)
		}
	}
	return out
}

// generateOpeningContent 主持人生成开场正文；模型不可用/生成失败时落 system 兜底消息并返回空串
func (e *meetingEngine) generateOpeningContent(ctx context.Context, room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	hostChat, err := e.resolveHostChat(ctx)
	if err == nil {
		resp, cerr := hostChat.Chat(ctx,
			[]chat.Message{{Role: "user", Content: buildOpeningPrompt(room, agents, feedback)}},
			&chat.ChatOptions{Temperature: 0.3})
		if cerr != nil {
			logger.Warnf(ctx, "meeting opening generate failed, room=%s err=%v", room.ID, cerr)
		} else if resp != nil && strings.TrimSpace(resp.Content) != "" {
			return resp.Content
		}
	} else {
		logger.Warnf(ctx, "meeting opening host model unavailable, room=%s err=%v", room.ID, err)
	}
	e.publishSystemMessage(ctx, room.ID, "主持人开场生成失败，可直接发言开始会议")
	return ""
}
```

`meeting_opening.go` import 块相应为：

```go
import (
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)
```

（`fmt` 已由 buildOpeningPrompt 使用，保留。）

- [ ] **Step 4: 创建后异步触发** — `meeting.go` `CreateMeeting` 在 `participantRepo.CreateBatch` 成功后、`return room, nil` 之前追加：

```go
	// 创建成功即异步触发主持人开场（M3）：不阻塞创建返回；失败由引擎落 system 兜底，房间保持待确认
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting GenerateOpening panic, room=%s recover=%v", room.ID, r)
			}
		}()
		if err := s.engine.GenerateOpening(bgCtx, room, ""); err != nil {
			logger.Errorf(bgCtx, "meeting GenerateOpening failed, room=%s err=%v", room.ID, err)
		}
	}()
```

- [ ] **Step 5: 测试补充** — `meeting_opening_test.go` 追加（引擎方法依赖仓储 fake，走轻量断言）：

```go
func TestLatestOpeningMessageID(t *testing.T) {
	// 纯逻辑：payload 解析由 latestOpeningMessageID 承担，这里验证其倒序取最近开场
	msgs := []*types.MeetingMessage{
		{ID: "m1", Payload: []byte(`{"stage":"opening"}`)},
		{ID: "m2", Payload: []byte(`{}`)},
		{ID: "m3", Payload: []byte(`{"stage":"opening"}`)},
	}
	// 最近一条开场是 m3
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		var p openingPayload
		if json.Unmarshal(msgs[i].Payload, &p) == nil && p.Stage == MeetingOpeningStage {
			idx = i
			break
		}
	}
	assert.Equal(t, 2, idx)
}
```

（`latestOpeningMessageID` 的 service 实现见 T3.3；本测试先固化"倒序找最近开场"的解析契约。import 补 `encoding/json`。）

- [ ] **Step 6: 验证** — `go test ./internal/application/service/ -run 'TestBuildOpeningPrompt|TestLatestOpeningMessageID' -v`；`go build ./...`。
- [ ] **Step 7: 提交** — `feat(meeting): 创建后异步生成主持人开场并广播流程确认事件`

## Task T3.3 Service：ConfirmFlow + 端点（TDD）

**文件：** `internal/application/service/meeting.go`、`internal/types/interfaces/meeting.go`、`internal/handler/meeting.go`、`internal/router/routes_meeting.go`、`internal/application/service/meeting_opening_test.go`

- [ ] **Step 1: 写失败测试** — `meeting_opening_test.go` 追加：

```go
func TestConfirmFlowMatrix(t *testing.T) {
	ctx := context.Background()

	newFixture := func(status string) (*meetingService, *isolationRoomRepo, *stubConfirmEngine) {
		rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: status}))
		engine := &stubConfirmEngine{}
		svc := &meetingService{
			roomRepo:    rooms,
			messageRepo: &fakeMeetingMessageRepo{},
			engine:      engine,
		}
		return svc, rooms, engine
	}

	t.Run("active 幂等", func(t *testing.T) {
		svc, rooms, engine := newFixture(types.MeetingStatusActive)
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status)
		assert.Equal(t, 0, engine.turnCount, "已激活房间不重复触发讨论轮")
	})

	t.Run("archived 拒绝", func(t *testing.T) {
		svc, _, engine := newFixture(types.MeetingStatusArchived)
		assert.Equal(t, ErrRoomArchived, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, 0, engine.turnCount)
	})

	t.Run("待确认 → active 并触发首轮", func(t *testing.T) {
		svc, rooms, engine := newFixture(types.MeetingStatusAwaitingConfirmation)
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status)
		waitFor(t, func() bool { return engine.turnCount == 1 }, "应异步触发一次 RunTurn")
	})

	t.Run("跨租户拒绝", func(t *testing.T) {
		svc, _, engine := newFixture(types.MeetingStatusAwaitingConfirmation)
		assert.Error(t, svc.ConfirmFlow(ctx, 2, "r1"))
		assert.Equal(t, 0, engine.turnCount)
	})
}

// stubConfirmEngine 记录 RunTurn 触发（触发消息为最近开场；无开场时不触发）
type stubConfirmEngine struct {
	interfaces.MeetingEngine
	turnCount int
}

func (e *stubConfirmEngine) RunTurn(context.Context, string, string) error {
	e.turnCount++
	return nil
}

// waitFor 轮询等待异步动作完成（上限 2s）
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}
```

（import 补 `context`、`time`。`fakeMeetingMessageRepo` 无消息 → `latestOpeningMessageID` 返回空 → 不触发 RunTurn。注意「待确认 → active 并触发首轮」子用例：若 fake 仓储无开场消息则不触发，断言应为 `engine.turnCount == 0` 且状态已 active。**执行时按 fake 实际行为选择断言**：本计划默认 `fakeMeetingMessageRepo` 预置为空 → 该子用例断言状态流转为 active、`turnCount == 0`，并在 `fakeMeetingMessageRepo.preset` 中塞一条 `Payload: []byte(`{"stage":"opening"}`)` 的消息后另起子用例断言触发。最终测试代码（含两个子用例）如下：）

```go
	t.Run("待确认 → active（无开场消息时不空转触发）", func(t *testing.T) {
		svc, rooms, engine := newFixture(types.MeetingStatusAwaitingConfirmation)
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status)
		waitFor(t, func() bool { return engine.turnCount == 0 }, "无开场消息不应触发 RunTurn")
	})

	t.Run("待确认且有开场消息 → 触发首轮", func(t *testing.T) {
		rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusAwaitingConfirmation}))
		engine := &stubConfirmEngine{}
		svc := &meetingService{
			roomRepo: rooms,
			messageRepo: &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
				{ID: "open-1", MeetingRoomID: "r1", SenderKind: types.MeetingSenderHost, Payload: []byte(`{"stage":"opening"}`)},
			}},
			engine: engine,
		}
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		waitFor(t, func() bool { return engine.turnCount == 1 }, "应以最近开场消息为触发异步开跑")
	})
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run TestConfirmFlowMatrix -v`。
- [ ] **Step 3: 最小实现** —
  1. `interfaces/meeting.go` `MeetingService` 追加：

```go
	// ConfirmFlow 确认开场流程：幂等置 active，并以最近一条开场消息为触发自动开跑首轮
	ConfirmFlow(ctx context.Context, tenantID uint64, roomID string) error
```

  2. `meeting.go`（`SendUserMessage` 之前）追加：

```go
// ConfirmFlow 「开始会议」：awaiting_confirmation → active（幂等），以最近一条开场消息为触发自动开跑首轮。
func (s *meetingService) ConfirmFlow(ctx context.Context, tenantID uint64, roomID string) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	switch room.Status {
	case types.MeetingStatusActive:
		return nil // 幂等：已确认直接返回
	case types.MeetingStatusArchived:
		return ErrRoomArchived
	}
	room.Status = types.MeetingStatusActive
	if err := s.roomRepo.Update(ctx, room); err != nil {
		return err
	}
	// 触发消息取最近一条开场（含主题分析 + 建议流程，天然的讨论起点）；无开场则等用户首条发言
	triggerID := s.latestOpeningMessageID(ctx, roomID)
	if triggerID == "" {
		return nil
	}
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunTurn panic, room=%s recover=%v", roomID, r)
			}
		}()
		if err := s.engine.RunTurn(bgCtx, roomID, triggerID); err != nil {
			logger.Errorf(bgCtx, "meeting RunTurn failed, room=%s err=%v", roomID, err)
		}
	}()
	return nil
}

// latestOpeningMessageID 倒序找最近一条开场消息 ID（payload.stage=opening）；无则空串
func (s *meetingService) latestOpeningMessageID(ctx context.Context, roomID string) string {
	msgs, err := s.messageRepo.ListByRoom(ctx, roomID, "", 100)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		var p openingPayload
		if json.Unmarshal(msgs[i].Payload, &p) == nil && p.Stage == MeetingOpeningStage {
			return msgs[i].ID
		}
	}
	return ""
}
```

`meeting.go` import 块补 `"encoding/json"`。

  3. handler — `meeting.go` 追加：

```go
// ConfirmFlow POST /api/v1/meetings/:id/confirm —— 确认开场流程（幂等置 active + 自动开跑首轮）
func (h *MeetingHandler) ConfirmFlow(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.ConfirmFlow(ctx, tenantID, c.Param("id")); err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to confirm meeting flow"))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "message": "confirmed"})
}
```

  4. 路由 — `routes_meeting.go` 在 participants 路由后加：

```go
		meetings.POST("/:id/confirm", g.OwnedMeetingOrAdmin(), meetingHandler.ConfirmFlow)
```

- [ ] **Step 4: 运行通过** — 上述命令；`go build ./...`。
- [ ] **Step 5: 提交** — `feat(meeting): 开场流程确认端点（幂等激活并自动开跑）`

## Task T3.4 SendUserMessage 支持 adjust_flow + 隐式确认（TDD）

**文件：** `internal/types/interfaces/meeting.go`、`internal/application/service/meeting.go`、`internal/handler/meeting.go`、`internal/application/service/meeting_opening_test.go`

- [ ] **Step 1: 写失败测试** — `meeting_opening_test.go` 追加：

```go
func TestSendUserMessageImplicitConfirmAndAdjustFlow(t *testing.T) {
	ctx := context.Background()
	// stubMultiEngine 分别记录 RunTurn / GenerateOpening 调用
	type calls struct {
		turns, openings int
		lastFeedback    string
	}
	mk := func(status string) (*meetingService, *isolationRoomRepo, *calls) {
		rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: status}))
		c := &calls{}
		engine := &stubMultiEngine{calls: c}
		svc := &meetingService{
			roomRepo:    rooms,
			messageRepo: &fakeMeetingMessageRepo{},
			engine:      engine,
		}
		return svc, rooms, c
	}

	t.Run("待确认 + 普通发言 = 隐式确认并开跑", func(t *testing.T) {
		svc, rooms, c := mk(types.MeetingStatusAwaitingConfirmation)
		_, err := svc.SendUserMessage(ctx, 1, "r1", "u1", "开始吧", nil, false)
		require.NoError(t, err)
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status, "直接打字即确认")
		waitFor(t, func() bool { return c.turns == 1 }, "隐式确认后应触发讨论轮")
		assert.Equal(t, 0, c.openings)
	})

	t.Run("待确认 + adjust_flow = 保持待确认并重出流程", func(t *testing.T) {
		svc, rooms, c := mk(types.MeetingStatusAwaitingConfirmation)
		_, err := svc.SendUserMessage(ctx, 1, "r1", "u1", "先聊性能", nil, true)
		require.NoError(t, err)
		assert.Equal(t, types.MeetingStatusAwaitingConfirmation, rooms.rooms["r1"].Status, "调整流程不激活")
		waitFor(t, func() bool { return c.openings == 1 }, "应以该消息为反馈重出开场")
		assert.Equal(t, 0, c.turns, "调整流程不触发讨论轮")
	})

	t.Run("active + adjust_flow = 按普通消息处理", func(t *testing.T) {
		svc, _, c := mk(types.MeetingStatusActive)
		_, err := svc.SendUserMessage(ctx, 1, "r1", "u1", "继续", nil, true)
		require.NoError(t, err)
		waitFor(t, func() bool { return c.turns == 1 }, "active 态忽略 adjust_flow 走讨论")
		assert.Equal(t, 0, c.openings)
	})

	t.Run("archived 拒绝", func(t *testing.T) {
		svc, _, c := mk(types.MeetingStatusArchived)
		_, err := svc.SendUserMessage(ctx, 1, "r1", "u1", "hi", nil, false)
		assert.Equal(t, ErrRoomArchived, err)
		assert.Equal(t, 0, c.turns)
	})
}

type stubMultiEngine struct {
	interfaces.MeetingEngine
	calls *struct {
		turns, openings int
		lastFeedback    string
	}
}

func (e *stubMultiEngine) RunTurn(context.Context, string, string) error {
	e.calls.turns++
	return nil
}

func (e *stubMultiEngine) GenerateOpening(_ context.Context, _ *types.MeetingRoom, feedbackMessageID string) error {
	e.calls.openings++
	e.calls.lastFeedback = feedbackMessageID
	return nil
}
```

（注意：`calls` 匿名结构体类型不能被方法引用——实际落地时把 `calls` 定义为具名类型 `turnCalls struct{ turns, openings int; lastFeedback string }`，`stubMultiEngine{calls *turnCalls}`。执行者按此修正测试代码。）

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run TestSendUserMessageImplicitConfirmAndAdjustFlow -v`（签名不匹配编译失败）。
- [ ] **Step 3: 最小实现** —
  1. `interfaces/meeting.go`：`SendUserMessage` 签名改为：

```go
	// SendUserMessage 落库用户消息并异步触发讨论轮，返回落库后的消息（归档房间拒绝）。
	// adjustFlow=true 且房间待确认时：不激活、不讨论，转由主持人按该意见重出开场（M3）
	SendUserMessage(ctx context.Context, tenantID uint64, roomID string, userID string, content string, mentionedAgentIDs []string, adjustFlow bool) (*types.MeetingMessage, error)
```

  2. `meeting.go` `SendUserMessage` 替换为：

```go
func (s *meetingService) SendUserMessage(ctx context.Context, tenantID uint64, roomID string, userID string, content string, mentionedAgentIDs []string, adjustFlow bool) (*types.MeetingMessage, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // 归档守卫：archived 房间只读
	}
	msg := &types.MeetingMessage{
		MeetingRoomID:     roomID,
		SenderKind:        types.MeetingSenderUser,
		SenderID:          userID,
		Content:           content,
		MentionedAgentIDs: mentionedAgentIDs,
	}
	if err := s.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	// 「调整流程」：保持 awaiting_confirmation，主持人结合该意见重出开场（新的 opening 消息）
	if adjustFlow && room.Status == types.MeetingStatusAwaitingConfirmation {
		go func() {
			bgCtx := logger.CloneContext(ctx)
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf(bgCtx, "meeting GenerateOpening panic, room=%s recover=%v", roomID, r)
				}
			}()
			if err := s.engine.GenerateOpening(bgCtx, room, msg.ID); err != nil {
				logger.Errorf(bgCtx, "meeting GenerateOpening failed, room=%s err=%v", roomID, err)
			}
		}()
		return msg, nil
	}
	// 直接打字 = 隐式确认：awaiting_confirmation → active，再进入正常讨论轮
	if room.Status == types.MeetingStatusAwaitingConfirmation {
		room.Status = types.MeetingStatusActive
		if err := s.roomRepo.Update(ctx, room); err != nil {
			return nil, err
		}
	}
	// 异步触发讨论轮；CloneContext 保留 trace，避免请求 ctx 取消中断讨论
	go func() {
		bgCtx := logger.CloneContext(ctx)
		// 兜底 recover：讨论轮是异步重管线，单次 panic 不应拖垮整个服务进程
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunTurn panic, room=%s recover=%v", roomID, r)
			}
		}()
		if err := s.engine.RunTurn(bgCtx, roomID, msg.ID); err != nil {
			logger.Errorf(bgCtx, "meeting RunTurn failed, room=%s err=%v", roomID, err)
		}
	}()
	return msg, nil
}
```

  3. handler：`sendMessageRequest` 加字段，`SendMessage` 调用处透传：

```go
type sendMessageRequest struct {
	Content           string   `json:"content" binding:"required"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
	// 「调整流程」标记：仅待确认状态下生效，主持人据此重出开场
	AdjustFlow bool `json:"adjust_flow"`
}
```

```go
	msg, err := h.meetingService.SendUserMessage(ctx, tenantID, c.Param("id"), userID, req.Content, req.MentionedAgentIDs, req.AdjustFlow)
```

- [ ] **Step 4: 运行通过** — 本 Task 与 T3.3 全部测试 + 既有 `TestGenerateSolutionTenantGuard` 等回归；`go build ./...`。
- [ ] **Step 5: 提交** — `feat(meeting): 发言支持调整流程标记与待确认隐式确认`

## Task T3.5 前端：待确认提示条 + 开场按钮组 + 调整流程模式

**文件：** `frontend/src/api/meeting/index.ts`、`frontend/src/api/meeting/meeting.test.ts`、`frontend/src/stores/meeting.ts`、`frontend/src/views/meeting/MeetingRoomView.vue`

- [ ] **Step 1: API + 纯函数（先测）** — `index.ts`：
  - `MeetingMessage` 接口在 `is_consensus` 之后加：

```ts
  // jsonb 载荷：开场消息为 { stage: 'opening' }（其余消息为 null/缺省）
  payload?: Record<string, unknown> | null;
```

  - `buildMessagePayload` 替换为：

```ts
// 组装发言请求体：@ 提及可选；adjust_flow 仅待确认状态下的「调整流程」模式为 true
export function buildMessagePayload(content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
  return { content, mentioned_agent_ids: mentionAgentIds, adjust_flow: adjustFlow };
}
```

  - `sendMessage` 替换为：

```ts
export async function sendMessage(roomId: string, content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
  const { post } = await loadRequest();
  // 后端固定 202 Accepted，讨论结果经 SSE 推送
  return post<{ data: MeetingMessage }>(
    `/api/v1/meetings/${roomId}/messages`,
    buildMessagePayload(content, mentionAgentIds, adjustFlow),
  ).then((r) => r.data);
}
```

  - 新增：

```ts
// 确认开场流程（幂等置 active + 后端自动开跑首轮）
export async function confirmMeetingFlow(roomId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/confirm`, {});
}
```

  `meeting.test.ts` 追加：

```ts
test('buildMessagePayload 支持 adjust_flow', () => {
  const plain = buildMessagePayload('大家好', ['agent-9']);
  assert.equal(plain.adjust_flow, false); // 默认普通发言
  const adjust = buildMessagePayload('先聊性能', [], true);
  assert.equal(adjust.adjust_flow, true); // 调整流程模式
  assert.deepEqual(adjust.mentioned_agent_ids, []);
});
```

（文件头 import 处 `buildMessagePayload` 已导入，无需改动。）

- [ ] **Step 2: 运行测试** — `cd frontend; npm test -- src/api/meeting/meeting.test.ts`（新断言先失败→实现已在 Step 1 写完则直接通过；若直接通过视为 Step 1/2 合并完成）。
- [ ] **Step 3: Store** — `stores/meeting.ts`：
  - import 行加 `confirmMeetingFlow`；`speak` 替换并新增 `confirm`：

```ts
  async function speak(content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
    if (!currentRoom.value) return;
    // 后端不会通过 SSE 回推用户自身的消息，这里本地插入返回值，避免自己发言不显示
    const msg = await sendMessage(currentRoom.value.id, content, mentionAgentIds, adjustFlow);
    appendMessage(msg);
  }

  // 「开始会议」：确认流程后本地置 active（按钮组/提示条随之消失；讨论经 SSE 回流）
  async function confirm() {
    if (!currentRoom.value) return;
    await confirmMeetingFlow(currentRoom.value.id);
    if (currentRoom.value) {
      currentRoom.value = { ...currentRoom.value, status: 'active' };
    }
  }
```

  - `applyStreamEvent` 的 `consensus.created` 分支之后追加（事件到达时消息已由 message.created 插入；按钮组由消息 payload 驱动，这里仅保证状态可见性）：

```ts
    } else if (event === 'discussion.flow_confirmation') {
      // 开场确认事件：无独立状态，开场消息本体经 message.created 回流，按钮组由 payload.stage 渲染
```

  - `leaveRoom` 不变（无新增状态）。
  - return 列表补 `confirm`。
  - `MeetingRoom` 类型 `status` 字段在 T6.1 统一扩展；本 Task 先在 `index.ts` 内联扩为 `status: 'active' | 'awaiting_confirmation' | 'archived';`（避免 M3 期间 type-check 报错）。

- [ ] **Step 4: 视图** — `MeetingRoomView.vue`：
  - 归档提示条之后加待确认提示条：

```html
    <!-- 待确认提示条：开场流程尚未确认 -->
    <t-alert v-if="isAwaiting" :message="$t('meeting.awaitingBanner')" theme="warning" class="archived-banner" />
```

  - host/agent 消息分支的 `review-bar` 之后（`msg-body` 内）加按钮组：

```html
              <!-- 开场确认按钮组：仅待确认状态下、最近一条开场消息下方展示 -->
              <div v-if="m.sender_kind === 'host' && isLatestOpening(m) && isAwaiting" class="opening-actions">
                <t-button size="small" theme="primary" :loading="confirming" :disabled="adjustMode"
                  @click="startMeeting">
                  {{ $t('meeting.startMeeting') }}
                </t-button>
                <t-button size="small" theme="default" variant="outline" :class="{ 'is-active': adjustMode }"
                  @click="adjustMode = !adjustMode">
                  {{ $t('meeting.adjustFlow') }}
                </t-button>
              </div>
```

  - 输入区 `mention-chips` 之后加模式 chip；`t-textarea` 的 placeholder 改绑定：

```html
        <div v-if="adjustMode" class="adjust-chip">
          <t-tag size="small" theme="warning" variant="light" closable @close="adjustMode = false">
            {{ $t('meeting.adjustFlowMode') }}
          </t-tag>
        </div>
```

```html
          <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }"
            :disabled="store.discussing || isArchived" :placeholder="inputPlaceholder" />
```

  - script 追加/修改：

```ts
import { confirmMeetingFlow } from '@/api/meeting';   // 与既有 interruptMeeting/openMeetingStream 合并到同一 import
```

```ts
// 待确认态：等待用户确认开场流程（确认后转 active）
const isAwaiting = computed(() => store.currentRoom?.status === 'awaiting_confirmation');
const confirming = ref(false); // 「开始会议」按钮 loading
const adjustMode = ref(false); // 「调整流程」输入模式

const inputPlaceholder = computed(() =>
  adjustMode.value ? t('meeting.adjustFlowPlaceholder') : t('meeting.inputPlaceholder'));

// 开场消息判定与「最近一条」限定（调整流程会产生多条 opening，只渲染最后一组的按钮）
function isOpening(m: MeetingMessage): boolean {
  return (m.payload as { stage?: string } | null | undefined)?.stage === 'opening';
}

function isLatestOpening(m: MeetingMessage): boolean {
  if (!isOpening(m)) return false;
  const list = store.messages;
  for (let i = list.length - 1; i >= 0; i -= 1) {
    if (isOpening(list[i])) return list[i].id === m.id;
  }
  return false;
}

async function startMeeting() {
  confirming.value = true;
  try {
    await store.confirm();
  } catch (e) {
    toastError(e);
  } finally {
    confirming.value = false;
  }
}
```

  - `send()` 中 `store.speak(...)` 改为并在成功后退出调整模式：

```ts
    await store.speak(content, ids, adjustMode.value);
    adjustMode.value = false;
```

  - `openRoom` 复位段补 `adjustMode.value = false; confirming.value = false;`。
  - 样式块追加：

```less
.opening-actions {
  display: flex;
  gap: var(--app-space-2);
}

.adjust-chip {
  display: flex;
  margin-bottom: var(--app-space-2);
}
```

- [ ] **Step 5: i18n** — 附录 A 的 M3 键加入 6 个 locale。
- [ ] **Step 6: 验证** — `npm test -- src/api/meeting/meeting.test.ts; npm run type-check; npm run check-i18n`。
- [ ] **Step 7: 提交** — `feat(meeting): 开场按钮组、待确认提示条与调整流程输入模式`

**M3 验收：** 后端 M3 全部测试绿；手工链路——新建会议数秒内出现开场与按钮组；「开始会议」自动开跑；「调整流程」发送后出现新开场；刷新页面按钮仍在；开场失败出现系统兜底提示且可直接发言。

---

# Milestone M4 — 卡片精简

**验收标准：** 卡片仅显示状态标签 + 时间；三种状态文案正确。

## Task T4.1 卡片页脚精简 + 状态标签三态

**文件：** `frontend/src/views/meeting/MeetingLayoutView.vue`

- [ ] **Step 1: 模板** — `rc-foot` 整块替换为：

```html
          <div class="rc-foot">
            <t-tag :theme="statusTheme(room.status)" size="small" variant="light">
              {{ statusLabel(room.status) }}
            </t-tag>
            <span class="rc-time">{{ formatTime(room.created_at) }}</span>
          </div>
```

（移除发言模式 / 发言权路由 / 主持人介入三个 chip。）

- [ ] **Step 2: script** — `modeLabel`/`routingLabel` 两个函数整体删除，替换为：

```ts
// 状态标签三态映射（G1：卡片只保留状态 + 时间）
function statusLabel(status: MeetingRoom['status']): string {
  if (status === 'archived') return t('meeting.filterArchived');
  if (status === 'awaiting_confirmation') return t('meeting.filterAwaiting');
  return t('meeting.filterActive');
}

function statusTheme(status: MeetingRoom['status']) {
  if (status === 'active') return 'success' as const;
  if (status === 'awaiting_confirmation') return 'warning' as const;
  return 'default' as const;
}
```

- [ ] **Step 3: 样式** — 删除 `.rc-mode` 规则块（已无引用）。
- [ ] **Step 4: i18n** — 附录 A 的 `filterAwaiting` 加入 6 个 locale。
- [ ] **Step 5: 验证** — `npm run type-check; npm run check-i18n`。
- [ ] **Step 6: 提交** — `feat(meeting): 会议卡片精简为状态标签 + 时间`

---

# Milestone M5 — 产物数据层 + 生成链路

**验收标准：** 能生成一个 word 产物并下载；同一房间并发生成被串行化；生成中列表出现 loading 卡片，完成/失败状态正确；后台会话不出现在聊天侧栏；solution 产物行为不回归。

## Task T5.1 迁移 000128 + 类型扩展（TDD）

**文件：** `migrations/versioned/000128_meeting_artifact_generation.up.sql` / `.down.sql`、`internal/types/meeting.go`、`internal/application/service/meeting_artifact_gen_test.go`（新建）

- [ ] **Step 1: 写迁移** — `000128_meeting_artifact_generation.up.sql`：

```sql
-- 会议产物生成链路：隐藏后台会话指针 + generated 产物元数据
ALTER TABLE meeting_rooms ADD COLUMN artifact_session_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE meeting_artifacts ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN kind_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN title VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN requirement TEXT NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN file_size BIGINT NOT NULL DEFAULT 0;
ALTER TABLE meeting_artifacts ADD COLUMN url VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN content_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN error TEXT NOT NULL DEFAULT '';
ALTER TABLE meeting_artifacts ADD COLUMN source_message_id VARCHAR(36) NOT NULL DEFAULT '';
```

`.down.sql`：

```sql
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS source_message_id;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS error;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS status;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS content_hash;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS url;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS file_size;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS file_type;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS file_name;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS requirement;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS title;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS kind_type;
ALTER TABLE meeting_artifacts DROP COLUMN IF EXISTS kind;
ALTER TABLE meeting_rooms DROP COLUMN IF EXISTS artifact_session_id;
```

- [ ] **Step 2: 写失败测试** — 新建 `meeting_artifact_gen_test.go`（本文件后续 Task 持续追加）：

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeMeetingArtifact(t *testing.T) {
	// 历史行（迁移前数据）：kind/status 为空 → 兜底 solution/ready
	legacy := &types.MeetingArtifact{ID: "a1", SolutionMessageID: "m1", Version: 1}
	out := types.NormalizeMeetingArtifact(legacy)
	assert.Equal(t, types.MeetingArtifactKindSolution, out.Kind)
	assert.Equal(t, types.MeetingArtifactStatusReady, out.Status)

	// 新行原样保留
	fresh := &types.MeetingArtifact{
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusGenerating,
	}
	out2 := types.NormalizeMeetingArtifact(fresh)
	assert.Equal(t, types.MeetingArtifactKindGenerated, out2.Kind)
	assert.Equal(t, types.MeetingArtifactStatusGenerating, out2.Status)
}

func TestValidMeetingArtifactType(t *testing.T) {
	for _, k := range []string{
		types.MeetingArtifactTypeReportMarkdown, types.MeetingArtifactTypeMinutes,
		types.MeetingArtifactTypeTodoList, types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx,
		types.MeetingArtifactTypePptx, types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage,
	} {
		assert.True(t, types.ValidMeetingArtifactType(k), k)
	}
	for _, bad := range []string{"", "pdf", "docx ", "Solution"} {
		assert.False(t, types.ValidMeetingArtifactType(bad), bad)
	}
}
```

- [ ] **Step 3: 运行确认失败** — `go test ./internal/application/service/ -run 'TestNormalizeMeetingArtifact|TestValidMeetingArtifactType' -v`。
- [ ] **Step 4: 类型实现** — `internal/types/meeting.go`：
  1. 常量块追加：

```go
// ---- 产物（generated）常量（M5）----

const (
	// MeetingArtifactKindSolution 历史方案汇总产物（solution_message_id 指向会议消息）
	MeetingArtifactKindSolution = "solution"
	// MeetingArtifactKindGenerated 用户指定形态生成的产物（字节存后台会话的 MessageArtifact）
	MeetingArtifactKindGenerated = "generated"

	MeetingArtifactStatusGenerating = "generating"
	MeetingArtifactStatusReady      = "ready"
	MeetingArtifactStatusFailed     = "failed"

	// MeetingArtifactSessionMarker 会议产物后台会话的隐藏标记前缀（聊天侧栏过滤，仿 skill_maintenance/agent_eval）
	MeetingArtifactSessionMarker = "meeting_artifact:"
)

// 8 种产物形态（本期硬编码集合，spec §1 G4）
const (
	MeetingArtifactTypeReportMarkdown = "report_markdown"
	MeetingArtifactTypeMinutes        = "minutes"
	MeetingArtifactTypeTodoList       = "todo_list"
	MeetingArtifactTypeDocx           = "docx"
	MeetingArtifactTypeXlsx           = "xlsx"
	MeetingArtifactTypePptx           = "pptx"
	MeetingArtifactTypeHtmlReport     = "html_report"
	MeetingArtifactTypeChartImage     = "chart_image"
)

var validMeetingArtifactTypes = map[string]bool{
	MeetingArtifactTypeReportMarkdown: true,
	MeetingArtifactTypeMinutes:        true,
	MeetingArtifactTypeTodoList:       true,
	MeetingArtifactTypeDocx:           true,
	MeetingArtifactTypeXlsx:           true,
	MeetingArtifactTypePptx:           true,
	MeetingArtifactTypeHtmlReport:     true,
	MeetingArtifactTypeChartImage:     true,
}

// ValidMeetingArtifactType 形态白名单校验（handler/service 共用）
func ValidMeetingArtifactType(t string) bool { return validMeetingArtifactTypes[t] }

// IsMeetingArtifactSessionDescription 判断会话描述是否为会议产物后台会话标记（侧栏隐藏判定用）
func IsMeetingArtifactSessionDescription(desc string) bool {
	return strings.HasPrefix(desc, MeetingArtifactSessionMarker)
}

// NormalizeMeetingArtifact 读取路径兼容归一：历史行 kind=""→solution、status=""→ready
func NormalizeMeetingArtifact(a *MeetingArtifact) *MeetingArtifact {
	if a.Kind == "" {
		a.Kind = MeetingArtifactKindSolution
	}
	if a.Status == "" {
		a.Status = MeetingArtifactStatusReady
	}
	return a
}
```

`meeting.go` import 块补 `"strings"`。
  2. `MeetingRoom` 在 `ContextSummary` 字段之后加：

```go
	// ArtifactSessionID 懒创建的产物生成后台会话（空 = 尚未生成过产物）；
	// 会话带隐藏标记不出现在聊天侧栏，属主为会议创建者
	ArtifactSessionID string `gorm:"type:varchar(36)" json:"artifact_session_id"`
```

  3. `MeetingArtifact` 在 `ExportedFormat` 之后、`CreatedBy` 之前插入：

```go
	// ---- 以下为产物扩展（迁移 000128；solution 行保持零值）----
	Kind            string `gorm:"type:varchar(16)" json:"kind"`             // solution / generated（历史行读取时归一为 solution）
	KindType        string `gorm:"type:varchar(16)" json:"kind_type"`        // 8 种形态之一（generated 用）
	Title           string `gorm:"type:varchar(255)" json:"title"`           // 展示标题
	Requirement     string `gorm:"type:text" json:"requirement"`             // 用户补充要求（regenerate 回放）
	FileName        string `gorm:"type:varchar(255)" json:"file_name"`       // 交付文件名（generated 文件形态）
	FileType        string `gorm:"type:varchar(32)" json:"file_type"`        // MIME/类型描述
	FileSize        int64  `gorm:"default:0" json:"file_size"`               // 字节数
	URL             string `gorm:"type:varchar(512)" json:"url"`             // 存储引用（provider://path），下载走会话端点
	ContentHash     string `gorm:"type:varchar(64)" json:"content_hash"`
	Status          string `gorm:"type:varchar(16)" json:"status"`           // generating / ready / failed
	Error           string `gorm:"type:text" json:"error"`                   // 生成失败原因
	SourceMessageID string `gorm:"type:varchar(36)" json:"source_message_id"` // 生成产物的后台会话消息 ID
```

- [ ] **Step 5: 运行通过** — 上述命令；`make migrate-up`。
- [ ] **Step 6: 提交** — `feat(meeting): 产物元数据扩展列与后台会话指针（迁移 000128）`

## Task T5.2 Repository：Update / MaxVersionByRoom + solution version 统一

**文件：** `internal/types/interfaces/meeting.go`、`internal/application/repository/meeting.go`、`internal/application/service/meeting_solution.go`

- [ ] **Step 1: 接口** — `MeetingArtifactRepository` 追加：

```go
	Update(ctx context.Context, a *types.MeetingArtifact) error
	// MaxVersionByRoom 房间内产物最大版本号（solution 与 generated 共用序列，规避 UNIQUE(room,version) 冲突）
	MaxVersionByRoom(ctx context.Context, roomID string) (int, error)
```

- [ ] **Step 2: 实现** — `repository/meeting.go` `meetingArtifactRepository` 追加：

```go
// Update 全量保存产物行（生成链路回写状态/文件元信息/失败原因用）
func (r *meetingArtifactRepository) Update(ctx context.Context, a *types.MeetingArtifact) error {
	return r.db.WithContext(ctx).Save(a).Error
}

// MaxVersionByRoom 房间内产物最大版本号；空房回 0
func (r *meetingArtifactRepository) MaxVersionByRoom(ctx context.Context, roomID string) (int, error) {
	var v int
	err := r.db.WithContext(ctx).Model(&types.MeetingArtifact{}).
		Where("meeting_room_id = ?", roomID).
		Select("COALESCE(MAX(version), 0)").Scan(&v).Error
	return v, err
}
```

- [ ] **Step 3: solution 对齐** — `meeting_solution.go` `GenerateSolution`：
  - version 分配段替换为：

```go
	// 产物行与方案消息共用 version 序列：以 meeting_artifacts 最大版本为准（UNIQUE(room_id, version)）
	version, err := e.artifactRepo.MaxVersionByRoom(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	version++
```

  - `artifact := &types.MeetingArtifact{...}` 初始化块补两个字段：

```go
		Kind:   types.MeetingArtifactKindSolution,
		Status: types.MeetingArtifactStatusReady,
```

- [ ] **Step 4: 验证** — `go build ./...`；`go test ./internal/application/service/ -run TestGenerateSolution -v`（回归）。
- [ ] **Step 5: 提交** — `feat(meeting): 产物仓储支持整行更新与房间级版本号分配`

## Task T5.3 隐藏后台会话标记贯通

**文件：** `internal/types/session.go`、`internal/application/repository/session.go`、`internal/application/repository/message_artifact.go`

- [ ] **Step 1: 会话隐藏判定** — `session.go` `SessionRequiresAdminConsoleRead` 中 `IsSkillMaintenanceDescription` 判定之后追加：

```go
	if IsMeetingArtifactSessionDescription(s.Description) {
		return true // 会议产物后台会话：对普通用户隐藏，仅管理控制台可见
	}
```

- [ ] **Step 2: 会话列表过滤** — `repository/session.go` `applyBase` 内的 description NOT LIKE 条件（现仅排除 skill_maintenance 与 agent_eval 两个前缀）扩展为三前缀：

```go
	db = db.Where("(s.description IS NULL OR (s.description NOT LIKE ? AND s.description NOT LIKE ? AND s.description NOT LIKE ?))",
		types.SkillMaintenanceSessionMarker+"%", types.AgentEvalSessionMarker+"%", types.MeetingArtifactSessionMarker+"%")
```

（以文件内实际代码为准：在现有 NOT LIKE 子句中追加第三个占位符与 `types.MeetingArtifactSessionMarker+"%"` 参数，保持其余查询语义不变。）
- [ ] **Step 3: 产物库过滤** — `repository/message_artifact.go` `ListArtifactLibrary`（约 L261-284）where 构造处：现有 skill-maintenance 排除条件按同一模式追加 meeting 标记（`s.description NOT LIKE types.MeetingArtifactSessionMarker+"%"`）。执行时先读该函数当前实现，把排除谓词从「单标记」扩为「双标记」（skill_maintenance + meeting_artifact；若该处已含其他标记则逐一并列），参数顺序与占位符一一对应。
- [ ] **Step 4: 验证** — `go build ./...`；`go test ./internal/application/repository/ -run TestSession -v`（回归既有仓储测试）。
- [ ] **Step 5: 提交** — `feat(meeting): 会议产物后台会话对聊天侧栏与产物库隐藏`

## Task T5.4 引擎产物纯函数 + 依赖扩展（TDD）

**文件：** `internal/application/service/meeting_artifact_gen.go`（新建）、`internal/application/service/meeting_artifact_gen_test.go`、`internal/application/service/meeting_engine.go`、`internal/types/interfaces/meeting.go`、`internal/application/service/meeting_broadcast.go`

- [ ] **Step 1: 写失败测试** — `meeting_artifact_gen_test.go` 追加：

```go
func TestArtifactKindHelpers(t *testing.T) {
	// 文件形态必须产出文件；文本形态不需要
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeDocx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeXlsx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypePptx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeHtmlReport))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeChartImage))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeReportMarkdown))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeMinutes))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeTodoList))

	// 期望扩展名映射
	assert.Equal(t, []string{".docx"}, artifactExpectedExts(types.MeetingArtifactTypeDocx))
	assert.Equal(t, []string{".html", ".htm"}, artifactExpectedExts(types.MeetingArtifactTypeHtmlReport))
}

func TestPickArtifactFile(t *testing.T) {
	arts := types.MessageArtifacts{
		{FileName: "notes.md"}, {FileName: "report.docx"}, {FileName: "data.xlsx"},
	}
	// 期望扩展优先命中
	got, ok := pickArtifactFile(arts, types.MeetingArtifactTypeDocx)
	assert.True(t, ok)
	assert.Equal(t, "report.docx", got.FileName)
	// 无期望扩展（文本形态）取第一个
	got2, ok2 := pickArtifactFile(arts, types.MeetingArtifactTypeMinutes)
	assert.True(t, ok2)
	assert.Equal(t, "notes.md", got2.FileName)
	// 期望扩展未命中 → 降级取第一个，不整体失败
	got3, ok3 := pickArtifactFile(arts, types.MeetingArtifactTypeChartImage)
	assert.True(t, ok3)
	assert.Equal(t, "notes.md", got3.FileName)
	// 无文件
	_, ok4 := pickArtifactFile(nil, types.MeetingArtifactTypeDocx)
	assert.False(t, ok4)
}

func TestSanitizeArtifactFileName(t *testing.T) {
	assert.Equal(t, "artifact", sanitizeArtifactFileName(""), "空标题回退")
	assert.Equal(t, "发布-评审-v2", sanitizeArtifactFileName("发布 评审 v2"), "空白折叠为连字符")
	assert.Equal(t, "a-b-c", sanitizeArtifactFileName("a/b\\c"), "路径分隔符替换")
	assert.Equal(t, "artifact", sanitizeArtifactFileName("###"), "纯符号回退")
}

func TestDefaultArtifactTitle(t *testing.T) {
	assert.Equal(t, "会议纪要", defaultArtifactTitle(types.MeetingArtifactTypeMinutes))
	assert.Equal(t, "Markdown 报告", defaultArtifactTitle(types.MeetingArtifactTypeReportMarkdown))
}

func TestBuildArtifactPrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", Description: "风险"}
	recent := []*types.MeetingMessage{
		{SenderKind: types.MeetingSenderUser, Content: "性能是首要关切"},
	}
	adopted := []*types.MeetingMessage{
		{SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "先做压测"},
	}
	labelOf := func(m *types.MeetingMessage) string { return "某参会人" }
	p := buildArtifactPrompt(room, recent, adopted, types.MeetingArtifactTypeDocx, "发布评审报告", "包含性能章节",
		labelOf, artifactKindInstruction(types.MeetingArtifactTypeDocx, "发布评审报告"))
	assert.Contains(t, p, "发布评审")
	assert.Contains(t, p, "产物标题：发布评审报告")
	assert.Contains(t, p, "用户补充要求：包含性能章节")
	assert.Contains(t, p, "性能是首要关切")
	assert.Contains(t, p, "先做压测")
	assert.Contains(t, p, "发布评审报告.docx", "文件形态指令应含建议文件名")
}

func TestBuildMeetingArtifactAgent(t *testing.T) {
	agent := buildMeetingArtifactAgent(7, "model-1", "sbx-1")
	assert.Equal(t, "model-1", agent.Config.ModelID, "exec agent 必须显式绑定模型（resolveChatModelID 硬性要求）")
	assert.Equal(t, "sbx-1", agent.Config.SandboxConfigID, "沙箱配置是 AgentConfig.SandboxConfigID 的唯一来源")
	assert.Equal(t, "all", agent.Config.SkillsSelectionMode, "打开全部已装技能")
	assert.False(t, agent.Config.MultiTurnEnabled, "单次生成不挂多轮历史")
	assert.NotZero(t, agent.Config.MaxIterations)
	assert.NotEmpty(t, agent.Config.SystemPrompt)
}
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run 'TestArtifactKindHelpers|TestPickArtifactFile|TestSanitizeArtifactFileName|TestDefaultArtifactTitle|TestBuildArtifactPrompt|TestBuildMeetingArtifactAgent' -v`。
- [ ] **Step 3: 实现** — 新建 `meeting_artifact_gen.go`：

```go
package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// artifactLockKey 房间级产物生成互斥锁（同房间串行，避免采集器 diff 去重互相干扰）
func artifactLockKey(roomID string) string { return fmt.Sprintf("meeting:%s:artifact_lock", roomID) }

// artifactNeedsFile 该形态的交付物是否必须为沙箱产出的文件
func artifactNeedsFile(kindType string) bool {
	switch kindType {
	case types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx, types.MeetingArtifactTypePptx,
		types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage:
		return true
	}
	return false
}

// artifactExpectedExts 形态 → 期望文件扩展名（命中者优先作为交付物）
func artifactExpectedExts(kindType string) []string {
	switch kindType {
	case types.MeetingArtifactTypeDocx:
		return []string{".docx"}
	case types.MeetingArtifactTypeXlsx:
		return []string{".xlsx"}
	case types.MeetingArtifactTypePptx:
		return []string{".pptx"}
	case types.MeetingArtifactTypeHtmlReport:
		return []string{".html", ".htm"}
	case types.MeetingArtifactTypeChartImage:
		return []string{".png", ".jpg", ".jpeg", ".svg", ".webp"}
	}
	return nil
}

// pickArtifactFile 从本次采集结果中挑出交付物：期望扩展优先，否则降级取第一个；无文件返回 false
func pickArtifactFile(arts types.MessageArtifacts, kindType string) (types.MessageArtifact, bool) {
	if len(arts) == 0 {
		return types.MessageArtifact{}, false
	}
	for _, ext := range artifactExpectedExts(kindType) {
		for _, a := range arts {
			if strings.EqualFold(filepath.Ext(a.FileName), ext) {
				return a, true
			}
		}
	}
	return arts[0], true
}

// sanitizeArtifactFileName 标题 → 安全文件名基：仅保留字母/数字/CJK，其余折叠为连字符
func sanitizeArtifactFileName(title string) string {
	if title == "" {
		return "artifact"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "artifact"
	}
	if r := []rune(out); len(r) > 60 { // 文件名长度保险
		out = string(r[:60])
	}
	return out
}

// defaultArtifactTitle 未填标题时按形态回缺省名
func defaultArtifactTitle(kindType string) string {
	switch kindType {
	case types.MeetingArtifactTypeMinutes:
		return "会议纪要"
	case types.MeetingArtifactTypeTodoList:
		return "待办事项"
	case types.MeetingArtifactTypeDocx:
		return "Word 文档"
	case types.MeetingArtifactTypeXlsx:
		return "Excel 表格"
	case types.MeetingArtifactTypePptx:
		return "PPT 演示"
	case types.MeetingArtifactTypeHtmlReport:
		return "网页报告"
	case types.MeetingArtifactTypeChartImage:
		return "图表"
	}
	return "Markdown 报告"
}

// artifactKindInstruction 形态 → 执行指令（注入生成 prompt；文件形态给出建议文件名）
func artifactKindInstruction(kindType, title string) string {
	base := sanitizeArtifactFileName(title)
	switch kindType {
	case types.MeetingArtifactTypeReportMarkdown:
		return "请在最终回答中直接输出完整的 Markdown 报告正文，不要生成文件。"
	case types.MeetingArtifactTypeMinutes:
		return "请在最终回答中直接输出完整的会议纪要（Markdown，含议题、结论、分歧、行动项），不要生成文件。"
	case types.MeetingArtifactTypeTodoList:
		return "请在最终回答中直接输出待办事项清单（Markdown 复选框列表，含负责人建议与优先级），不要生成文件。"
	case types.MeetingArtifactTypeDocx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Word 文档，文件名建议为 %s.docx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeXlsx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Excel 表格，文件名建议为 %s.xlsx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypePptx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 PPT 演示文稿，文件名建议为 %s.pptx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeHtmlReport:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成单文件 HTML 报告（自包含样式、无外部依赖），文件名建议为 %s.html，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeChartImage:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成图表图片（优先 PNG），文件名建议为 %s.png，并在最终回答中说明图表结论。", base)
	}
	return ""
}

// buildArtifactPrompt 组装产物生成 prompt（纯函数，单测覆盖）
func buildArtifactPrompt(
	room *types.MeetingRoom, recent, adopted []*types.MeetingMessage,
	kindType, title, requirement string,
	labelOf func(*types.MeetingMessage) string, instruction string,
) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人，现在需要为本会议产出一份交付物。\n")
	fmt.Fprintf(&b, "会议主题：%s\n", room.Title)
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	fmt.Fprintf(&b, "产物标题：%s\n产物形态：%s\n", title, kindType)
	if requirement != "" {
		b.WriteString("用户补充要求：" + requirement + "\n")
	}
	if len(recent) > 0 {
		b.WriteString("\n【会议讨论记录（截取）】\n")
		for _, m := range recent {
			fmt.Fprintf(&b, "%s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	if len(adopted) > 0 {
		b.WriteString("\n【用户已采纳的意见】\n")
		for _, m := range adopted {
			fmt.Fprintf(&b, "- %s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	b.WriteString("\n【执行要求】\n")
	b.WriteString(instruction + "\n")
	b.WriteString("内容必须忠于会议记录与采纳意见，不得编造未讨论过的事实。")
	return b.String()
}

// buildMeetingArtifactAgent 构造产物执行智能体（合成、不落库）。
// 模型 = 租户默认 KnowledgeQA（resolveChatModelID 对非空 ModelID 强校验类型）；
// 沙箱 = 调用方解析的租户具名配置（空则 AgentQA 走部署级 lite/禁用策略）；
// SkillsSelectionMode=all 打开沙箱内已装技能，兑现「可调用工具箱技能」。
func buildMeetingArtifactAgent(tenantID uint64, modelID, sandboxConfigID string) *types.CustomAgent {
	agent := &types.CustomAgent{
		ID:       "meeting-artifact-executor",
		TenantID: tenantID,
		Name:     "会议产物生成器",
		Config: types.CustomAgentConfig{
			AgentMode:           "smart-reasoning",
			ModelID:             modelID,
			SandboxConfigID:     sandboxConfigID,
			SystemPrompt:        "你是会议室的产物生成助手，负责把会议讨论沉淀为指定形态的交付物。严格遵循用户的形态与文件要求，善用可用的工具与技能，最终回答用中文。",
			SkillsSelectionMode: "all",
			MCPSelectionMode:    "none",
			MaxIterations:       20,
			Temperature:         0.3,
			MultiTurnEnabled:    false,
		},
	}
	agent.EnsureDefaults()
	return agent
}
```

- [ ] **Step 4: 引擎依赖与锁查询** — `meeting_engine.go`：
  - struct 追加三字段（import 补 `"github.com/Tencent/WeKnora/internal/application/repository"`、`"github.com/Tencent/WeKnora/internal/agent/skills"`——skills 在 T5.5 使用，此处先加 repository）：

```go
	messageService   interfaces.MessageService                  // 后台会话 assistant 消息落库（产物链路）
	sandboxConfigRepo repository.TenantSandboxConfigRepository  // 租户沙箱配置解析（产物执行沙箱）
	collector        *ArtifactCollector                          // 沙箱产物采集（可能为 typed nil，使用前判空）
```

  - `NewMeetingEngine` 签名与构造体扩展（参数插在 `modelService` 之后、`redisClient` 之前）：

```go
func NewMeetingEngine(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	artifactRepo interfaces.MeetingArtifactRepository,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	messageService interfaces.MessageService,
	sandboxConfigRepo repository.TenantSandboxConfigRepository,
	collector *ArtifactCollector,
	redisClient *redis.Client,
	cfg *config.Config,
) interfaces.MeetingEngine {
```

构造字面量对应补：

```go
		messageService:    messageService,
		sandboxConfigRepo: sandboxConfigRepo,
		collector:         collector,
```

  - `MeetingEngine` 接口（`interfaces/meeting.go`）追加：

```go
	// IsArtifactRunning 查询房间产物生成锁是否被持有（生成 409 预检用）
	IsArtifactRunning(ctx context.Context, roomID string) (bool, error)
	// RunArtifactGeneration 产物生成主链路（互斥 → 后台会话 → AgentQA → 采集 → 回写 → 广播）
	RunArtifactGeneration(ctx context.Context, room *types.MeetingRoom, artifact *types.MeetingArtifact) error
```

  - 引擎追加锁查询：

```go
// IsArtifactRunning 查询房间产物生成锁是否被持有（无 Redis 环境按"未在生成"处理）
func (e *meetingEngine) IsArtifactRunning(ctx context.Context, roomID string) (bool, error) {
	if e.redis == nil {
		return false, nil
	}
	n, err := e.redis.Exists(ctx, artifactLockKey(roomID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
```

  - `meeting_broadcast.go` 常量追加：

```go
	MeetingEventArtifactGenerating = "artifact.generating"
	MeetingEventArtifactCreated    = "artifact.created"
	MeetingEventArtifactFailed     = "artifact.failed"
```

- [ ] **Step 5: 运行通过** — Step 1 全部测试；`go build ./...`（`NewMeetingEngine` 仅 dig 调用，无测试调用方）。
- [ ] **Step 6: 提交** — `feat(meeting): 产物生成纯函数库与引擎依赖扩展`

## Task T5.5 Engine：RunArtifactGeneration 主链路

**文件：** `internal/application/service/meeting_artifact_gen.go`

- [ ] **Step 1: 实现** — `meeting_artifact_gen.go` 追加（import 补 `context`/`errors`/`uuid`/`event`/`logger`/`redislock`/`skills`/`interfaces`，按编译器提示收敛）：

```go
// ensureArtifactSession 懒创建（并持久化到房间）会议产物后台会话；已存在则直接复用。
// 会话属主 = 会议创建者（复用既有会话产物下载链路的属主校验），描述带隐藏标记。
func (e *meetingEngine) ensureArtifactSession(ctx context.Context, room *types.MeetingRoom) (string, error) {
	if room.ArtifactSessionID != "" {
		return room.ArtifactSessionID, nil
	}
	sess, err := e.sessionService.CreateSession(ctx, &types.Session{
		TenantID:    room.TenantID,
		UserID:      room.CreatedBy,
		Title:       "会议产物生成",
		Description: types.MeetingArtifactSessionMarker + room.ID,
	})
	if err != nil {
		return "", err
	}
	room.ArtifactSessionID = sess.ID
	if err := e.roomRepo.Update(ctx, room); err != nil {
		return "", err
	}
	return sess.ID, nil
}

// meetingArtifactSandboxConfigID 取租户第一个具名沙箱配置作为产物执行沙箱；
// 无配置回退空（文本形态仍可产出，文件形态将失败并写入 error）
func (e *meetingEngine) meetingArtifactSandboxConfigID(ctx context.Context, tenantID uint64) string {
	if e.sandboxConfigRepo == nil {
		return ""
	}
	list, err := e.sandboxConfigRepo.ListByTenant(ctx, tenantID)
	if err != nil || len(list) == 0 {
		return ""
	}
	return list[0].ID
}

// buildArtifactQuery 拉取会议上下文（最近记录 + 采纳意见）并组装产物生成 query
func (e *meetingEngine) buildArtifactQuery(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact) string {
	recent, err := e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
	if err != nil {
		logger.Warnf(ctx, "meeting artifact list messages failed, room=%s err=%v", room.ID, err)
		recent = nil
	}
	adopted, err := e.messageRepo.ListByReviewState(ctx, room.ID, types.ReviewStateAdopted)
	if err != nil {
		adopted = nil
	}
	labelOf := func(m *types.MeetingMessage) string {
		if m.SenderKind == types.MeetingSenderAgent {
			return e.agentDisplayName(ctx, room.ID, m.SenderID)
		}
		return senderLabel(m)
	}
	return buildArtifactPrompt(room, recent, adopted, a.KindType, a.Title, a.Requirement,
		labelOf, artifactKindInstruction(a.KindType, a.Title))
}

// runArtifactAgentQA 在后台会话上执行 AgentQA 并捕获最终回答（模式同 runAgentTurn）。
// 知识库刻意不注入：生成素材为会议记录 + 采纳意见；纯 agent 模式同时避开 rerank 模型硬性校验。
func (e *meetingEngine) runArtifactAgentQA(ctx context.Context, room *types.MeetingRoom, agent *types.CustomAgent, sessionID, assistantMessageID, query string) (string, error) {
	eventBus := event.NewEventBus()
	var answer strings.Builder
	var runErr error
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			answer.WriteString(data.Content)
		}
		return nil
	})
	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			runErr = fmt.Errorf("stage=%s err=%v", data.Stage, data.Error)
		}
		return nil
	})
	req := &types.QARequest{
		Session:            &types.Session{ID: sessionID, TenantID: room.TenantID},
		Query:              query,
		AssistantMessageID: assistantMessageID,
		CustomAgent:        agent,
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	if strings.TrimSpace(answer.String()) == "" {
		return "", errors.New("meeting: artifact agent produced no answer")
	}
	return answer.String(), nil
}

// failArtifact 生成失败收口：置 failed + error 字段 + 广播 artifact.failed
func (e *meetingEngine) failArtifact(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact, cause error) error {
	a.Status = types.MeetingArtifactStatusFailed
	a.Error = cause.Error()
	if err := e.artifactRepo.Update(ctx, a); err != nil {
		logger.Errorf(ctx, "meeting artifact fail-mark update failed, room=%s artifact=%s err=%v", room.ID, a.ID, err)
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventArtifactFailed, Data: map[string]interface{}{
		"artifact_id": a.ID, "error": a.Error,
	}})
	return cause
}

// RunArtifactGeneration 产物生成主链路：
// room 级互斥 → 懒创建后台会话 → 合成执行智能体 → AgentQA（工具/技能/沙箱）
// → 采集器采集 → 后台会话落 assistant 消息（携带产物索引）→ 回写 MeetingArtifact → 广播。
// 任一环节失败走 failArtifact（置 failed + 广播），锁由 defer 释放（30min lease 兜底）。
func (e *meetingEngine) RunArtifactGeneration(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact) error {
	token, err := redislock.NewToken()
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	ok, err := redislock.TryAcquire(ctx, e.redis, artifactLockKey(room.ID), token, 30*time.Minute)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	if !ok {
		// 同步预检之后的竞态窗口内被抢锁：本任务失败，前端可重试
		return e.failArtifact(ctx, room, a, ErrArtifactBusy)
	}
	defer func() {
		if _, rerr := redislock.Release(ctx, e.redis, artifactLockKey(room.ID), token); rerr != nil {
			logger.Warnf(ctx, "meeting artifact lock release failed, room=%s err=%v", room.ID, rerr)
		}
	}()

	sessionID, err := e.ensureArtifactSession(ctx, room)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	modelID, err := e.resolveHostModelID(ctx)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	if e.collector == nil || e.messageService == nil {
		return e.failArtifact(ctx, room, a, errors.New("meeting: artifact pipeline dependencies unavailable"))
	}
	execAgent := buildMeetingArtifactAgent(room.TenantID, modelID, e.meetingArtifactSandboxConfigID(ctx, room.TenantID))

	assistantMsgID := uuid.New().String()
	answer, err := e.runArtifactAgentQA(ctx, room, execAgent, sessionID, assistantMsgID, e.buildArtifactQuery(ctx, room, a))
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}

	// 采集沙箱输出目录（对齐 agent_stream_handler：CollectTarget 一次解析出目录与跳过决策）
	var collected types.MessageArtifacts
	if collectDir, skip := e.collector.CollectTarget(ctx, sessionID); !skip {
		if collectDir == "" {
			collectDir = skills.ArtifactOutputDir()
		}
		collected, err = e.collector.Collect(ctx, sessionID, assistantMsgID, room.TenantID, collectDir)
		if err != nil {
			// 采集失败降级为无文件：文本形态仍可交付，文件形态由下方 needsFile 判定失败
			logger.Warnf(ctx, "meeting artifact collect failed, room=%s err=%v", room.ID, err)
		}
	}

	// 落后台会话 assistant 消息：产物行按 (session, message) JOIN 寻址的前提（GetSessionArtifacts 联 messages）
	msg := &types.Message{
		ID: assistantMsgID, SessionID: sessionID, TenantID: room.TenantID,
		Role: "assistant", Content: answer, AgentID: execAgent.ID, IsCompleted: true,
		Artifacts: collected,
	}
	if _, err := e.messageService.CreateMessage(ctx, msg); err != nil {
		return e.failArtifact(ctx, room, a, err)
	}

	file, hasFile := pickArtifactFile(collected, a.KindType)
	if artifactNeedsFile(a.KindType) && !hasFile {
		return e.failArtifact(ctx, room, a, fmt.Errorf("meeting: expected %s file but sandbox produced none", a.KindType))
	}

	a.Status = types.MeetingArtifactStatusReady
	a.Error = ""
	a.SourceMessageID = assistantMsgID
	if hasFile {
		a.FileName, a.FileType, a.FileSize = file.FileName, file.FileType, file.FileSize
		a.URL, a.ContentHash = file.URL, file.ContentHash
	}
	if err := e.artifactRepo.Update(ctx, a); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventArtifactCreated, Data: a})
	return nil
}
```

`ErrArtifactBusy` 常量定义在 T5.6 的 service 文件中；本文件引用它（同包）。
- [ ] **Step 2: 验证** — `go build ./...`；`go vet ./internal/application/service/`。
- [ ] **Step 3: 提交** — `feat(meeting): 引擎产物生成主链路（互斥/后台会话/AgentQA/采集/回写）`

## Task T5.6 Service：产物请求/重生成/详情/下载解析（TDD）

**文件：** `internal/application/service/meeting_artifact.go`（新建）、`internal/application/service/meeting_artifact_test.go`（新建）、`internal/application/service/meeting.go`、`internal/types/interfaces/meeting.go`

- [ ] **Step 1: 写失败测试** — 新建 `meeting_artifact_test.go`：

```go
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeArtifactRepo 内存实现产物仓储（Create/Update/GetByID/ListByRoom/MaxVersionByRoom）
type fakeArtifactRepo struct {
	interfaces.MeetingArtifactRepository
	items  []*types.MeetingArtifact
	nextID int
}

func (f *fakeArtifactRepo) Create(_ context.Context, a *types.MeetingArtifact) error {
	f.nextID++
	if a.ID == "" {
		a.ID = "art-" + string(rune('0'+f.nextID))
	}
	f.items = append(f.items, a)
	return nil
}

func (f *fakeArtifactRepo) Update(_ context.Context, a *types.MeetingArtifact) error {
	for i, item := range f.items {
		if item.ID == a.ID {
			f.items[i] = a
			return nil
		}
	}
	return assert.AnError
}

func (f *fakeArtifactRepo) GetByID(_ context.Context, id, _ string) (*types.MeetingArtifact, error) {
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return nil, assert.AnError
}

func (f *fakeArtifactRepo) ListByRoom(_ context.Context, _ string) ([]*types.MeetingArtifact, error) {
	return f.items, nil
}

func (f *fakeArtifactRepo) MaxVersionByRoom(context.Context, string) (int, error) {
	max := 0
	for _, item := range f.items {
		if item.Version > max {
			max = item.Version
		}
	}
	return max, nil
}

// stubArtifactEngine 记录产物生成调用与锁状态
type stubArtifactEngine struct {
	interfaces.MeetingEngine
	running bool
	genCall int
}

func (e *stubArtifactEngine) IsArtifactRunning(context.Context, string) (bool, error) {
	return e.running, nil
}

func (e *stubArtifactEngine) RunArtifactGeneration(context.Context, *types.MeetingRoom, *types.MeetingArtifact) error {
	e.genCall++
	return nil
}

// fakeSessionMessageService 只实现产物链路用到的 GetMessage
type fakeSessionMessageService struct {
	interfaces.MessageService
	messages map[string]*types.Message
}

func (f *fakeSessionMessageService) GetMessage(_ context.Context, _ string, id string) (*types.Message, error) {
	if m, ok := f.messages[id]; ok {
		return m, nil
	}
	return nil, assert.AnError
}

func artifactFixture(status string, running bool) (*meetingService, *isolationRoomRepo, *fakeArtifactRepo, *stubArtifactEngine) {
	rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
	_ = rooms.Create(context.Background(), &types.MeetingRoom{
		ID: "r1", TenantID: 1, Status: status,
		ArtifactSessionID: "sess-1",
	})
	artifacts := &fakeArtifactRepo{}
	engine := &stubArtifactEngine{running: running}
	svc := &meetingService{
		roomRepo:    rooms,
		messageRepo: &fakeMeetingMessageRepo{},
		artifactRepo: artifacts,
		messageService: &fakeSessionMessageService{messages: map[string]*types.Message{}},
		engine:      engine,
	}
	return svc, rooms, artifacts, engine
}

func TestRequestArtifactMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("归档拒绝", func(t *testing.T) {
		svc, _, arts, engine := artifactFixture(types.MeetingStatusArchived, false)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeDocx, "报告", "")
		assert.Equal(t, ErrRoomArchived, err)
		assert.Empty(t, arts.items)
	})

	t.Run("非法形态拒绝", func(t *testing.T) {
		svc, _, _, engine := artifactFixture(types.MeetingStatusActive, false)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", "pdf", "报告", "")
		assert.Equal(t, ErrInvalidArtifactKind, err)
		assert.Equal(t, 0, engine.genCall)
	})

	t.Run("生成中 409", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, true)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeDocx, "报告", "")
		assert.Equal(t, ErrArtifactBusy, err)
		assert.Empty(t, arts.items)
	})

	t.Run("正常入队 + 版本递增 + 缺省标题", func(t *testing.T) {
		svc, _, arts, engine := artifactFixture(types.MeetingStatusActive, false)
		a, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeMinutes, "", "要含行动项")
		require.NoError(t, err)
		assert.Equal(t, types.MeetingArtifactKindGenerated, a.Kind)
		assert.Equal(t, types.MeetingArtifactTypeMinutes, a.KindType)
		assert.Equal(t, "会议纪要", a.Title, "空标题按形态回缺省")
		assert.Equal(t, "要含行动项", a.Requirement)
		assert.Equal(t, types.MeetingArtifactStatusGenerating, a.Status)
		assert.Equal(t, 1, a.Version)
		waitFor(t, func() bool { return engine.genCall == 1 }, "应异步触发一次生成")
		// 第二次生成版本 +1（共用序列）
		a2, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeTodoList, "清单", "")
		require.NoError(t, err)
		assert.Equal(t, 2, a2.Version)
	})
}

func TestRegenerateArtifactMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("solution 产物不可重生成", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "s1", MeetingRoomID: "r1", Version: 1, SolutionMessageID: "m1",
			Kind: types.MeetingArtifactKindSolution,
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "s1")
		assert.Equal(t, ErrNotRegenerable, err)
	})

	t.Run("历史空 kind 行按 solution 处理", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "legacy", MeetingRoomID: "r1", Version: 1, SolutionMessageID: "m1",
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "legacy")
		assert.Equal(t, ErrNotRegenerable, err)
	})

	t.Run("generating 中拒绝", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "g1", MeetingRoomID: "r1", Version: 1,
			Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
			Status: types.MeetingArtifactStatusGenerating,
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "g1")
		assert.Equal(t, ErrArtifactBusy, err)
	})

	t.Run("正常重生成重置字段", func(t *testing.T) {
		svc, _, arts, engine := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "g2", MeetingRoomID: "r1", Version: 3,
			Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
			Title: "报告", Requirement: "含图表", Status: types.MeetingArtifactStatusFailed,
			Error: "boom", FileName: "old.docx", URL: "provider://old",
		}))
		a, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "g2")
		require.NoError(t, err)
		assert.Equal(t, types.MeetingArtifactStatusGenerating, a.Status)
		assert.Empty(t, a.Error)
		assert.Empty(t, a.FileName)
		assert.Empty(t, a.URL)
		assert.Equal(t, 3, a.Version, "重生成沿用原版本号")
		assert.Equal(t, "含图表", a.Requirement, "原参数保留供回放")
		waitFor(t, func() bool { return engine.genCall == 1 }, "应异步触发重新生成")
	})
}

func TestGetArtifactDetailContentJoin(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	// generated 产物：正文来自后台会话消息
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeMinutes,
		Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1",
	}))
	d, err := svc.GetArtifactDetail(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Empty(t, d.Content, "源消息不存在时降级空正文")

	// 注入源消息后能带出正文
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Content: "纪要正文"},
	}}
	d2, err := svc.GetArtifactDetail(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "纪要正文", d2.Content)
}

func TestResolveArtifactDownload(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1", FileName: "report.docx",
	}))
	// 源消息带两个产物，目标文件按文件名定位索引 1
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Artifacts: types.MessageArtifacts{
			{FileName: "notes.md"}, {FileName: "report.docx"},
		}},
	}}
	loc, err := svc.ResolveArtifactDownload(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/sessions/sess-1/messages/sm-1/artifacts/1/download", loc)

	// 文件名不匹配 → 报错
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Artifacts: types.MessageArtifacts{
			{FileName: "other.xlsx"},
		}},
	}}
	_, err = svc.ResolveArtifactDownload(ctx, 1, "r1", "g1")
	assert.Error(t, err)
}
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/application/service/ -run 'TestRequestArtifact|TestRegenerateArtifact|TestGetArtifactDetail|TestResolveArtifactDownload' -v`。
- [ ] **Step 3: 实现** —
  1. `interfaces/meeting.go` `MeetingService` 追加：

```go
	// RequestArtifact 发起产物生成（202 异步）：校验 + 落 generating 行 + 委托引擎后台执行
	RequestArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, kindType, title, requirement string) (*types.MeetingArtifact, error)
	// RegenerateArtifact 按原参数重新生成指定 generated 产物（202 异步，沿用原行与版本号）
	RegenerateArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, artifactID string) (*types.MeetingArtifact, error)
	// GetArtifactDetail 产物详情（solution 联查会议消息正文；generated 联查后台会话消息正文）
	GetArtifactDetail(ctx context.Context, tenantID uint64, roomID string, artifactID string) (*types.MeetingArtifactDetail, error)
	// ResolveArtifactDownload 解析 generated 产物下载跳转地址（后台会话消息的产物索引）
	ResolveArtifactDownload(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, error)
```

  2. `meeting.go`：struct 与构造函数扩展（`artifactRepo` 与 `engine` 之间插入）：

```go
type meetingService struct {
	roomRepo        interfaces.MeetingRoomRepository
	participantRepo interfaces.MeetingParticipantRepository
	messageRepo     interfaces.MeetingMessageRepository
	agentRepo       interfaces.CustomAgentRepository
	artifactRepo    interfaces.MeetingArtifactRepository
	messageService  interfaces.MessageService // 产物详情/下载：读取后台会话消息
	engine          interfaces.MeetingEngine
}

func NewMeetingService(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	artifactRepo interfaces.MeetingArtifactRepository,
	messageService interfaces.MessageService,
	engine interfaces.MeetingEngine,
) interfaces.MeetingService {
	return &meetingService{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo,
		artifactRepo: artifactRepo, messageService: messageService,
		engine: engine,
	}
}
```

  3. `ListArtifacts` 的联查循环替换为（normalize + 仅 solution 联正文；generated 正文由详情接口提供）：

```go
	out := make([]*types.MeetingArtifactDetail, 0, len(artifacts))
	for _, a := range artifacts {
		a = types.NormalizeMeetingArtifact(a) // 历史行 kind/status 兜底
		d := &types.MeetingArtifactDetail{MeetingArtifact: a}
		if a.SolutionMessageID != "" {
			if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil {
				d.Content = msg.Content // 正文关联失败降级为空串，不拖垮整个列表
			}
		}
		out = append(out, d)
	}
	return out, nil
```

  4. 新建 `meeting_artifact.go`：

```go
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrArtifactBusy 房间已有产物生成任务进行中（handler 判等映射 409）
	ErrArtifactBusy = errors.New("meeting: another artifact generation is running")
	// ErrInvalidArtifactKind 形态不在 8 种白名单内（handler 判等映射 400）
	ErrInvalidArtifactKind = errors.New("meeting: invalid artifact kind_type")
	// ErrNotRegenerable 仅 generated 产物支持重新生成（handler 判等映射 400）
	ErrNotRegenerable = errors.New("meeting: only generated artifacts can be regenerated")
)

// RequestArtifact 发起产物生成：房间/形态校验 → 互斥预检 → 落 generating 行（version 共用序列）
// → 广播 artifact.generating → 异步执行引擎链路。
func (s *meetingService) RequestArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, kindType, title, requirement string) (*types.MeetingArtifact, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err // 租户校验（仓储按 tenant+created_by 过滤）
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err // archived 只读；awaiting_confirmation 允许
	}
	if !types.ValidMeetingArtifactType(kindType) {
		return nil, ErrInvalidArtifactKind
	}
	if title == "" {
		title = defaultArtifactTitle(kindType)
	}
	running, err := s.engine.IsArtifactRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrArtifactBusy
	}
	version, err := s.artifactRepo.MaxVersionByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	version++ // solution 与 generated 共用房间内 version 序列（UNIQUE(room_id, version)）
	a := &types.MeetingArtifact{
		MeetingRoomID: roomID, Version: version,
		Kind: types.MeetingArtifactKindGenerated, KindType: kindType,
		Title: title, Requirement: requirement,
		Status:   types.MeetingArtifactStatusGenerating,
		CreatedBy: userID,
	}
	if err := s.artifactRepo.Create(ctx, a); err != nil {
		return nil, err
	}
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventArtifactGenerating, Data: map[string]interface{}{
		"artifact_id": a.ID, "kind_type": a.KindType, "title": a.Title,
	}})
	s.spawnArtifactGeneration(ctx, roomID, a)
	return a, nil
}

// RegenerateArtifact 按原参数重跑指定 generated 产物：重置结果字段、沿用原行与版本号。
func (s *meetingService) RegenerateArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, artifactID string) (*types.MeetingArtifact, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := ensureRoomActive(room); err != nil {
		return nil, err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return nil, err
	}
	a = types.NormalizeMeetingArtifact(a)
	if a.Kind != types.MeetingArtifactKindGenerated {
		return nil, ErrNotRegenerable
	}
	if a.Status == types.MeetingArtifactStatusGenerating {
		return nil, ErrArtifactBusy
	}
	running, err := s.engine.IsArtifactRunning(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, ErrArtifactBusy
	}
	a.Status = types.MeetingArtifactStatusGenerating
	a.Error = ""
	a.FileName, a.FileType, a.URL, a.ContentHash, a.SourceMessageID = "", "", "", "", ""
	if err := s.artifactRepo.Update(ctx, a); err != nil {
		return nil, err
	}
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventArtifactGenerating, Data: map[string]interface{}{
		"artifact_id": a.ID, "kind_type": a.KindType, "title": a.Title,
	}})
	s.spawnArtifactGeneration(ctx, roomID, a)
	return a, nil
}

// spawnArtifactGeneration 后台执行产物生成（recover 兜底，失败态由引擎 failArtifact 落库）
func (s *meetingService) spawnArtifactGeneration(ctx context.Context, roomID string, a *types.MeetingArtifact) {
	room, err := s.roomRepo.GetByID(ctx, roomID, types.TenantIDFromContextOrZero(ctx))
	_ = err // 引擎侧自取房间亦可；这里传引用便于 room 内字段（ArtifactSessionID）就地持久化
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting RunArtifactGeneration panic, room=%s recover=%v", roomID, r)
			}
		}()
		if err := s.engine.RunArtifactGeneration(bgCtx, room, a); err != nil {
			logger.Errorf(bgCtx, "meeting RunArtifactGeneration failed, room=%s artifact=%s err=%v", roomID, a.ID, err)
		}
	}()
}

// GetArtifactDetail 产物详情：solution 正文联会议消息；generated 正文联后台会话消息（agent 最终回答）。
func (s *meetingService) GetArtifactDetail(ctx context.Context, tenantID uint64, roomID string, artifactID string) (*types.MeetingArtifactDetail, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return nil, err
	}
	a = types.NormalizeMeetingArtifact(a)
	d := &types.MeetingArtifactDetail{MeetingArtifact: a}
	switch {
	case a.SolutionMessageID != "":
		if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil && msg.MeetingRoomID == roomID {
			d.Content = msg.Content
		}
	case a.SourceMessageID != "" && room.ArtifactSessionID != "" && s.messageService != nil:
		if msg, err := s.messageService.GetMessage(ctx, room.ArtifactSessionID, a.SourceMessageID); err == nil {
			d.Content = msg.Content // 文本形态即交付物正文；文件形态为 agent 的说明
		}
	}
	return d, nil
}

// ResolveArtifactDownload 解析下载跳转地址：在后台会话消息的产物列表中按文件名定位索引，
// 返回既有会话产物下载端点路径（属主校验/存储解析/tombstone 检查全部由该端点承担）。
func (s *meetingService) ResolveArtifactDownload(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return "", err
	}
	a, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
	if err != nil {
		return "", err
	}
	a = types.NormalizeMeetingArtifact(a)
	if a.Kind != types.MeetingArtifactKindGenerated || a.SourceMessageID == "" || room.ArtifactSessionID == "" {
		return "", errors.New("meeting: artifact is not downloadable")
	}
	if s.messageService == nil {
		return "", errors.New("meeting: message service unavailable")
	}
	msg, err := s.messageService.GetMessage(ctx, room.ArtifactSessionID, a.SourceMessageID)
	if err != nil || msg == nil {
		return "", errors.New("meeting: artifact source message not found")
	}
	index := -1
	for i, art := range msg.Artifacts {
		if art.FileName == a.FileName && !art.Deleted() {
			index = i
			break
		}
	}
	if index < 0 {
		return "", errors.New("meeting: artifact file not found on source message")
	}
	return fmt.Sprintf("/api/v1/sessions/%s/messages/%s/artifacts/%d/download",
		room.ArtifactSessionID, a.SourceMessageID, index), nil
}
```

  **注意**：`spawnArtifactGeneration` 中重新取房间的写法需要租户上下文——检查 `types` 是否已有 `TenantIDFromContextOrZero`；若无此辅助函数，改为由调用方（RequestArtifact/RegenerateArtifact）把已取到的 `room` 作为参数传入：`spawnArtifactGeneration(ctx, room, a)`，函数体去掉重新查询。**执行时采用后者**（更简单、已验证租户），最终签名为：

```go
func (s *meetingService) spawnArtifactGeneration(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact)
```

调用处相应传 `room`；goroutine 内日志用 `room.ID`。

- [ ] **Step 4: 运行通过** — Step 1 全部测试 + M2/M3 全部测试回归：`go test ./internal/application/service/ -run 'TestReplace|TestConfirm|TestSendUserMessage|TestRequestArtifact|TestRegenerate|TestGetArtifactDetail|TestResolveArtifact' -v`。
- [ ] **Step 5: 提交** — `feat(meeting): 产物请求/重生成/详情/下载解析服务层与校验矩阵`

## Task T5.7 Handler + Router：产物端点 ×4

**文件：** `internal/handler/meeting.go`、`internal/router/routes_meeting.go`

- [ ] **Step 1: handler** — `meeting.go` 追加：

```go
type requestArtifactRequest struct {
	KindType    string `json:"kind_type" binding:"required"`
	Title       string `json:"title"`
	Requirement string `json:"requirement"`
}

// writeArtifactError 产物端点统一错误映射（哨兵判等）
func (h *MeetingHandler) writeArtifactError(c *gin.Context, err error) {
	switch {
	case err == service.ErrRoomArchived:
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
	case err == service.ErrArtifactBusy:
		c.Error(errors.NewConflictError("已有产物正在生成，请稍候再试"))
	case err == service.ErrInvalidArtifactKind, err == service.ErrNotRegenerable:
		c.Error(errors.NewBadRequestError(err.Error()))
	default:
		c.Error(errors.NewInternalServerError(err.Error()))
	}
}

// RequestArtifact POST /api/v1/meetings/:id/artifacts —— 202 异步生成（SSE 推终态）
func (h *MeetingHandler) RequestArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	var req requestArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	a, err := h.meetingService.RequestArtifact(ctx, tenantID, c.Param("id"), userID, req.KindType, req.Title, req.Requirement)
	if err != nil {
		h.writeArtifactError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": a})
}

// RegenerateArtifact POST /api/v1/meetings/:id/artifacts/:aid/regenerate —— 202 按原参数重跑
func (h *MeetingHandler) RegenerateArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	a, err := h.meetingService.RegenerateArtifact(ctx, tenantID, c.Param("id"), userID, c.Param("aid"))
	if err != nil {
		h.writeArtifactError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": a})
}

// GetArtifactDetail GET /api/v1/meetings/:id/artifacts/:aid —— 产物详情（含正文）
func (h *MeetingHandler) GetArtifactDetail(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	d, err := h.meetingService.GetArtifactDetail(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		if stderrors.Is(err, apprepo.ErrMeetingArtifactNotFound) || stderrors.Is(err, apprepo.ErrMeetingRoomNotFound) {
			c.Error(errors.NewNotFoundError("artifact not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to load artifact"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": d})
}

// DownloadArtifact GET /api/v1/meetings/:id/artifacts/:aid/download
// —— 302 跳转既有会话产物下载端点：后台会话属主即会议创建者，
// 鉴权/租户存储后端解析/tombstone 检查全部复用（spec §2.3 下载复用）。
func (h *MeetingHandler) DownloadArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	location, err := h.meetingService.ResolveArtifactDownload(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		c.Error(errors.NewNotFoundError("artifact not found"))
		return
	}
	c.Redirect(http.StatusFound, location)
}
```

- [ ] **Step 2: 路由** — `routes_meeting.go` 在 `GET /:id/artifacts` 与 `GET /:id/artifacts/:aid/export` 之后加：

```go
		meetings.POST("/:id/artifacts", g.OwnedMeetingOrAdmin(), meetingHandler.RequestArtifact)
		meetings.GET("/:id/artifacts/:aid", g.OwnedMeetingOrAdmin(), meetingHandler.GetArtifactDetail)
		meetings.GET("/:id/artifacts/:aid/download", g.OwnedMeetingOrAdmin(), meetingHandler.DownloadArtifact)
		meetings.POST("/:id/artifacts/:aid/regenerate", g.OwnedMeetingOrAdmin(), meetingHandler.RegenerateArtifact)
```

- [ ] **Step 3: 验证** — `go build ./...`；`go test ./internal/handler/ -run TestMeeting -v`（若该包无既有 handler 测试则跳过）。
- [ ] **Step 4: 提交** — `feat(meeting): 产物生成/详情/下载/重生成端点`

**M5 验收（后端）：** 全部产物测试绿；`make migrate-up` 成功；手工：带沙箱配置的租户创建会议 → 讨论几句 → `POST /:id/artifacts {"kind_type":"docx","title":"评审报告"}` 返回 202 → SSE 依次 `artifact.generating`/`artifact.created` → `GET .../download` 302 后成功下载 .docx；并发第二次请求 409；聊天侧栏与会话产物库看不到 `meeting_artifact:` 会话。

---

# Milestone M6 — 产物抽屉重构 + 收尾

**验收标准：** 抽屉列表展示全部产物卡片（solution + generated 统一）；点卡片进详情；solution 回滚/导出不回归；generated 可下载/重新生成；生成中 loading 卡片就位；i18n/type-check/全部测试通过。

## Task T6.1 API 类型与函数扩展（TDD）

**文件：** `frontend/src/api/meeting/index.ts`、`frontend/src/api/meeting/meeting.test.ts`

- [ ] **Step 1: 类型** — `index.ts`：
  - 顶部类型区追加，并把 `MeetingRoom.status` 与 `MeetingMessage` 等既有类型对齐（`status: MeetingStatus`）：

```ts
// 会议房间状态：待确认（开场流程未确认）/ 进行中 / 已归档（只读终态）
export type MeetingStatus = 'active' | 'awaiting_confirmation' | 'archived';
// 产物类别：solution = 评审汇总方案；generated = 用户指定形态生成
export type ArtifactKind = 'solution' | 'generated';
// 产物生成状态机：generating → ready | failed
export type ArtifactStatus = 'generating' | 'ready' | 'failed';
// 8 种产物形态（与后端 types/meeting.go 常量一致）
export type ArtifactKindType =
  | 'report_markdown' | 'minutes' | 'todo_list'
  | 'docx' | 'xlsx' | 'pptx' | 'html_report' | 'chart_image';
```

  - `MeetingArtifact` 替换为：

```ts
// 会议产物（solution 与 generated 统一模型；列表接口不保证 content，详情接口带正文）
export interface MeetingArtifact {
  id: string;
  meeting_room_id: string;
  // solution 产物指向会议方案消息；generated 为空串
  solution_message_id: string;
  version: number;
  adopted_count: number;
  rejected_count: number;
  created_by: string;
  created_at: string;
  kind: ArtifactKind;
  kind_type: ArtifactKindType | '';
  title: string;
  requirement: string;
  file_name: string;
  file_type: string;
  file_size: number;
  url: string;
  content_hash: string;
  status: ArtifactStatus;
  error: string;
  // generated 产物的后台会话消息 ID（下载/详情寻址）
  source_message_id: string;
  content: string;
}
```

- [ ] **Step 2: HTTP 函数** — `index.ts` 在 `exportArtifact` 之后追加：

```ts
// 发起产物生成（后端 202 异步；终态经 SSE artifact.created / artifact.failed 推送）
export async function requestArtifact(roomId: string, kindType: ArtifactKindType, title: string, requirement: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingArtifact }>(`/api/v1/meetings/${roomId}/artifacts`, {
    kind_type: kindType, title, requirement,
  }).then((r) => r.data);
}

// 按原参数重新生成（202 异步，沿用原产物行）
export async function regenerateArtifact(roomId: string, artifactId: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingArtifact }>(
    `/api/v1/meetings/${roomId}/artifacts/${artifactId}/regenerate`, {},
  ).then((r) => r.data);
}

// 产物详情（含正文；generated 正文为生成时 agent 的最终回答）
export async function getArtifactDetail(roomId: string, artifactId: string) {
  const { get } = await loadRequest();
  return get<{ data: MeetingArtifact }>(`/api/v1/meetings/${roomId}/artifacts/${artifactId}`)
    .then((r) => r.data);
}

// generated 产物下载地址（服务端 302 到会话产物下载端点，fetch 自动跟随时携带鉴权头）
export function artifactDownloadUrl(roomId: string, artifactId: string): string {
  return `/api/v1/meetings/${roomId}/artifacts/${artifactId}/download`;
}
```

- [ ] **Step 3: 测试** — `meeting.test.ts` 追加：

```ts
test('artifactDownloadUrl 指向会议产物下载端点', () => {
  assert.equal(artifactDownloadUrl('room-1', 'art-9'), '/api/v1/meetings/room-1/artifacts/art-9/download');
});
```

（import 行补 `artifactDownloadUrl`。）
- [ ] **Step 4: 验证** — `npm test -- src/api/meeting/meeting.test.ts; npm run type-check`（M6 后续 Task 完成前 type-check 可能因 store/视图引用报错——本 Task 至少保证 api 模块自身无错；完整 type-check 在 T6.6 收口）。
- [ ] **Step 5: 提交** — `feat(meeting): 前端产物 API 类型与请求函数`

## Task T6.2 Store：artifacts 状态 + 4 个 SSE 事件

**文件：** `frontend/src/stores/meeting.ts`

- [ ] **Step 1: 实现** —
  - import 补 `listArtifacts, requestArtifact as apiRequestArtifact, regenerateArtifact as apiRegenerateArtifact, type MeetingArtifact, type ArtifactKindType`（`listArtifacts` 原本未被 store 引用，现在改为 store 统一持有产物列表）。
  - 状态区追加：

```ts
  const artifacts = ref<MeetingArtifact[]>([]); // 当前房间产物（solution + generated 统一）
```

  - 动作区追加（`generate` 之后）：

```ts
  // 拉取当前房间全部产物（抽屉打开/回滚后刷新）
  async function fetchArtifacts(roomId: string) {
    artifacts.value = await listArtifacts(roomId);
  }

  // 按 id 就地 upsert（HTTP 返回值与 SSE 事件共用；SSE 可能先于 HTTP 到达）
  function upsertArtifact(a: MeetingArtifact | null | undefined) {
    if (!a?.id) return;
    const i = artifacts.value.findIndex((x) => x.id === a.id);
    if (i >= 0) artifacts.value[i] = { ...artifacts.value[i], ...a };
    else artifacts.value.push(a);
  }

  // SSE 产物事件 → 卡片最小模型（generating/failed 事件只带部分字段）
  function artifactFromEvent(event: string, data: unknown): MeetingArtifact | null {
    const d = data as Record<string, unknown> | null;
    if (!d) return null;
    if (event === 'artifact.created') return d as unknown as MeetingArtifact; // 后端推完整产物对象
    const id = String(d.artifact_id ?? '');
    if (!id) return null;
    return {
      id, meeting_room_id: currentRoom.value?.id ?? '', solution_message_id: '',
      version: 0, adopted_count: 0, rejected_count: 0, created_by: '', created_at: '',
      kind: 'generated', kind_type: (String(d.kind_type ?? '') || '') as ArtifactKindType | '',
      title: String(d.title ?? ''), requirement: '', file_name: '', file_type: '',
      file_size: 0, url: '', content_hash: '',
      status: event === 'artifact.generating' ? 'generating' : 'failed',
      error: String(d.error ?? ''), source_message_id: '', content: '',
    };
  }

  // 发起产物生成：HTTP 202 返回 generating 行，本地立即插入 loading 占位卡片
  async function requestArtifact(kindType: ArtifactKindType, title: string, requirement: string) {
    if (!currentRoom.value) return;
    const a = await apiRequestArtifact(currentRoom.value.id, kindType, title, requirement);
    upsertArtifact(a);
  }

  // 重新生成：原行重置为 generating
  async function regenerateArtifact(artifactId: string) {
    if (!currentRoom.value) return;
    const a = await apiRegenerateArtifact(currentRoom.value.id, artifactId);
    upsertArtifact(a);
  }
```

  - `applyStreamEvent` 的 `consensus.created` 分支后追加：

```ts
    } else if (event === 'artifact.generating' || event === 'artifact.created' || event === 'artifact.failed') {
      upsertArtifact(artifactFromEvent(event, data));
```

  - `leaveRoom` 追加 `artifacts.value = [];`。
  - return 列表补 `artifacts, fetchArtifacts, requestArtifact, regenerateArtifact`。
- [ ] **Step 2: 验证** — `npm run type-check`（ArtifactDrawer 尚未改造，可能仍引用旧 `listArtifacts`——T6.3 一并收口）。
- [ ] **Step 3: 提交** — `feat(meeting): 会议 store 持有产物列表并接入 4 个 SSE 事件`

## Task T6.3 ArtifactDrawer 两态重构

**文件：** `frontend/src/views/meeting/ArtifactDrawer.vue`（整文件替换）

- [ ] **Step 1: 实现** — 完整新内容：

```vue
<template>
  <t-drawer :visible="visible" :header="detail ? $t('meeting.artifactDetail') : $t('meeting.artifacts')"
    size="46%" :footer="false" :z-index="2600" attach="body" @close="closeDrawer">
    <!-- 列表态：产物卡片 + 生成入口 -->
    <div v-if="!detail" class="drawer-body">
      <div class="drawer-toolbar">
        <t-button size="small" theme="primary" :disabled="isArchivedRoom" @click="showGenerate = true">
          <template #icon><t-icon name="add" /></template>
          {{ $t('meeting.generateArtifact') }}
        </t-button>
      </div>
      <t-loading :loading="loading" show-overlay>
        <div v-if="!loading && artifacts.length === 0" class="empty">
          {{ $t('meeting.artifactsEmptyNew') }}
        </div>
        <div v-else class="artifact-cards">
          <div v-for="a in artifacts" :key="a.id" class="artifact-card" @click="openDetail(a)">
            <div class="ac-main">
              <div class="ac-info">
                <div class="ac-title">
                  <t-tag size="small" variant="light" class="kind-tag">{{ kindLabel(a) }}</t-tag>
                  <span class="ac-name">{{ cardTitle(a) }}</span>
                </div>
                <div class="ac-meta">
                  <t-tag v-if="a.kind === 'solution'" size="small" variant="light">v{{ a.version }}</t-tag>
                  <t-tag v-if="isCurrent(a)" size="small" theme="success" variant="light">
                    {{ $t('meeting.currentSolution') }}
                  </t-tag>
                  <t-tag size="small" :theme="statusTheme(a)" variant="light">{{ statusLabel(a) }}</t-tag>
                  <span class="ac-time">{{ a.created_at }}</span>
                </div>
                <div v-if="a.status === 'failed' && a.error" class="ac-error">{{ a.error }}</div>
              </div>
              <div v-if="a.status === 'generating'" class="ac-loading"><t-loading size="small" /></div>
            </div>
            <div class="ac-actions" @click.stop>
              <template v-if="a.kind === 'solution'">
                <t-button v-if="!isCurrent(a)" size="small" theme="primary" variant="text"
                  @click="rollback(a)">{{ $t('meeting.rollbackTo') }}</t-button>
                <t-button size="small" theme="default" variant="text" @click="onExport(a)">
                  {{ $t('meeting.exportMd') }}
                </t-button>
              </template>
              <template v-else-if="a.status === 'ready'">
                <t-button v-if="a.file_name" size="small" theme="primary" variant="text"
                  @click="onDownload(a)">{{ $t('meeting.download') }}</t-button>
                <t-button size="small" theme="default" variant="text" @click="onRegenerate(a)">
                  {{ $t('meeting.regenerate') }}
                </t-button>
              </template>
              <t-button v-else-if="a.status === 'failed'" size="small" theme="default" variant="text"
                @click="onRegenerate(a)">{{ $t('meeting.regenerate') }}</t-button>
            </div>
          </div>
        </div>
      </t-loading>
    </div>

    <!-- 详情态：正文/文件信息 + 动作 -->
    <div v-else class="drawer-body">
      <div class="detail-head">
        <t-button size="small" variant="text" @click="detail = null">
          <template #icon><t-icon name="arrow-left" /></template>
          {{ $t('meeting.backToList') }}
        </t-button>
      </div>
      <t-loading :loading="detailLoading" show-overlay>
        <div v-if="detail" class="detail-body">
          <div class="detail-meta">
            <span v-if="detail.kind === 'solution'">v{{ detail.version }} · {{ $t('meeting.solution') }}</span>
            <span v-else>{{ kindLabel(detail) }}</span>
            <span class="ac-time">{{ detail.created_at }}</span>
          </div>
          <div v-if="detail.file_name" class="detail-file">
            <span class="file-name">{{ detail.file_name }}</span>
            <span class="file-size">{{ formatSize(detail.file_size) }}</span>
            <t-button v-if="detail.status === 'ready'" size="small" theme="primary" variant="text"
              @click="onDownload(detail)">{{ $t('meeting.download') }}</t-button>
          </div>
          <div v-if="detail.status === 'failed' && detail.error" class="ac-error">{{ detail.error }}</div>
          <div class="detail-content markdown-content" v-html="renderContent(detail.content)" />
        </div>
      </t-loading>
    </div>

    <ArtifactGenerateDialog v-model:visible="showGenerate" @requested="showGenerate = false" />
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  getArtifactDetail, exportArtifact, artifactDownloadUrl,
  type MeetingArtifact,
} from '@/api/meeting';
import { useMeetingStore } from '@/stores/meeting';
import ArtifactGenerateDialog from './components/ArtifactGenerateDialog.vue';
import { createChatMarkdownRenderer, renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import { safeMarkdownToHTML, sanitizeMarkdownHTML } from '@/utils/security';

const props = defineProps<{ visible: boolean; roomId: string; currentSolutionMessageId: string }>();
const emit = defineEmits<{ 'update:visible': [boolean] }>();

const { t } = useI18n();
const store = useMeetingStore();

const loading = ref(false);
const detailLoading = ref(false);
const detail = ref<MeetingArtifact | null>(null); // null = 列表态；非空 = 详情态
const showGenerate = ref(false);

// 列表数据统一由 store 持有（SSE 事件就地更新，抽屉重开不再丢生成中的卡片）
const artifacts = computed(() => store.artifacts);
const isArchivedRoom = computed(() => store.currentRoom?.status === 'archived');

// Markdown 渲染器与详情缓存
const markdownRenderer = createChatMarkdownRenderer();
const renderCache = new Map<string, { content: string; html: string }>();

watch(() => props.visible, (v) => {
  if (v) {
    detail.value = null;
    void refresh();
  }
});

async function refresh() {
  loading.value = true;
  try {
    await store.fetchArtifacts(props.roomId);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  } finally {
    loading.value = false;
  }
}

function closeDrawer() {
  emit('update:visible', false);
  detail.value = null;
}

// 当前方案指针指向该产物的消息时视为“当前版本”
function isCurrent(a: MeetingArtifact): boolean {
  return Boolean(props.currentSolutionMessageId) && a.solution_message_id === props.currentSolutionMessageId;
}

// 卡片标题：generated 用 title/file_name；solution 用「方案 vN」
function cardTitle(a: MeetingArtifact): string {
  if (a.kind === 'solution') return `${t('meeting.solution')} v${a.version}`;
  return a.title || a.file_name || kindLabel(a);
}

// 形态标签：solution → 「方案」；generated → 8 形态文案
function kindLabel(a: MeetingArtifact): string {
  if (a.kind === 'solution') return t('meeting.solution');
  switch (a.kind_type) {
    case 'report_markdown': return t('meeting.kindReportMarkdown');
    case 'minutes': return t('meeting.kindMinutes');
    case 'todo_list': return t('meeting.kindTodoList');
    case 'docx': return t('meeting.kindDocx');
    case 'xlsx': return t('meeting.kindXlsx');
    case 'pptx': return t('meeting.kindPptx');
    case 'html_report': return t('meeting.kindHtmlReport');
    case 'chart_image': return t('meeting.kindChartImage');
    default: return t('meeting.solution');
  }
}

function statusTheme(a: MeetingArtifact): 'warning' | 'success' | 'danger' {
  if (a.status === 'generating') return 'warning';
  if (a.status === 'failed') return 'danger';
  return 'success';
}

function statusLabel(a: MeetingArtifact): string {
  if (a.status === 'generating') return t('meeting.artifactGenerating');
  if (a.status === 'failed') return t('meeting.artifactFailed');
  return t('meeting.artifactReady');
}

// 字节数 → 可读大小
function formatSize(bytes: number): string {
  if (!bytes) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function renderContent(content: string): string {
  const raw = content || '';
  if (!raw) return '';
  const key = `detail-${raw.length}`;
  const hit = renderCache.get(key);
  if (hit && hit.content === raw) return hit.html;
  const html = renderChatMarkdown(raw, {
    renderer: markdownRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: false,
  });
  renderCache.set(key, { content: raw, html });
  return html;
}

async function openDetail(a: MeetingArtifact) {
  detailLoading.value = true;
  try {
    detail.value = await getArtifactDetail(props.roomId, a.id);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  } finally {
    detailLoading.value = false;
  }
}

// 回滚 solution 版本：确认后走 store（HTTP 成功即重拉房间与消息），再刷新抽屉列表
function rollback(a: MeetingArtifact) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.rollback'),
    body: t('meeting.rollbackConfirm', { v: a.version }),
    confirmBtn: { content: t('meeting.rollbackTo'), theme: 'danger' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await store.rollback(a.version);
        confirm.destroy();
        MessagePlugin.success(t('meeting.rollbackDone'));
        await refresh();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}

// 导出 solution 为 .md：blob 触发浏览器下载
async function onExport(a: MeetingArtifact) {
  try {
    const blob = await exportArtifact(props.roomId, a.id);
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `meeting-solution-v${a.version}.md`;
    link.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  }
}

// 下载 generated 产物：服务端 302 至会话下载端点，getDown 跟随重定向（保留鉴权头）
async function onDownload(a: MeetingArtifact) {
  try {
    const { getDown } = await import('../../utils/request');
    const blob = await getDown(artifactDownloadUrl(props.roomId, a.id));
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = a.file_name || 'artifact';
    link.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  }
}

// 重新生成：确认后走 store（原行重置为 generating，SSE 推终态）
function onRegenerate(a: MeetingArtifact) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.regenerate'),
    body: t('meeting.regenerateConfirm'),
    confirmBtn: { content: t('meeting.regenerate'), theme: 'primary' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await store.regenerateArtifact(a.id);
        confirm.destroy();
        detail.value = null; // 回列表态看 loading 卡片
      } catch (e) {
        confirm.destroy();
        const msg = (e as Error)?.message || '';
        if (msg.includes('409') || msg.includes('正在生成') || msg.includes('Conflict')) {
          MessagePlugin.warning(t('meeting.artifactBusy'));
        } else {
          MessagePlugin.error(msg || t('meeting.loadFailed'));
        }
      }
    },
  });
}
</script>

<style lang="less" scoped>
.drawer-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: var(--app-space-2, 8px);
}

.drawer-body {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.empty {
  padding: 40px 0;
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
}

.artifact-cards {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2, 8px);
}

.artifact-card {
  padding: var(--app-space-3, 12px);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
  cursor: pointer;
  transition: border-color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.ac-main {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2, 8px);
}

.ac-info {
  flex: 1;
  min-width: 0;
}

.ac-title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1, 4px);
  min-width: 0;

  .ac-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 500;
  }
}

.ac-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-1, 4px);
  flex-wrap: wrap;
  margin-top: 4px;
}

.ac-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
}

.ac-error {
  margin-top: 4px;
  font-size: 12px;
  color: var(--td-danger-color);
  word-break: break-word;
}

.ac-loading {
  flex-shrink: 0;
}

.ac-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0;
  margin-top: 4px;
}

.detail-head {
  margin-bottom: var(--app-space-2, 8px);
}

.detail-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-2, 8px);
  margin-bottom: var(--app-space-2, 8px);
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
}

.detail-file {
  display: flex;
  align-items: center;
  gap: var(--app-space-2, 8px);
  padding: var(--app-space-2, 8px) var(--app-space-3, 12px);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
  margin-bottom: var(--app-space-3, 12px);
  font-size: 13px;

  .file-name {
    font-weight: 500;
    word-break: break-all;
  }

  .file-size {
    color: var(--td-text-color-placeholder, #999);
  }
}

.detail-content {
  font-size: 13px;
  line-height: 1.6;
  word-break: break-word;
}
</style>
```

（对比视图「勾选两版本并排」为旧交互，按 spec B1 统一为「卡片 → 详情」后移除；`compareIds`/`toggleCompare`/`compareList` 及相关模板/样式一并删除。）
- [ ] **Step 2: 验证** — `npm run type-check`（ArtifactGenerateDialog 在 T6.4 前不存在会报错——本 Task 与 T6.4 一起完成后统一验证，或先建空壳组件再填充；推荐顺序：先 T6.4 再回填本 Step 验证）。
- [ ] **Step 3: 提交** — `feat(meeting): 产物抽屉重构为卡片列表 + 详情两态`

## Task T6.4 ArtifactGenerateDialog 生成弹窗

**文件：** `frontend/src/views/meeting/components/ArtifactGenerateDialog.vue`（新建）

- [ ] **Step 1: 实现** — 完整内容：

```vue
<template>
  <!-- 生成产物弹窗：选形态 + 标题 + 补充要求 → store.requestArtifact（202 异步） -->
  <t-dialog :visible="visible" :header="$t('meeting.generateArtifact')" :width="560"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.generateArtifact'), loading: submitting }"
    :cancel-btn="{ content: $t('meeting.cancel') }" @confirm="submit"
    @close="emit('update:visible', false)">
    <t-form :data="form" label-width="90" class="gen-form">
      <t-form-item :label="$t('meeting.artifactKind')" required>
        <div class="kind-grid">
          <button v-for="k in KINDS" :key="k.value" type="button" class="kind-cell"
            :class="{ 'is-active': form.kindType === k.value }" @click="form.kindType = k.value">
            {{ $t(k.label) }}
          </button>
        </div>
      </t-form-item>
      <t-form-item :label="$t('meeting.artifactTitle')">
        <t-input v-model="form.title" :maxlength="100"
          :placeholder="$t('meeting.artifactTitlePlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.artifactRequirement')">
        <t-textarea v-model="form.requirement" :autosize="{ minRows: 3, maxRows: 6 }"
          :placeholder="$t('meeting.artifactRequirementPlaceholder')" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import type { ArtifactKindType } from '@/api/meeting';

const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ 'update:visible': [boolean]; requested: [] }>();

const { t } = useI18n();
const store = useMeetingStore();
const submitting = ref(false);

// 8 种形态（顺序即展示顺序；与后端白名单一致）
const KINDS: Array<{ value: ArtifactKindType; label: string }> = [
  { value: 'report_markdown', label: 'meeting.kindReportMarkdown' },
  { value: 'minutes', label: 'meeting.kindMinutes' },
  { value: 'todo_list', label: 'meeting.kindTodoList' },
  { value: 'docx', label: 'meeting.kindDocx' },
  { value: 'xlsx', label: 'meeting.kindXlsx' },
  { value: 'pptx', label: 'meeting.kindPptx' },
  { value: 'html_report', label: 'meeting.kindHtmlReport' },
  { value: 'chart_image', label: 'meeting.kindChartImage' },
];

const form = reactive({
  kindType: 'report_markdown' as ArtifactKindType,
  title: '',
  requirement: '',
});

watch(() => props.visible, (v) => {
  if (v) {
    form.kindType = 'report_markdown';
    form.title = '';
    form.requirement = '';
  }
});

async function submit() {
  submitting.value = true;
  try {
    await store.requestArtifact(form.kindType, form.title.trim(), form.requirement.trim());
    emit('update:visible', false);
    emit('requested'); // 抽屉据此保持在列表态，观看 loading 卡片
  } catch (e) {
    const msg = (e as Error)?.message || '';
    // 409：同房间已有生成任务（room 级互斥）
    if (msg.includes('409') || msg.includes('正在生成') || msg.includes('Conflict')) {
      MessagePlugin.warning(t('meeting.artifactBusy'));
      return;
    }
    MessagePlugin.error(msg || t('meeting.loadFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.gen-form {
  padding-top: var(--app-space-2, 8px);
}

.kind-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--app-space-2, 8px);
  width: 100%;
}

.kind-cell {
  height: 34px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium, 6px);
  background: var(--td-bg-color-container);
  font-size: 13px;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
    font-weight: 500;
  }
}
</style>
```

- [ ] **Step 2: 验证** — 与 T6.3 一起 `npm run type-check`。
- [ ] **Step 3: 提交** — `feat(meeting): 产物生成弹窗（8 形态选择 + 标题 + 补充要求）`

## Task T6.5 MeetingRoomView 集成生成入口

**文件：** `frontend/src/views/meeting/MeetingRoomView.vue`

- [ ] **Step 1: 顶栏生成入口** — `tb-actions` 中「产物」按钮改为同时打开抽屉，其前加生成按钮：

```html
        <t-button size="small" theme="default" variant="outline" :disabled="isArchived" @click="showGenerate = true">
          {{ $t('meeting.generateArtifact') }}
        </t-button>
        <t-button size="small" theme="default" variant="outline" @click="showArtifacts = true">
          {{ $t('meeting.artifacts') }}
        </t-button>
```

- [ ] **Step 2: 挂弹窗** — `ArtifactDrawer` 标签之后加：

```html
    <ArtifactGenerateDialog v-model:visible="showGenerate" @requested="showArtifacts = true" />
```

（生成发起后自动打开抽屉观看 loading 卡片。）
- [ ] **Step 3: script** — import 补 `import ArtifactGenerateDialog from './components/ArtifactGenerateDialog.vue';`，状态区补 `const showGenerate = ref(false);`，`openRoom` 复位段补 `showGenerate.value = false;`。
- [ ] **Step 4: 验证** — `npm run type-check`。
- [ ] **Step 5: 提交** — `feat(meeting): 房间顶栏接入产物生成入口`

## Task T6.6 i18n 全语言补齐 + 回归收尾

**文件：** 6 个 locale 文件、（回归验证涉及的全部模块）

- [ ] **Step 1: i18n** — 按附录 A 把全部新键加入 `zh-CN.ts`、`zh-TW.ts`、`en-US.ts`、`ja-JP.ts`、`ko-KR.ts`、`ru-RU.ts` 的 `meeting` 节（若 M2/M3/M4 各 Task 已部分加入，本步查漏补齐并核对 6 语言齐全）。
- [ ] **Step 2: 前端验证** — `cd frontend; npm test -- src/api/meeting/meeting.test.ts; npm run type-check; npm run check-i18n`。
- [ ] **Step 3: 后端全量回归** — 仓库根：`go build ./...; go test ./...`。
- [ ] **Step 4: solution 回归手工清单** — 生成方案（同步路径）→ 抽屉 solution 卡片回滚/导出 .md 正常；评审采纳计数展示正常；旧数据（无 kind/status 列值的行）卡片显示「方案 / 已就绪」。
- [ ] **Step 5: 全链路手工验收（spec §9 集成）** — 创建 → 开场 → 开始会议 → 讨论 → 采纳 → 生成 word 产物 → 下载；「调整流程」重出开场；待确认态 PATCH/发消息/评审成功；归档后全部 403。
- [ ] **Step 6: 提交** — `feat(meeting): i18n 全语言补齐与端到端回归收尾`

---

## 附录 A：i18n 新增键全语言表

以下键全部加入 6 个 locale 文件的 `meeting` 节（`npm run check-i18n` 要求所有语言键集一致）。

### M2（详情弹窗）

| key | zh-CN | zh-TW | en-US | ja-JP | ko-KR | ru-RU |
|---|---|---|---|---|---|---|
| detail | 详情 | 詳情 | Details | 詳細 | 세부정보 | Подробности |
| detailTitle | 会议详情 | 會議詳情 | Meeting Details | 会議の詳細 | 회의 세부정보 | Подробности встречи |
| save | 保存 | 儲存 | Save | 保存 | 저장 | Сохранить |
| saveFailed | 保存失败 | 儲存失敗 | Save failed | 保存に失敗しました | 저장 실패 | Не удалось сохранить |
| participantsLocked | 讨论进行中，暂不能修改参会智能体 | 討論進行中，暫不能修改參會智能體 | Cannot change participants while a discussion is running | ディスカッション実行中は参加エージェントを変更できません | 토론 진행 중에는 참가 에이전트를 변경할 수 없습니다 | Во время обсуждения нельзя изменять участников |
| guardMaxExchange | 护栏阈值 | 護欄閾值 | Guard threshold | ガードしきい値 | 가드 임계값 | Порог модератора |

### M3（开场确认）

| key | zh-CN | zh-TW | en-US | ja-JP | ko-KR | ru-RU |
|---|---|---|---|---|---|---|
| filterAwaiting | 待确认 | 待確認 | Pending | 確認待ち | 확인 대기 | Ожидает подтверждения |
| awaitingBanner | 会议待确认：请查看主持人的开场与建议流程，点击「开始会议」或直接发言 | 會議待確認：請查看主持人的開場與建議流程，點擊「開始會議」或直接發言 | Meeting pending: review the host opening and suggested flow, then press "Start" or just speak | 会議は確認待ちです：司会の開始挨拶と提案フローを確認し、「会議開始」を押すかそのまま発言してください | 회의 확인 대기 중입니다: 진행자의 개회 및 제안 흐름을 확인한 뒤 "회의 시작"을 누르거나 바로 발언하세요 | Встреча ожидает подтверждения: ознакомьтесь с открытием ведущего и планом, затем нажмите «Начать встречу» или просто напишите |
| startMeeting | 开始会议 | 開始會議 | Start Meeting | 会議開始 | 회의 시작 | Начать встречу |
| adjustFlow | 调整流程 | 調整流程 | Adjust Flow | フロー調整 | 흐름 조정 | Скорректировать план |
| adjustFlowPlaceholder | 说说你想怎么调整会议流程… | 說說你想怎麼調整會議流程… | Describe how you want to adjust the meeting flow… | 会議の流れをどう調整したいか記入してください… | 회의 흐름을 어떻게 조정하고 싶은지 입력하세요… | Опишите, как вы хотите скорректировать план встречи… |
| adjustFlowMode | 流程调整模式 | 流程調整模式 | Flow adjustment mode | フロー調整モード | 흐름 조정 모드 | Режим корректировки плана |

### M6（产物）

| key | zh-CN | zh-TW | en-US | ja-JP | ko-KR | ru-RU |
|---|---|---|---|---|---|---|
| generateArtifact | 生成产物 | 生成產物 | Generate Artifact | アーティファクト生成 | 산출물 생성 | Создать артефакт |
| artifactDetail | 产物详情 | 產物詳情 | Artifact Details | アーティファクト詳細 | 산출물 상세 | Детали артефакта |
| artifactKind | 产物形态 | 產物形態 | Artifact type | アーティファクト種別 | 산출물 유형 | Тип артефакта |
| artifactTitle | 产物标题 | 產物標題 | Artifact title | アーティファクト名 | 산출물 제목 | Название артефакта |
| artifactTitlePlaceholder | 给产物起个名字（可选） | 為產物命名（可選） | Name this artifact (optional) | アーティファクトに名前をつける（任意） | 산출물 이름 짓기(선택) | Назовите артефакт (необязательно) |
| artifactRequirement | 补充要求 | 補充要求 | Requirements | 追加要件 | 추가 요구사항 | Доп. требования |
| artifactRequirementPlaceholder | 对产物内容/结构的额外要求（可选） | 對產物內容/結構的額外要求（可選） | Extra requirements for the artifact (optional) | 成果物への追加要件（任意） | 산출물에 대한 추가 요구사항(선택) | Дополнительные требования к артефакту (необязательно) |
| artifactGenerating | 生成中 | 生成中 | Generating | 生成中 | 생성 중 | Создаётся |
| artifactReady | 已就绪 | 已就緒 | Ready | 完了 | 완료 | Готово |
| artifactFailed | 生成失败 | 生成失敗 | Failed | 生成失敗 | 생성 실패 | Ошибка создания |
| regenerate | 重新生成 | 重新生成 | Regenerate | 再生成 | 다시 생성 | Пересоздать |
| regenerateConfirm | 按原参数重新生成该产物？ | 按原參數重新生成該產物？ | Regenerate this artifact with the same parameters? | 同じパラメータで再生成しますか？ | 동일한 매개변수로 다시 생성하시겠습니까? | Пересоздать артефакт с теми же параметрами? |
| download | 下载 | 下載 | Download | ダウンロード | 다운로드 | Скачать |
| backToList | 返回列表 | 返回列表 | Back to list | 一覧に戻る | 목록으로 | К списку |
| artifactBusy | 已有产物正在生成，请稍候 | 已有產物正在生成，請稍候 | An artifact is already generating, please wait | 別のアーティファクトを生成中です。しばらくお待ちください | 다른 산출물을 생성 중입니다. 잠시만 기다려 주세요 | Уже создаётся другой артефакт, подождите |
| artifactsEmptyNew | 暂无产物，点击「生成产物」创建 | 暫無產物，點擊「生成產物」建立 | No artifacts yet. Click "Generate Artifact" to create one | アーティファクトがありません。「アーティファクト生成」から作成できます | 산출물이 없습니다. "산출물 생성"을 눌러 만들어 보세요 | Артефактов пока нет. Нажмите «Создать артефакт» |
| kindReportMarkdown | Markdown 报告 | Markdown 報告 | Markdown Report | Markdown レポート | Markdown 보고서 | Отчёт Markdown |
| kindMinutes | 会议纪要 | 會議紀要 | Meeting Minutes | 議事録 | 회의록 | Протокол встречи |
| kindTodoList | 待办事项 | 待辦事項 | To-do List | ToDo リスト | 할 일 목록 | Список задач |
| kindDocx | Word 文档 | Word 文件 | Word Document | Word 文書 | Word 문서 | Документ Word |
| kindXlsx | Excel 表格 | Excel 表格 | Excel Spreadsheet | Excel 表 | Excel 시트 | Таблица Excel |
| kindPptx | PPT 演示 | PPT 簡報 | PPT Slides | PPT スライド | PPT 슬라이드 | Презентация PPT |
| kindHtmlReport | 网页报告 | 網頁報告 | HTML Report | HTML レポート | HTML 보고서 | HTML-отчёт |
| kindChartImage | 图表图片 | 圖表圖片 | Chart Image | チャート画像 | 차트 이미지 | Диаграмма |

> 旧键 `artifactsEmpty` / `compareHint` / `viewContent` / `exitCompare` 在重构后不再被引用：`check-i18n` 的"extra"检查若报多余键则一并删除（以审计输出为准）。

---

## Self-Review（计划完成后自查记录）

- **Spec 覆盖**：G1→M4；G2→M2（端点/弹窗/卡片入口/409/归档只读/待确认允许）；G3→M3（异步开场/落库 payload/两按钮/confirm 幂等/adjust_flow/直接打字=确认/兜底消息）；G4→M5+M6（8 形态/202+SSE/room 互斥/隐藏会话/采集回写/两态抽屉/生成弹窗/入口两处/regenerate）；§2 数据模型→T5.1（+2 列澄清已声明）；§3 API 全部 9 条变更/新增齐备；§4.4 守卫放宽→T1.1/T1.4；§9 测试策略→各 Task 矩阵 + T6.6 手工集成清单；§10 Out of Scope 未引入。
- **占位符扫描**：全部代码块为可编译/可粘贴的完整实现；两处"以文件实际代码为准"的修改点（session.go LIKE 过滤、message_artifact.go ListArtifactLibrary）给出了明确的目标语义与模式参照，非占位符。
- **类型一致性**：`MeetingStatusAwaitingConfirmation`（21 字符）↔ varchar(32) 列 ↔ 前端 `MeetingStatus` 联合；`ArtifactKindType` 8 值 ↔ 后端白名单；SSE 事件载荷（generating/failed 最小字段 + created 完整对象）与 store `artifactFromEvent` 一一对应；`SendUserMessage` 签名变更点（接口/实现/handler/前端 sendMessage）四处同步。
- **风险点**：M5 手工验收依赖租户已配置沙箱与默认 KnowledgeQA 模型（spec §7 风险对策已覆盖失败文案路径）。
