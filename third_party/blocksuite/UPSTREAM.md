repo:    https://github.com/toeverything/AFFiNE
tag:     v0.27.4
commit:  b4c8548c09da21b2898443559a5b846f0ccf5dd8
subtree: blocksuite/
copied:  2026-10-28
license: MIT（内容在 packages/backend、packages/common/native 之外；见同目录 LICENSE 与 LICENSE-MIT；@blocksuite/bs-docs 与 @blocksuite/docs 两个文档包自身声明 MPL-2.0）
notes:   仅引用，未修改上游源码。实现方案 §10.1 指定 v0.27.5，但上游仓库不存在 v0.27.5 稳定 tag（只有 v0.27.5-beta.0/1/2），故改用最新稳定 tag v0.27.4；beta 版不作为默认。方案中"BlockSuite 采用 MPL-2.0"的表述与本 tag 实际不符：74 个 @blocksuite/* 包中 72 个声明 MIT，仅 @blocksuite/bs-docs、@blocksuite/docs 声明 MPL-2.0（二者为文档站，不参与构建）。

## 工作台依赖的上游非公开接口（P3-1 / P3-3 / P3-4 / P3-5）

以下位置上游没有正式导出的“工具条 API”，工作台以最小侵入方式复用，升级时需优先回归：

| 用途 | 上游位置 | 用法与限制 |
| --- | --- | --- |
| 工具条读写视图/行 | `affine/blocks/database` 的 `DatabaseBlockDataSource` | **必须**取 `<affine-data-view-renderer>.logic.config.dataSource`，不能自己 `new`。自己 new 会得到另一个 `viewManager`，`setCurrentView` 只改私有信号，页面无变化 |
| 行详情 | `DataViewRootUILogic.openDetailPanel({ view, rowId })` | 与用户点行号完全同一条链路；没有“关闭详情”的公开 API，由上游遮罩自己处理 |
| 行搜索 | `SingleView.setSearch(text)` | 与表格内搜索框共享同一个 `searchString`；切视图会重置，工具条同步清空输入框 |
| 筛选/排序编辑 | DOM 点击 `.affine-database-filter-button` / `.affine-database-sort-button` | 上游没有命令式入口。按钮只在悬停时可见，但 `click()` 不依赖可见性；视图未渲染完时按帧重试一次 |
| Calendar 日期属性 | `CalendarSingleView.setStartDateColumn(id)` | 只改 `date.startColumnId`；`defaultData` 里该字段为空，seed 时必须补，否则停在空态 |
| Calendar 跨月拖拽 | 无（上游只在当前月网格命中） | 工作台在宿主上监听 `dragover`，指针进入左右 56px 边缘时点一次上游的「上/下个月」按钮（`installCalendarEdgeFlip`）。**不 fork 上游** |
| 行/列读取（CSV） | `props.columns$` + `dataSource.propertyTypeGet/propertyNameGet/propertyDataGet` | 只能用 `props.columns`：`properties$` 首部会混入 `title`/`type` 等固定属性，导出会让表头多列 |
| 富文本单元格写入 | 列元数据 `config.rawValue.fromJson` | 原始值是 `Y.Text`，直接写字符串会渲染成空；CSV 导入统一过一遍 `fromJson` |
| 数据库 AI 新建列（P3-5） | `dataSource.propertyAdd('end', { type })` + `propertyNameSet` / `propertyDataSet` | 纯文本列的 type 是 `rich-text`，传 `text` 会拿到 undefined 直接抛错；`select`/`multi-select` 的选项要写 `data.options` |
| 数据库 AI 批量写入（P3-5） | `store.transact()` 包住 `propertyAdd` / `rowAdd` / `cellValueChange` | 上游各写方法自己也会 `captureSync`，整包套一层事务才能保证“任一失败全部回滚”；行标题写行块 model 的 `text` |

## 工作台依赖的上游非公开接口（P2-1 画布工具 / P2-5 文档列表）

P2-1 的「工具契约」是本项目拟定的 id（`select|hand|frame|connector|note|text|shape|pen|eraser|mindmap|database`），
上游没有同名 API；映射关系全部收口在 `frontend/src/workbench/editor/blocksuite.ts`：

| 用途 | 上游位置 | 用法与限制 |
| --- | --- | --- |
| 工具激活 | `gfx.tool.setTool(ToolClass, options)` | 传**工具类**而不是名字。`select`=`DefaultTool`、`hand`=`PanTool`、`pen`=`BrushTool`、`eraser`=`EraserTool`，其余同名类 |
| 工具必填项 | `ShapeTool{shapeName}` / `ConnectorTool{mode}` / `NoteTool{childFlavour,childType,tip}` / `PanTool{panning}` | 缺字段会读 undefined 崩；`ShapeName` 是 type-only 联合，不能当值导入，直接传字面量 |
| 工具高亮 | `gfx.tool.currentToolName$`（preact Signal） | 是 `subscribe` 而不是 `effect`；`setMode` 会重建 std，订阅必须重挂 |
| 思维导图插入 | `EdgelessCRUDIdentifier.addElement('mindmap', { style, children })` | 工具条没有拖拽上下文，直接给坐标；`children` 是 `{text, xywh, children}` 树 |
| 数据库插入 | `initDatabaseBlock(store, model, id, 'table', true)` | 复用它的「默认视图 + 首行」；数据库是 note 内的 hub block，**没有 xywh**，不要试图定位到视口中心 |
| 删除擦除范围 | `EraserTool` 的命中集合是 `gfx.layer.canvasElements + gfx.layer.blocks` | 上游会擦掉 note/形状/Frame。工作台改用本地 `PenOnlyEraserTool`（`toolName='pen-eraser'`，注册在 edgeless specs 末尾）：只扫 `layer.canvasElements` 且 type ∈ {brush, highlighter}，因此不碰 block 类元素 |
| 撤销/重做 | `store.undo()` / `store.redo()`（只读时是空操作 + console.error） | 只在 Edgeless 且非只读时调用；Page 形态下工具条不出现 |
| 文档全局列表 | `doc_documents` INNER JOIN `doc_generation_tasks` 取 theme 作标题 | 表本身没有 title/updated_at 列；「更新时间」取最新版本的 created_at |

