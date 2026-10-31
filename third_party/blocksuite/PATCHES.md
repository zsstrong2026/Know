# PATCHES

当前状态：**无补丁**。`third_party/blocksuite` 内的上游文件与 tag v0.27.4 的 `blocksuite/` 子树完全一致，
本目录顶层新增的 `UPSTREAM.md`、`PATCHES.md`、`LICENSE`、`LICENSE-MIT` 均为仓库侧元数据/许可文件，不属于上游源码改动。

## 规则

1. 不散改 `third_party/blocksuite` 内部任何上游文件。
2. 确需修改时，必须先在此登记，再改代码。
3. 升级时先丢弃全部补丁，再整包替换（禁止"只替换几个文件"），然后重新生成别名并跑 `npm run build`。

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
