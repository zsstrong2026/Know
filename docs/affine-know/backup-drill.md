# 工作台备份与恢复演练（P4-4）

- 适用版本：工作台 P4 收口（迁移 PG 158 / SQLite 62）
- 权威口径：[实现方案 §12.5、§14.8](../../AFFiNE-Know实现方案.md)
- 脚本：[`scripts/workbench-backup.sh`](../../scripts/workbench-backup.sh)（备份/事故恢复）
  、[`scripts/workbench-backup-drill.sh`](../../scripts/workbench-backup-drill.sh)（隔离演练）

本文是**演练记录模板 + 执行手册**。每次演练复制一份「§6 记录」小节填写，不要把结论写回模板。

---

## 1. 备份四类对象

| 类别 | 内容 | 为什么必须备 | 脚本动作 |
| --- | --- | --- | --- |
| 数据库 | `workbench_items`（含 `ydoc`）、`workbench_item_versions`、`workbench_item_assets`、`workbench_item_links`、`workbench_folders`、`workbench_archives`、`workbench_tags` / `workbench_item_tags`、`workbench_templates`、`workbench_notices`、`workbench_orphans`、`doc_generation_tasks` / `doc_documents` | Y.Doc 只在 `workbench_items.ydoc` 与版本表里，丢了无法重建 | PG：`pg_dump -Fc`；SQLite：`sqlite3 .backup` |
| 资产文件 | `LOCAL_STORAGE_BASE_DIR`（默认 `/data/files`）下 `{tenant}/{item}/` 命名空间 | 图片/附件不在库里，无法由 Y.Doc 还原 | 打包资产卷为 `assets.tar.gz` |
| 索引 | 向量/关键词索引 | 可重建，但**必须记录 embedding 模型 ID**，否则重建后维度对不上 | 只写 `config/index-meta.txt` |
| 配置 | `config/config.yaml`、`.env`（脱敏） | 存储/模型配置丢了，恢复后起不来 | 复制 + 过滤密码/API Key |

不进备份包的东西：`third_party/blocksuite`（源码仓库自带）、`dist/`（构建产物）、日志。

## 2. 执行备份

```bash
# 默认输出到 backups/<UTC 时间戳>
scripts/workbench-backup.sh backup

# 指定目录
scripts/workbench-backup.sh backup backups/drill-20261107
```

Windows 上必须用 Git-Bash（`& "C:\Program Files\Git\bin\bash.exe" -c "bash scripts/workbench-backup.sh backup"`）；
脚本已内置 `MSYS_NO_PATHCONV=1` 与「大文件走挂载而非 stdin」两条规避，改动时不要退回去。

产出结构：

```text
backups/drill-20261107/
├── weknora.dump            # PG（或 weknora.db，SQLite）
├── assets.tar.gz           # 资产卷（STORAGE_TYPE != local 时只有 assets.SKIPPED.txt）
├── config/
│   ├── config.yaml
│   ├── env.redacted        # 已剔除 *_PASSWORD / *_API_KEY / *_SECRET / *_TOKEN
│   └── index-meta.txt      # embedding_model_id / db_driver / storage_type
└── SHA256SUMS
```

**对象存储部署**（`STORAGE_TYPE=minio|cos|tos|s3|obs|oss`）不会复制资产，只在 `assets.SKIPPED.txt` 留一行记录。这类部署必须在对象存储侧开启版本或跨桶复制，并写进演练记录，不能当作「已备份」。

## 3. 恢复顺序（不能调换）

1. `scripts/workbench-backup.sh verify <备份目录>` —— 校验和不通过就停在这里。
2. 停应用容器（脚本自动 `docker stop`），避免恢复期间写入。
3. 恢复数据库：`pg_restore --clean --if-exists --no-owner`；SQLite 直接覆盖文件。
4. 恢复资产卷，并核对 `workbench_item_assets.storage_path` 对应的文件都存在。
5. 启动应用，等 `/health` 通过。
6. 需要时重建索引（用 `index-meta.txt` 里记录的 embedding 模型）。
7. 打开工作台做只读检查 → 写一次测试画布 → 跑一次归档与搜索。

```bash
scripts/workbench-backup.sh restore backups/drill-20261107 --yes
```

