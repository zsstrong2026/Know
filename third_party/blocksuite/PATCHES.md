# PATCHES

当前状态：**2 个补丁**（PATCH-1：日期弹层水平避让；PATCH-2：无限画布格式工具条脱离裁剪层）。除下述补丁外，`third_party/blocksuite` 内的上游文件与 tag v0.27.4 的 `blocksuite/` 子树一致，本目录顶层新增的 `UPSTREAM.md`、`PATCHES.md`、`LICENSE`、`LICENSE-MIT` 均为仓库侧元数据/许可文件，不属于上游源码改动。

## 规则

1. 不散改 `third_party/blocksuite` 内部任何上游文件。
2. 确需修改时，必须先在此登记，再改代码。
3. 升级时先丢弃全部补丁，再整包替换（禁止"只替换几个文件"），然后重新生成别名并跑 `npm run build`。

## 补丁记录

### PATCH-1：修复日期弹层在右侧抽屉中被窗口裁切

- 文件：`affine/data-view/src/property-presets/date/cell-renderer.ts`
- 原因：行详情抽屉固定在视口右侧（352px），日期单元格靠近窗口右边缘。上游弹层只用了 `flip()`，它只处理上下翻转，不处理水平溢出，导致约 296px 宽的日历弹层超出窗口、右侧周六列被裁掉。
- diff：
  ```diff
  --- a/affine/data-view/src/property-presets/date/cell-renderer.ts
  +++ b/affine/data-view/src/property-presets/date/cell-renderer.ts
  @@
  -import { flip, offset } from '@floating-ui/dom';
  +import { flip, offset, shift } from '@floating-ui/dom';
  @@
  -          middleware: [offset(10), flip()],
  +          // 弹层靠近视口右边缘时需要水平平移；flip() 只负责上下翻转。
  +          middleware: [offset(10), flip(), shift({ padding: 8 })],
  ```
- 对应测试：打开「事项」→ 点击任一行打开右侧详情 → 点击「截止日」，日历应完整显示（含 Sa 列），不得超出窗口右边缘。
- 升级处理：升级 BlockSuite 后检查该处 middleware 是否已自带 `shift()`；若上游已修复则删除本补丁并在本文件移除记录。

### PATCH-2：修复无限画布中顶部格式工具条不显示

- 文件：`affine/widgets/toolbar/src/toolbar.ts`
- 原因：无限画布的 `.widgets-container` 带 `contain: size layout`，会成为 `position: fixed` 的包含块。工作台把正文格式工具条固定在编辑区上方 52px 的预留顶栏里，但编辑器挂载层 `.j-mount` 带 `overflow: hidden`，工具条被整体裁掉，画布中编辑文字时看不到格式工具条。
- 处理：固定置顶且存在 fixed 包含块（无限画布）时，把 `editor-toolbar` 临时挂到 `document.body`，用视口坐标定位；退出固定或组件卸载时放回 shadow root 并清理。脱离 shadow root 后由 `frontend/src/workbench/editor/editor-popover.css` 的 `editor-toolbar[data-wb-portal='true']` 补齐固定栏样式。
- 对应测试：进入 Journals 无限画布 → 双击文档卡片正文进入编辑 → 顶部预留栏应出现加粗/斜体等格式按钮；切回文档形态仍正常显示。
- 升级处理：升级 BlockSuite 后检查工具条是否已改用 portal 或不再被画布 `contain` 裁剪；若上游已修复则删除本补丁。

## 补丁记录模板

```text
### PATCH-<序号>：<一句话标题>

- 文件：<相对 third_party/blocksuite 的路径>
- 原因：<为什么要改，不改会出现什么现象>
- diff：
  ```diff
  --- a/<路径>
  +++ b/<路径>
  ```
- 对应测试：<单测/手工验证步骤>
- 升级处理：<升级到新版本时应如何判断该补丁是否仍需要>
```
