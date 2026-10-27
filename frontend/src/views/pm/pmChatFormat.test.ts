import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatChatBlocks } from './pmChatFormat';

// 整行加粗与 # 标题都转成小标题，且不留标记符号
test('标题识别', () => {
  const blocks = formatChatBlocks('**需求价值**\n## 技术风险\n内容');
  assert.deepEqual(blocks[0], { kind: 'heading', text: '需求价值' });
  assert.deepEqual(blocks[1], { kind: 'heading', text: '技术风险' });
  assert.deepEqual(blocks[2], { kind: 'paragraph', text: '内容' });
});

// 连续同类条目合并为一个列表块，有序列表重新编号
test('列表合并', () => {
  const blocks = formatChatBlocks('- 甲\n- 乙\n1. 第一\n2. 第二');
  assert.deepEqual(blocks, [
    { kind: 'bullets', items: ['甲', '乙'] },
    { kind: 'ordered', items: ['第一', '第二'] },
  ]);
});

// 行内标记在正文与列表中都要清理干净
test('行内标记清理', () => {
  const blocks = formatChatBlocks('这里是 **重点** 与 `code`，见 [文档](https://x.y)。\n- ~~废弃~~ 项');
  assert.deepEqual(blocks, [
    { kind: 'paragraph', text: '这里是 重点 与 code，见 文档。' },
    { kind: 'bullets', items: ['废弃 项'] },
  ]);
});

// 流式输出末尾残留未闭合的 ** 不应显示
test('流式残留符号', () => {
  assert.deepEqual(formatChatBlocks('推测**'), [{ kind: 'paragraph', text: '推测' }]);
});

// 空行与分隔线只断段，不产生空块
test('空行与分隔线', () => {
  const blocks = formatChatBlocks('第一段\n\n---\n第二段');
  assert.deepEqual(blocks, [
    { kind: 'paragraph', text: '第一段' },
    { kind: 'paragraph', text: '第二段' },
  ]);
});

// Markdown 表格整块转成表格数据，分隔行不进入正文
test('表格识别', () => {
  const blocks = formatChatBlocks('| 模块 | 功能点 |\n| --- | :---: |\n| 需求管理 | 需求录入 |\n| 版本管理 | 版本创建 |');
  assert.deepEqual(blocks, [{
    kind: 'table',
    headers: ['模块', '功能点'],
    rows: [['需求管理', '需求录入'], ['版本管理', '版本创建']],
  }]);
});

// 单行竖线文本不成表格，仍按段落展示
test('单行竖线不成表格', () => {
  assert.deepEqual(formatChatBlocks('甲 | 乙'), [{ kind: 'paragraph', text: '甲 | 乙' }]);
});

// 连续「标签：值」行合并为一张键值卡
test('键值对合并', () => {
  const blocks = formatChatBlocks('目标用户：产品经理\n核心价值：需求到上线闭环');
  assert.deepEqual(blocks, [{
    kind: 'kv',
    items: [
      { label: '目标用户', value: '产品经理' },
      { label: '核心价值', value: '需求到上线闭环' },
    ],
  }]);
});

// 流程箭头描述与整句结论不做成键值卡
test('流程与整句不识别为键值', () => {
  assert.deepEqual(formatChatBlocks('版本管理: 需求归纳 -> 评审'), [
    { kind: 'paragraph', text: '版本管理: 需求归纳 -> 评审' },
  ]);
  assert.deepEqual(formatChatBlocks('总结：本期先聚焦核心链路。'), [
    { kind: 'paragraph', text: '总结：本期先聚焦核心链路。' },
  ]);
});
