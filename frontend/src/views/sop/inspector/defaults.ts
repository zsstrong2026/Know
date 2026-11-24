// 检查器的默认值与派生逻辑（纯函数，供组件与测试共用）。
import type { SopDefinition, SopNode } from '@/api/sop';

export type InspectorNodeType = 'agent' | 'script' | 'approval' | 'sop';

/** 新节点默认值：与后端 NormalizeSopDefinition 的补默认口径一致。 */
export function inspectorDefaults(type: InspectorNodeType): SopNode {
  return {
    id: '',
    name: '',
    type,
    executor:
      type === 'script'
        ? { kind: 'script' }
        : { kind: 'platform_agent', kb_scope: 'project' },
    task_prompt: '',
    task_refs: [],
    extra_rules: '',
    skills: null,
    mcp: ['know-kb'],
    artifact_outputs: [],
    gate_commands: [],
    gate_extra: '',
    on_failure: 'loop',
    reject_to: '',
    max_attempts: 3,
    max_reworks: 2,
    loop_max_iterations: 3,
    carry_over: true,
    serial_group: '',
    timeout_minutes: 60,
    position: { x: 0, y: 0 },
  };
}

function edgesOf(def: Pick<SopDefinition, 'edges'>): { from: string; to: string }[] {
  return ((def.edges || []) as any[]).map((edge) => ({
    from: String(edge.source ?? edge.from ?? ''),
    to: String(edge.target ?? edge.to ?? ''),
  }));
}

/**
 * 打回候选 = 该节点的上游祖先集合（沿依赖边反向可达）。
 * 与后端 T9 的 Upstreams 同口径：不含自身，不含下游。
 */
export function rejectCandidates(
  def: Pick<SopDefinition, 'nodes' | 'edges'>,
  nodeId: string,
): { id: string; name: string }[] {
  const edges = edgesOf(def);
  const parents = new Map<string, string[]>();
  for (const edge of edges) {
    if (!edge.from || !edge.to) continue;
    parents.set(edge.to, [...(parents.get(edge.to) || []), edge.from]);
  }
  const seen = new Set<string>();
  const queue = [...(parents.get(nodeId) || [])];
  while (queue.length) {
    const current = queue.shift() as string;
    if (seen.has(current)) continue;
    seen.add(current);
    queue.push(...(parents.get(current) || []));
  }
  const nodes = def.nodes || [];
  // 按定义顺序返回，保证下拉顺序稳定
  return nodes
    .filter((node) => seen.has(node.id))
    .map((node) => ({ id: node.id, name: node.name || node.id }));
}

/** 第 5 步 ? 气泡的动态文案（策略不同解释不同）。 */
export function failureHelpText(onFailure: string): string {
  switch (onFailure) {
    case 'reject':
      return '打回上游：把返工意见写进上一步的任务书，让上游重做，然后重新往下走。需要指定打回哪一步。';
    case 'fail':
      return '标记失败：不再自动尝试，运行直接失败并等人处理。适合不可恢复的错误。';
    case 'human':
      return '停下等人：运行停在人工关口，等人补充信息或决定后续，不会自己往下走。';
    case 'loop':
    default:
      return '原地重试：同一个节点带着失败原因再跑一次，次数用完才按后续策略处理。';
  }
}

/** 角色技能摘要：null=角色全部，空数组=全不选。 */
export function skillSummary(roleSkillCount: number, selected: string[] | null): string {
  if (selected === null) return `角色全部技能（${roleSkillCount}）`;
  if (!selected.length) return '未选择技能';
  return selected.join('、');
}
