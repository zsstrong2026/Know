# T57 · 10 种 CLI 真实 headless 实测记录

> 日期：2026-11-24 · 环境：Windows 10.0.26300 / Git Bash / 仓库 `D:\知识库\Know`（分支 `trae/metting`）
> 结论先行：**本次未能完成 10 种 CLI 的真实 headless 录制**。本文件如实记录原因、已验证的部分，以及黄金样本仍为手写样本这一事实。

## 1. 环境探测结果

`command -v <binary>` + `<binary> --version` 实测：

| CLI kind | 二进制 | 安装 | 版本 | headless 可跑 |
| --- | --- | --- | --- | --- |
| claude | claude | ✗ | — | 未验证（未安装） |
| opencode | opencode | ✗ | — | 未验证（未安装） |
| cursor-agent | cursor-agent | ✗ | — | 未验证（未安装） |
| agy | agy | ✗ | — | 未验证（未安装） |
| kimi | kimi | ✗ | — | 未验证（未安装） |
| pi | pi | ✓ | 1.5.2 | **否**：`No API key found for the selected model` |
| dsh | dsh | ✓ | 0.2.2-rc.1 | **否**：需先 `dsh --profile <name>`，无已配置 profile |
| zcode | zcode | ✓ | 0.17.1 | **否**：`Select a model before sending a message` |
| codebuddy | codebuddy | ✗ | — | 未验证（未安装） |
| codex | codex | ✗ | — | 未验证（未安装） |

已安装的 3 个 CLI 都在「缺凭据/缺模型选择」阶段就退出，没有产生任何 stream-json 帧
（stdout 为空，stderr 为上面引用的原文）。它们都需要交互式登录或模型配置，
本机没有可用于无人值守调用的凭据。

## 2. 已完成并验证的部分

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 缺少二进制时的白话指引 | ✅ 已验证 | `TestMissingBinaryGivesPlainError`：报「未检测到 zcode 命令…设置页有指引」 |
| 假二进制可被 `exec.LookPath` 命中（Windows `.cmd` 包装） | ✅ 已验证 | `TestFakeBinaryLookPath` |
| 完整 CLIProvider 链路（PATH → 私有 HOME → stream-json 翻译 → 产物提取 → 门禁） | ✅ 已验证（假 zcode） | T56 `TestMixedChainWithRework`：CLI 节点 succeeded、产物 sha256 与磁盘一致、打回后带返工意见重跑 |
| 运行中取消 → 进程树终止 | ✅ 已验证（脚本节点） | T56 `TestCancelKillsProcessTree`（Windows `tasklist` 核对 PID 消失） |
| 原始帧录制开关 | ✅ 已验证 | `TestCaptureSwitchWritesRawFrames` / `TestCaptureSwitchOffWritesNothing`（关闭时零落盘） |
| 凭据不明文落盘 | ✅ 已验证（配置渲染） | 适配器 `ConfigFiles` 只含 `${SOP_CLI_API_KEY}` 变量引用（`adapters*_test.go`） |

## 3. 未完成项与影响

1. **黄金样本仍是手写样本**：`testdata/<kind>/stream.jsonl` 没有替换成真实录制输出。
   若真实 CLI 的 stream-json 字段与手写样本不一致，适配器翻译层存在漂移风险
   （对应 spec §5.2 的风险项「10 种 CLI 协议漂移」）。
2. **真实凭据注入 / 续跑验证**（resume session id 复用）未实测。
3. **10 行实测表**（安装版本/运行 id/流事件 jsonl/产物 sha256/取消验证/续跑验证）只填了
   环境探测与「未安装/缺凭据」结论，其余为空。

## 4. 复测步骤（在有凭据的机器上）

1. 安装目标 CLI 并完成登录/模型配置（不代装）。
2. 在 SOP 工作台「执行器设置」按 kind 保存凭据。
3. 建单节点模板「CLI 冒烟」：cli 节点任务「在 out/ 写 hello.md（内容含当前 CLI 名），然后汇报 pass」，
   产物契约 `out/hello.md` exists。
4. 设置 `SOP_CLI_CAPTURE_DIR=<某目录>` 后触发运行；运行结束把
   `<dir>/<kind>.jsonl` 覆盖 `internal/application/service/sop/executor/cli/testdata/<kind>/stream.jsonl`。
5. 跑 `go test ./internal/application/service/sop/executor/cli/ -v`，按真实字段修正适配器与断言；
   与 spec §5.2 表格冲突处以实测为准，并把差异补进本文件。
6. 运行中取消 → `tasklist`（或 `ps`）核对进程树消失；删除凭据重跑核对白话指引；
   支持续跑的 kind（opencode/cursor/kimi/pi/dsh）核对 Retry 后 session id 复用。

## 5. 相关提交

- 原始帧录制开关：`internal/application/service/sop/executor/cli/stream.go`（`SOP_CLI_CAPTURE_DIR`，生产默认关）
- 对应单测：`internal/application/service/sop/executor/cli/fakebin_test.go`
