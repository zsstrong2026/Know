// AI 引导 / 需求分析回答的排版层：把模型输出里常见的 Markdown 写法拆成
// 「小标题 / 段落 / 列表 / 表格 / 键值对」块，由组件用普通 HTML 渲染，
// 气泡中不再出现 ** ## - 以及 | 表格竖线等标记。纯函数，不依赖浏览器 API，便于单测。

export type ChatBlock =
  | { kind: 'heading'; text: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'bullets'; items: string[] }
  | { kind: 'ordered'; items: string[] }
  | { kind: 'table'; headers: string[]; rows: string[][] }
  | { kind: 'kv'; items: Array<{ label: string; value: string }> };

// 行内标记清理：图片/链接取文字，加粗/斜体/行内代码/删除线去符号
function cleanInline(text: string): string {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\*\*(.+?)\*\*/g, '$1')
    .replace(/__(.+?)__/g, '$1')
    .replace(/~~(.+?)~~/g, '$1')
    .replace(/`([^`]*)`/g, '$1')
    // 流式输出末尾可能残留不成对的 **，直接去掉
    .replace(/\*\*/g, '')
    .trim();
}

// 表格行拆列：去掉首尾竖线后至少两列才算表格行，避免把普通文本误判为表格
function splitTableRow(line: string): string[] | null {
  if (!line.includes('|')) return null;
  const cells = line.split('|').map((cell) => cleanInline(cell));
  while (cells.length && !cells[0]) cells.shift();
  while (cells.length && !cells[cells.length - 1]) cells.pop();
  return cells.length >= 2 ? cells : null;
}

// Markdown 表格分隔行，如 | --- | :---: |
function isTableDivider(cells: string[]): boolean {
  return cells.every((cell) => /^:?-{2,}:?$/.test(cell.replace(/\s+/g, '')));
}

// 键值行：「目标用户：产品经理」这类短标签 + 冒号，长句与带标点的句子不参与识别
const KV_LINE = /^([^：:|，。！？；\s][^：:|，。！？；]{0,7})\s*[:：]\s*(\S.*)$/;

function matchKv(line: string): { label: string; value: string } | null {
  const matched = line.match(KV_LINE);
  if (!matched) return null;
  const label = cleanInline(matched[1]);
  const value = cleanInline(matched[2]);
  // 流程描述（A -> B）与整句结论仍按正文展示，避免把说明文字做成信息卡
  if (!label || !value || /->|→|=>/.test(value) || /[。！？]$/.test(value)) return null;
  return { label, value };
}

// 同类条目合并进相邻列表块，避免每个条目单独成段
function pushItem(blocks: ChatBlock[], kind: 'bullets' | 'ordered', text: string) {
  if (!text) return;
  const last = blocks[blocks.length - 1];
  if (last && last.kind === kind) last.items.push(text);
  else blocks.push({ kind, items: [text] });
}

export function formatChatBlocks(raw: string): ChatBlock[] {
  const blocks: ChatBlock[] = [];
  const lines = raw.split(/\r?\n/);
  let paragraph: string[] = [];

  const flushParagraph = () => {
    if (!paragraph.length) return;
    blocks.push({ kind: 'paragraph', text: paragraph.join(' ') });
    paragraph = [];
  };

  // 连续行需要一起消费（表格 / 键值卡），因此用下标循环而非 for-of
  for (let index = 0; index < lines.length; index += 1) {
    const text = lines[index].trim();
    // 空行与分隔线只起到断段作用
    if (!text || /^([-*_])\1{2,}$/.test(text)) {
      flushParagraph();
      continue;
    }

    // 整行加粗视为小标题，如「**需求价值**」「**1. 关联影响**」
    const boldOnly = text.match(/^\*\*(.+?)\*\*[:：]?$/);
    if (boldOnly) {
      flushParagraph();
      blocks.push({ kind: 'heading', text: cleanInline(boldOnly[1]) });
      continue;
    }

    const heading = text.match(/^#{1,6}\s+(.*)$/);
    if (heading) {
      flushParagraph();
      blocks.push({ kind: 'heading', text: cleanInline(heading[1]) });
      continue;
    }

    const bullet = text.match(/^[-*•]\s+(.*)$/);
    if (bullet) {
      flushParagraph();
      pushItem(blocks, 'bullets', cleanInline(bullet[1]));
      continue;
    }

    // 有序列表：1. / 1、/ 1) 三种常见写法，统一交给 ol 重新编号
    const ordered = text.match(/^\d+[.、)）]\s*(.+)$/);
    if (ordered) {
      flushParagraph();
      pushItem(blocks, 'ordered', cleanInline(ordered[1]));
      continue;
    }

    // 表格：连续的竖线行合并成一个表格块，分隔行不进入正文
    const tableRow = splitTableRow(text);
    if (tableRow) {
      const rows = [tableRow];
      let cursor = index + 1;
      while (cursor < lines.length) {
        const next = splitTableRow(lines[cursor].trim());
        if (!next) break;
        rows.push(next);
        cursor += 1;
      }
      const body = rows.filter((cells) => !isTableDivider(cells));
      // 至少表头 + 一行数据才成表格，否则当前行仍按正文展示
      if (body.length > 1) {
        flushParagraph();
        blocks.push({ kind: 'table', headers: body[0], rows: body.slice(1) });
        index = cursor - 1;
        continue;
      }
    }

    // 键值对：连续的「标签：值」行合并成一张信息卡
    const kv = matchKv(text);
    if (kv) {
      flushParagraph();
      const items = [kv];
      let cursor = index + 1;
      while (cursor < lines.length) {
        const next = matchKv(lines[cursor].trim());
        if (!next) break;
        items.push(next);
        cursor += 1;
      }
      index = cursor - 1;
      blocks.push({ kind: 'kv', items });
      continue;
    }

    // 引用块去掉 > 前缀后按正文处理
    paragraph.push(cleanInline(text.replace(/^>\s*/, '')));
  }
  flushParagraph();
  return blocks;
}
