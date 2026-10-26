import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  UNCLASSIFIED,
  buildFeatureTree,
  filterFeatureTree,
  sortFeatureIterations,
  summarizeFeatureTree,
  type FeatureFilterState,
} from './pmFeatureTree';

// 三级树构建：模块 → 功能 → 操作，空层级自动补「未分类」
test('buildFeatureTree', () => {
  const tree = buildFeatureTree([
    { module_name: '账号', feature_name: '登录', operation_name: '短信登录', status: 'planned' },
    { module_name: '账号', feature_name: '登录', operation_name: '扫码登录', status: 'launched' },
  ]);
  assert.equal(tree.length, 1);
  assert.equal(tree[0].children.length, 1);
  assert.equal(tree[0].children[0].children.length, 2);
});

// 空模块 / 空功能点 / 空操作项都要有兜底名称，避免表格出现空白单元格
test('buildFeatureTreeUnclassified', () => {
  const tree = buildFeatureTree([
    { module_name: '', feature_name: '', operation_name: '', status: 'planned' },
    { module_name: '账号', feature_name: '登录', operation_name: '', status: 'launched' },
  ]);
  assert.equal(tree.length, 2);
  assert.equal(tree[0].name, UNCLASSIFIED);
  assert.equal(tree[0].children[0].name, UNCLASSIFIED);
  // 无操作项时功能点本身即叶子
  assert.equal(tree[0].children[0].children.length, 0);
  assert.equal(tree[1].children[0].children.length, 0);
});

// 筛选命中子节点时保留祖先链，未命中的模块整体裁剪
test('filterFeatureTree', () => {
  const tree = buildFeatureTree([
    { module_name: '账号', feature_name: '登录', operation_name: '短信登录', source: 'prd', status: 'planned' },
    { module_name: '账号', feature_name: '登录', operation_name: '扫码登录', source: 'manual', status: 'launched' },
    { module_name: '版本', feature_name: '评审', operation_name: '记录意见', source: 'roadmap', status: 'planned' },
  ]);
  const filter: FeatureFilterState = { ...emptyFilter(), source: 'manual' };
  const filtered = filterFeatureTree(tree, filter);
  assert.equal(filtered.length, 1);
  assert.equal(filtered[0].children.length, 1);
  assert.equal(filtered[0].children[0].children.length, 1);
  assert.equal(filtered[0].children[0].children[0].name, '扫码登录');
  assert.equal(filterFeatureTree(tree, { ...emptyFilter(), onlyLaunched: true })[0].children[0].children[0].name, '扫码登录');
});

// 统计口径：模块数 / 功能点数 / 叶子数 / 已上线叶子数
test('summarizeFeatureTree', () => {
  const tree = buildFeatureTree([
    { module_name: '账号', feature_name: '登录', operation_name: '短信登录', status: 'launched' },
    { module_name: '账号', feature_name: '登录', operation_name: '扫码登录', status: 'launched' },
    { module_name: '账号', feature_name: '找回密码', operation_name: '', status: 'planned' },
  ]);
  const summary = summarizeFeatureTree(tree);
  assert.equal(summary.moduleCount, 1);
  assert.equal(summary.featureCount, 2);
  assert.equal(summary.leafCount, 3);
  assert.equal(summary.launchedCount, 2);
});

// 迭代历史排序：有版本计划的按计划上线日升序，无计划日的排最后，同日按变更时间升序
test('sortFeatureIterations', () => {
  const versions = [
    { id: 'v1', version_no: '1.3.0', name: 'v1.3', plan_launch_date: '2026-09-01' },
    { id: 'v2', version_no: '1.2.0', name: 'v1.2', plan_launch_date: '2026-08-01' },
    { id: 'v3', version_no: '', name: '规划中版本', plan_launch_date: '' },
  ];
  const sorted = sortFeatureIterations(
    [
      { version_id: 'v1', change_type: 'change', changed_at: '2026-08-10 10:00' },
      { version_id: 'v3', change_type: 'add', changed_at: '2026-08-01 10:00' },
      { version_id: 'v2', change_type: 'add', changed_at: '2026-08-02 10:00' },
    ],
    versions,
  );
  assert.deepEqual(
    sorted.map((item) => item.version_id),
    ['v2', 'v1', 'v3'],
  );
  assert.equal(sorted[0].version_no, '1.2.0');
  assert.equal(sorted[2].version_no, '');
});

function emptyFilter(): FeatureFilterState {
  return { keyword: '', source: '', versionNo: '', onlyLaunched: false };
}
