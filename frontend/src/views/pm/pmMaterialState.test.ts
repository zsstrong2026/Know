import assert from 'node:assert/strict';
import { test } from 'node:test';

import { canDelete, canDownload, materialFileName, materialHistory } from './pmMaterialState';

// 下载入口：正文为空（尚无生成结果）时不展示，归档资料仍可下载
test('canDownload', () => {
  assert.equal(canDownload({ content: '' }), false);
  assert.equal(canDownload({ content: '   ' }), false);
  assert.equal(canDownload({}), false);
  assert.equal(canDownload({ content: '# 操作手册' }), true);
});

// 删除入口：与后端一致，仅未归档草稿可见
test('canDelete', () => {
  assert.equal(canDelete({ status: 'draft', archived: false }), true);
  assert.equal(canDelete({ status: 'published', archived: false }), false);
  assert.equal(canDelete({ status: 'draft', archived: true }), false);
  assert.equal(canDelete({ status: 'published', archived: true }), false);
});

// 历史版本：仅同类型归档记录，排除当前资料自身
test('materialHistory', () => {
  const items = [
    { id: '1', material_type: 'manual' as const, archived: false },
    { id: '2', material_type: 'manual' as const, archived: true },
    { id: '3', material_type: 'ppt' as const, archived: true },
    { id: '4', material_type: 'manual' as const, archived: false },
  ];
  assert.deepEqual(
    materialHistory(items, { id: '1', material_type: 'manual' }).map((m) => m.id),
    ['2'],
  );
  // 当前资料为归档记录时，不把自己算作历史
  assert.deepEqual(
    materialHistory(items, { id: '2', material_type: 'manual' }).map((m) => m.id),
    [],
  );
});

// 文件名：按资料类型决定扩展名，并剔除文件系统非法字符
test('materialFileName', () => {
  assert.equal(materialFileName({ title: '操作手册', material_type: 'manual' }), '操作手册.docx');
  assert.equal(materialFileName({ title: '宣传册', material_type: 'brochure' }), '宣传册.docx');
  assert.equal(materialFileName({ title: '产品介绍', material_type: 'ppt' }), '产品介绍.pptx');
  assert.equal(materialFileName({ title: 'a/b:c*d?', material_type: 'manual' }), 'a_b_c_d_.docx');
  assert.equal(materialFileName({ title: '   ', material_type: 'ppt' }), 'material.pptx');
});