恢复会覆盖当前库与资产卷，因此脚本强制要求 `--yes`。**禁止**用 `migrate down` 回退迁移来「恢复」旧结构：迁移版本随 dump 一起回落，down 只会二次破坏数据。

### 3.1 演练模式（推荐：不停服、不碰生产）

日常演练不要用 `restore`，用独立库 + 独立卷：

```bash
scripts/workbench-backup-drill.sh backups/drill-20261107
# 可选：自定义演练库名 / 演练卷名
scripts/workbench-backup-drill.sh backups/drill-20261107 weknora_drill know_data-files-drill
```

脚本会：建 `weknora_drill` 与 `know_data-files-drill` → `pg_restore --clean --if-exists --no-owner`
→ 挂盘解包资产 → 逐表比对生产/演练行数 → 校验每个 `resources.physical_path` 文件存在
→ 检查归档→知识、`ydoc` 完整性、迁移版本。生产库与生产卷全程只读，**不需要停 `WeKnora-app`**。

演练结束清理：

```bash
docker exec WeKnora-postgres psql -U postgres -d postgres -c 'DROP DATABASE weknora_drill;'
docker volume rm know_data-files-drill
```

## 4. 恢复后只读检查清单

| # | 检查 | 期望 |
| --- | --- | --- |
| 1 | `workbench_items` 行数、`snapshot_version` 最大值 | 与备份前一致；无 `ydoc IS NULL` 的活条目 |
| 2 | 随机抽 3 个条目打开 | 内容完整，`current_mode` 生效，保存状态显示「草稿已暂存」 |
| 3 | `workbench_item_versions` 最近 30 天版本 | 存在，且可按版本恢复 |
| 4 | 附件在图文中可见 | `storage_path` 文件存在，`/api/v1/workbench/assets/:id` 返回 200 |
| 5 | 数据库条目行数与列定义 | 与备份前一致，三视图（table/kanban/calendar）都能打开 |
| 6 | Journals 月历 | 钉日期的画布条目都在，日期正确 |
| 7 | 归档状态徽标 | `active` / `stale` / `source_deleted` 与备份前一致 |
| 8 | 归档知识可搜索 | 在工作知识库里能检索到已归档内容 |
| 9 | 私有隔离 | 用户 B（同租户）看不到用户 A 的任何条目/文件夹/标签/归档 |
| 10 | API Key | 任何 key（含 full-access）访问 `/api/v1/workbench/*` 返回 403 |
| 11 | 总开关 | `WEKNORA_WORKBENCH_ENABLED=false` 重启后整组端点 404，界面无入口，数据仍在 |

> **校验第 4 项时注意**：资源目录后端下 `workbench_item_assets.storage_path` 存的是
> `resource://<handle>` 句柄，不是文件路径。真实路径要 join `resources.physical_path`
> （形如 `local://<tenant>/<item>/<file>`），去掉 `local://` 前缀即卷内相对路径：
>
> ```sql
> SELECT r.physical_path
>   FROM workbench_item_assets a
>   JOIN resources r ON r.handle = replace(a.storage_path, 'resource://', '')
>  WHERE a.deleted_at IS NULL AND r.deleted_at IS NULL;
> ```

## 5. 演练必须覆盖的五个场景

对应实现方案 §12.5：

1. **完整往返**：用户 A 的画布含 20 个元素 + 1 个数据库 + 2 个附件；备份后恢复到干净环境，元素/行/附件数量一致。
2. **归档可搜索**：归档知识在恢复后可被工作知识库检索命中。
3. **删除归档知识后编辑源文档不自动重建**：状态停在 `stale`/`source_deleted`，手动「更新归档」才新建 part。
4. **双标签页冲突仍是 409**：恢复后版本号连续，旧 `base_version` 保存返回 2401。
5. **单独恢复源文档、不恢复归档**：归档状态显示 `stale` / `source_deleted` 是预期，不报 500。

## 6. 演练记录（每次复制填写）

### 2026-11-07 首次演练（本机 Docker，88 个条目 / 33059 个分块）

