repo:    https://github.com/toeverything/AFFiNE
tag:     v0.27.4
commit:  b4c8548c09da21b2898443559a5b846f0ccf5dd8
subtree: blocksuite/
copied:  2026-10-28
license: MIT（内容在 packages/backend、packages/common/native 之外；见同目录 LICENSE 与 LICENSE-MIT；@blocksuite/bs-docs 与 @blocksuite/docs 两个文档包自身声明 MPL-2.0）
notes:   仅引用，未修改上游源码。实现方案 §10.1 指定 v0.27.5，但上游仓库不存在 v0.27.5 稳定 tag（只有 v0.27.5-beta.0/1/2），故改用最新稳定 tag v0.27.4；beta 版不作为默认。方案中"BlockSuite 采用 MPL-2.0"的表述与本 tag 实际不符：74 个 @blocksuite/* 包中 72 个声明 MIT，仅 @blocksuite/bs-docs、@blocksuite/docs 声明 MPL-2.0（二者为文档站，不参与构建）。
