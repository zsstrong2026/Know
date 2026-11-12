# 工作台权限矩阵（P4-3）

- 适用版本：工作台 P4 收口
- 权威口径：[实现方案 §4.1、§9.1、§15.3](../../AFFiNE-Know实现方案.md)、[开发计划 G4](../../AFFiNE-Know开发计划.md)
- 代码入口：[`internal/router/routes_workbench.go`](../../internal/router/routes_workbench.go)、[`internal/application/service/workbench/service.go`](../../internal/application/service/workbench/service.go)

工作台是**个人私有资源**，归属只看 `tenant_id + owner_id`。租户角色（Owner/Admin/Contributor/Viewer）只决定"能否进入工作台"，**不**决定"能看到谁的条目"。

## 1. 主体 × 资源矩阵

行是调用主体，列是资源归属。单元格是期望结果。

| 主体 | 自己的条目 | 同租户他人条目 | 跨租户条目 | 说明 |
| --- | --- | --- | --- | --- |
| 任意租户成员（含 Viewer） | 读写 | 2400 | 2400 | 工作台不设只读角色：能进就能改自己的 |
| 同租户 Admin | 读写 | 2400 | 2400 | 管理员不是"超级读者"，不参与私有内容访问 |
| 同租户 Owner | 读写 | 2400 | 2400 | 同上 |
| 跨租户超级管理员 | 读写自己的 | 2400 | 2400 | 跨租户身份只影响执行作用域，不授予资源访问 |
| API Key（含 full-access） | 403 | 403 | 403 | `DenyAPIKeyPrincipal` 在路由层直接拒绝 |
| 未登录 / 无租户上下文 | 401 | 401 | 401 | `callerScope` 先判身份 |

`2400` = `ErrWorkbenchNotFound`（HTTP 404），刻意不用 403：不泄露资源是否存在、属于谁。

## 2. 端点级矩阵

所有 `/api/v1/workbench/*` 与 `/api/v1/me/workbench-notices*` 端点共用同一套判定，差异只在资源对象。

| 端点组 | 资源对象 | 越权结果 | 自动化覆盖 |
| --- | --- | --- | --- |
| `GET/POST /items`、`GET/PUT/DELETE /items/:id`、`POST /items/:id/restore` | `workbench_items` | 2400 | `permissions_test.go`、`workbench_isolation_sqlite_test.go` |
| `GET/PUT /items/:id/content` | `workbench_items.ydoc` | 2400 | `permissions_test.go` |
| `GET /items/:id/versions`、`POST .../versions/:v/restore` | `workbench_item_versions` | 2400 / 空列表 | `workbench_isolation_sqlite_test.go` |
| `/items/:id/assets*`、`GET /assets/:asset_id` | `workbench_item_assets` + 物理文件 | 404 / nil | `workbench_isolation_sqlite_test.go`、`asset_test.go` |
| `/folders*` | `workbench_folders` | 2400 | `permissions_test.go`、`workbench_isolation_sqlite_test.go` |
| `/tags*`、`/items/:id/tags`、`/items/:id/backlinks` | `workbench_tags` / `workbench_item_tags` | 2400 / 空列表 | `workbench_isolation_sqlite_test.go` |
| `/archives*` | `workbench_archives` + 工作知识库知识 | 2400 / 空列表 | `workbench_isolation_sqlite_test.go` |
| `/journals/*` | `workbench_items`（`journal_date` 非空） | 空结果 | `calendar_test.go` |
| `/templates*` | `workbench_templates` | 2400 | `templates_test.go` |
| `/ai/*` | 只查自己的工作知识库 | 2400 | `ai_test.go`、`ai_ask_test.go` |
| `/me/workbench-notices*` | 按 `user_id`（tenantless） | 空列表 | `notices_test.go` |

## 3. 三条硬规则与对应实现

| 规则 | 实现位置 | 反例防护 |
| --- | --- | --- |
| owner 只从 ctx 取，路径/query/body 都不接受 owner | `callerScope()` | 请求体里的 `owner_id` 不参与任何 SQL |
| API Key 一律不可进工作台 | `middleware.DenyAPIKeyPrincipal()` + `callerScope` 的合成用户兜底 | full-access key 也返回 403 |
| 总开关关闭时整组路由不注册 | `RegisterWorkbenchRoutes` 首行 `if !flags.Enabled` | 返回 gin 404，不是 500 |

## 4. 自动化证据

```powershell
$env:PATH="D:\toolchain\w64devkit\bin;C:\Users\zs\go-portable\go\bin;$env:PATH"
$env:CGO_ENABLED="1"; $env:CC="D:\toolchain\w64devkit\bin\gcc.exe"
$env:CXX="D:\toolchain\w64devkit\bin\g++.exe"
$env:CGO_CFLAGS="-ID:/toolchain/include"; $env:CGO_CXXFLAGS="-ID:/toolchain/include"
$env:GOTMPDIR="D:\gotmp"; $env:TMP="D:\gotmp"; $env:TEMP="D:\gotmp"

go test ./internal/application/service/workbench/ -run PermissionMatrix -count=1
go test ./internal/application/repository/ -run WorkbenchIsolation -count=1
go test ./internal/router/ -run Workbench -count=1
```

三组用例的分工：

| 用例集 | 覆盖层次 | 断言 |
| --- | --- | --- |
| `TestPermissionMatrix_*` | 服务层 | 角色不参与归属；越权一律 2400；合成用户 401；缺身份 401 |
| `TestWorkbenchIsolation_*` | 仓储层 SQL | 即使直接调仓储、传别人的 id，查询条件也不会命中 |
| `TestWorkbenchRoutes_*` | 路由层 | API Key 403；总开关/子开关关闭时 404 |

## 5. 手工补充（自动化覆盖不到的部分）

以下需要真实环境走查，属于发布门禁而不是单测：

1. 用两个真实账号登录同一 workspace，确认侧栏只出现各自的文件夹与条目。
2. 用 Admin 账号打开工作知识库列表：看不到他人工作库（`visibility=private` + `is_system`）。
3. 用 API Key 调用 `GET /api/v1/workbench/items`，确认 403 且错误信息不含资源名。
4. 把 `WEKNORA_WORKBENCH_ENABLED` 置 `false` 重启：菜单入口消失，直接访问 `/platform/workbench` 被路由守卫弹回。
5. 越权访问他人在浏览器地址栏里复制来的条目 URL：页面显示"不存在"，不显示标题。

### 5.1 已验证的线上行为（2026-11-07 本机 Docker）

| 场景 | 命令 | 实测结果 |
| --- | --- | --- |
| 匿名访问 | `GET http://localhost:8080/api/v1/workbench/items`（无凭证） | **401**（Auth 中间件先拦） |
| 伪造 API Key | 同上 + `X-API-Key: bogus-key` | **401**（key 校验失败，未到工作台门禁） |
| 有效 API Key（full-access） | 路由级用例 `TestWorkbenchRoutes_DenyAPIKeyPrincipal` | **403**（`DenyAPIKeyPrincipal`） |
| 同租户 Admin 读他人条目 | 服务层用例 `TestPermissionMatrix_ItemAccess` | **2400** |
| 跨租户读写 | 同上 | **2400** |

> 注意：伪造 key 得到 401 是「key 本身无效」的正常结果，不能当作「API Key 被正确挡在工作台外」的证据；
> 后者的证据是有效 key 得到 403（路由级用例），两者不要混。