```text
演练日期：2026-11-07
执行人：AI（脚本执行）+ 用户（验收）
环境：Docker / PostgreSQL 17（paradedb），WeKnora-app healthy，迁移 PG 158
备份命令：bash scripts/workbench-backup.sh backup backups/drill-20261107
备份产物：
  weknora.dump    137.7 MB
  assets.tar.gz   896.2 MB（3167 个文件）
  config/          config.yaml + env.redacted + index-meta.txt
  SHA256SUMS       5 项全部 OK
校验：verify 通过（含 pg_restore -l 头部解析）
embedding_model_id：builtin-ollama-embedding-bge-m3

恢复方式：scripts/workbench-backup-drill.sh backups/drill-20261107
          即独立演练库 weknora_drill + 独立卷 know_data-files-drill，
          生产库与生产卷全程未被写入，WeKnora-app 未停服
pg_restore 耗时：65 秒 / 108 秒 / 114 秒（三次，含卷解包与行数比对）
pg_restore exit：0（不带 --clean 时曾报 688 条 "already exists"，统一加后归零）

行数对比（生产 = 演练）：
  workbench_items          88
  workbench_item_versions 109
  workbench_item_assets     1
  workbench_archives        2（status 全为 active）
  workbench_folders        10
  workbench_tags            1
  workbench_templates       1
  workbench_notices         2
  doc_documents             0
  resources              3140
  chunks                33059

资产文件校验：1/1 存在，缺失 0
  （要点：storage_path 存的是 resource:// 句柄，真实路径要 join resources.physical_path；
   assets.tar.gz 与生产卷 sha256 完全一致）
归档→知识存在性：缺失 0
快照完整性：snapshot_version>0 但 ydoc 为空的条目 = 0；max_version = 74
迁移版本：prod=158|dirty=false，drill=158|dirty=false

失败与修复（演练过程中发现并已写回脚本）：
  1. pg_dump -Fc -f /tmp/... 在 Git-Bash 下被改写成 C:/Users/.../Temp，导致
     "could not open output file" → 改为 pg_dump 写 stdout 重定向到宿主文件。
  2. docker run -v /data 的挂载点在 Git-Bash 下被改写成 C:/Program Files/Git/data
     → 脚本顶部 export MSYS_NO_PATHCONV=1 / MSYS2_ARG_CONV_EXCL='*'。
  3. 用 stdin 管道解包 896MB 资产包会截断成 "tar: invalid magic"（Windows 管道限制）
     → 改为把备份目录挂进容器再 tar xzf。
  4. 演练库不带 --clean 时 688 条 "constraint already exists" 噪音 → 恢复命令统一加
     --clean --if-exists。

结论：四类备份恢复通过；跨租户/私有隔离与快照完整性未受影响。
遗留：未在「干净机器」上演练（当前是同一台机器的独立库），下次发布前补一次。
签字：______________
```

### 空白模板

```text
演练日期：YYYY-MM-DD
执行人：
环境：Docker / Lite(SQLite)   版本：VERSION / commit
备份命令：
备份产物大小：weknora.dump ___ MB，assets.tar.gz ___ MB
SHA256SUMS 校验：通过 / 失败
恢复耗时：___ 分 ___ 秒
条目数：备份前 ___ / 恢复后 ___（snapshot_version 最大 ___）
数据库行数：___ / ___；附件数：___ / ___
归档 part 数：___ / ___；搜索命中：___ / ___
冲突 409：通过 / 失败
第二用户越权：通过 / 失败（期望 2400）
API Key 拒绝：通过 / 失败（期望 403）
失败与修复：
签字：
```

## 7. BlockSuite 升级矩阵（与备份同批验证）

升级 `third_party/blocksuite` 前必须先跑一遍备份，再按 [`UPSTREAM.md`](../../third_party/blocksuite/UPSTREAM.md) 的口径验证：

| 项 | 判定 |
| --- | --- |
| 是否有补丁 | 必须是「无补丁」整包替换（见 `PATCHES.md`） |
| 别名生成 | `npm run blocksuite:paths` 产出 74 个包 |
| 打开旧快照 | 备份恢复出来的条目能正常打开，不触发 schema 迁移报错 |
| 双形态切换 | page ↔ edgeless 往返 5 次，block 数与视口稳定 |
| 数据库三视图 | 1000 行仍可切换与渲染 |
| 首屏体积 | 首页 chunk 不因升级引入 BlockSuite |

演练不通过不能发布 P4（实现方案 §15.4）。
