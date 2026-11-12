# 工作台发布清单与法务核对（P4-5）

- 权威口径：[实现方案 §15.3、§16.2](../../AFFiNE-Know实现方案.md)
- 相关文件：[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md)、[`third_party/blocksuite/UPSTREAM.md`](../../third_party/blocksuite/UPSTREAM.md)、[`third_party/blocksuite/PATCHES.md`](../../third_party/blocksuite/PATCHES.md)
- 用途：发布前逐条勾选并签字。**AI 只能生成与核对清单，不能代替人签字。**

## 1. 许可与商标（法务口径）

| # | 核对项 | 结论 | 依据 |
| --- | --- | --- | --- |
| 1 | BlockSuite 实际许可 | **MIT**（72/74 个 `@blocksuite/*` 包）；仅 `@blocksuite/bs-docs` / `@blocksuite/docs` 两个文档包是 MPL-2.0，且不参与构建 | `third_party/blocksuite/UPSTREAM.md` |
| 2 | 实现方案「BlockSuite 采用 MPL-2.0」的表述 | **已确认为文档错误**，代码按 MIT 使用；对外材料按本文口径更正 | `THIRD_PARTY_NOTICES.md` §BlockSuite |
| 3 | 许可原文随包分发 | `licenses/blocksuite-LICENSE.txt`（上游顶层 LICENSE）与 `licenses/blocksuite-LICENSE-MIT.txt`（MIT 全文） | `scripts/check-license-bundle.sh` 已纳入必检清单 |
| 4 | 是否修改上游源码 | **无补丁**，整包原样 vendored | `third_party/blocksuite/PATCHES.md` |
| 5 | AFFiNE EE 代码 | **未包含**（`packages/backend`、`packages/common/native` 均未复制） | 同上，`third_party/blocksuite` 只保留 `blocksuite/` 子树 |
| 6 | AFFiNE 品牌资源 | **未使用**：产品名、页面标题、图标、Logo、文案均不出现 AFFiNE；界面许可说明只写 "BlockSuite (MIT)" | 前端资源与 i18n 全量检索 |
| 7 | 其他第三方 notices | Go MPL 模块（mysql / m1cpu）、OpenCC 词典、RapidOCR、ONNX Runtime、OpenCV、NumPy、Wails 模板均在 `THIRD_PARTY_NOTICES.md` 有条目 | `scripts/check-license-bundle.sh` |
| 8 | 索引/模型许可 | 只记录 embedding 模型 ID，不随包分发模型权重 | `docs/affine-know/backup-drill.md` §1 |

对外表述统一为：

> 本产品使用 BlockSuite（MIT License，Copyright © 2022-present TOEVERYTHING PTE. LTD. and its affiliates）作为画布/文档编辑器内核，未包含 AFFiNE 商业版（EE）代码。

## 2. 开关与降级（发布前必须实测）

| # | 检查 | 期望 |
| --- | --- | --- |
| 1 | `WEKNORA_WORKBENCH_ENABLED=false` 启动 | `/api/v1/workbench/*` 全部 404；前端菜单无入口；路由守卫弹回；已有数据不受影响 |
| 2 | `WEKNORA_WORKBENCH_ENABLED=true` + 子开关全关 | 只有条目/文件夹/归档可用；模板、Journals、AI 端点 404；界面无对应入口 |
| 3 | 编辑器故障降级 | 列表与「工作知识库 / 章节文档」仍可进入；条目页显示维护提示；不自动删数据 |
| 4 | 回滚方式 | 关开关 + 回滚前端 chunk；**禁止** `migrate down` |
| 5 | Lite（SQLite）与 Docker（PG）两种形态 | 两种开关状态都能启动，关闭状态无 500 |

## 3. 数据与运维

| # | 检查 | 期望 |
| --- | --- | --- |
| 1 | 备份恢复演练 | 按 [`backup-drill.md`](backup-drill.md) 完成，记录签字 |
| 2 | 迁移 | PG / SQLite 双通过；顺序与编号未被改动 |
| 3 | 权限矩阵 | 按 [`permissions-matrix.md`](permissions-matrix.md) 跑完自动化三组 + 手工五项 |
| 4 | 清理任务 | 回收站 30 天、版本 50 个、资产 30 天未被引用均已生效 |
| 5 | 配额 | 超限返回 413/2402，`details` 带 `limit` 与 `actual` |

## 4. 工程门禁

```powershell
# 前端
cd frontend
npm run type-check
npm run check-i18n
npx tsx --test "src/workbench/**/*.test.ts"
npm run build            # 首页 chunk 不得包含 BlockSuite

# 后端（宿主机可跑的包；完整 ./... 需 Docker，duckdb 静态库在 Windows 链不上）
go build ./internal/...
go test ./internal/application/service/workbench/ -count=1
go test ./internal/application/repository/ -run Workbench -count=1
go test ./internal/router/ ./internal/types/ ./internal/config/ -count=1
go test ./internal/handler/ -run Workbench -count=1
```

> 交接文档 §5 R3 记录的 4 项 Windows 环境失败（`TestSandboxAllocationOnTheMigratedSQLiteSchema`、
> `TestDataSourceScheduleAPIRejectsInvalidCron`、两个 `TestBatchDownloadKnowledge*`）与工作台无关，
> 发布判定时按「已知环境失败」处理，不要顺手改。

| # | 检查 | 期望 |
| --- | --- | --- |
| 1 | 首页 chunk | 不加载 BlockSuite 与 `.ydoc` 资产 |
| 2 | 编辑器 chunk | 只在打开条目时加载 |
| 3 | 六语言 | `check-i18n` 15/15 |
| 4 | 无远程依赖 | 无远程字体/图标/CDN 请求，离线可用 |
| 5 | 代码卫生 | 无 `TBD` / `FIXME` / 假数据 / 占位 API |
| 6 | 备份恢复 | `scripts/workbench-backup-drill.sh` 行数一致、资产缺失 0（2026-11-07 已实测通过） |
| 7 | 权限矩阵 | `go test -run PermissionMatrix` 全绿；手工五项见 `permissions-matrix.md` §5 |

## 5. 文档更新

| 文件 | 需要更新的内容 |
| --- | --- |
| `README.md` / `README_CN.md` | 工作台模块定位、开关说明、许可说明一句话 |
| `THIRD_PARTY_NOTICES.md` | BlockSuite 条目（本次已补） |
| `docs/affine-know/backup-drill.md` | 演练记录（每次演练） |
| `docs/affine-know/permissions-matrix.md` | 端点新增时补行 |
| `docs/affine-know/workbench-handover.md` | 收口状态与已知差异 |
| `CHANGELOG.md` | 工作台发布条目 |

## 6. 签字

```text
发布版本：____________     commit：____________
发布日期：____________
法务（许可/商标）：____________   结论：通过 / 有条件通过（条件：__________）
技术负责人（门禁）：____________  结论：通过 / 不通过
产品负责人（范围）：____________  结论：通过 / 不通过
备注：
```

未完成 §1 第 3、5、6 三项时不得对外分发；未完成 §3 第 1 项不得发布 P4。
