// vendored BlockSuite 是 TS 源码直出，但它在 TS 6 严格模式下无法独立通过类型检查
// （third_party 内没有 node_modules，且上游代码本身带隐式 any / 初始化顺序告警）。
// spike 阶段用模块 shim 让 type-check 聚焦在 frontend 自有代码；
// 真正的类型对齐留到上游源码可编译时再做。
declare module '@blocksuite/*'
