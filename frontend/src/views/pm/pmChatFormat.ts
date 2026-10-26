// AI 需求分析回答的排版层：把模型输出里常见的 Markdown 写法拆成
// 「小标题 / 段落 / 列表」块，由组件用普通 HTML 渲染，气泡中不再出现 ** ## - 等标记。
// 纯函数，不依赖浏览器 API，便于单测。

export type ChatBlock =
  | { kind: 'heading'; text: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'bullets'; items: string[] }
  | { kind: 'ordered'; items: string[] };

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

// 同类条目合并进相邻列表块，避免每个条目单独成段
function pushItem(blocks: ChatBlock[], kind: 'bullets' | 'ordered', text: string) {
  if (!text) return;
  const last = blocks[blocks.length - 1];
  if (last && last.kind === kind) last.items.push(text);
  else blocks.push({ kind, items: [text] });
}

export function formatChatBlocks(raw: string): ChatBlock[] {
  const blocks: ChatBlock[] = [];
  let paragraph: string[] = [];

  const flushParagraph = () => {
    if (!paragraph.length) return;
    blocks.push({ kind: 'paragraph', text: paragraph.join(' ') });
    paragraph = [];
  };

  for (const line of raw.split(/\r?\n/)) {
    const text = line.trim();
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

    // 引用块去掉 > 前缀后按正文处理
    paragraph.push(cleanInline(text.replace(/^>\s*/, '')));
  }
  flushParagraph();
  return blocks;
}
