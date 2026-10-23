import { describe, expect, it } from 'vitest';
import { buildGraphLayout } from '../../src/features/plan-graph/buildGraphLayout';
import type { GraphTask, PlanGraph } from '../../src/contracts/api';

const makePlan = (count: number): PlanGraph => {
  const nodes: GraphTask[] = Array.from({ length: count }, (_, index) => ({
    id: 'task-' + index,
    name: 'Synthetic task ' + index,
    type: 'general',
    description: 'Synthetic performance node ' + index,
    assigned_to: 'general_agent',
    status: 'pending',
    dependencies: index === 0 ? [] : ['task-' + (index - 1)],
    inputs: {},
    required_artifacts: [],
    output_artifacts: [],
    parallelizable: false,
    priority: 0,
    retry_limit: 1,
    run_count: 0,
    execution_epoch: 0,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
  }));
  return {
    id: 'synthetic-plan-' + count,
    user_intent: 'Synthetic layout performance test',
    intent_type: 'General',
    status: 'pending',
    trace_id: 'synthetic-trace',
    approval: { required: false, status: 'not_required' },
    budget: { max_task_attempts: count, max_duration_seconds: 600 },
    usage: { task_attempts: 0 },
    nodes,
    edges: nodes.slice(1).map((node, index) => ({
      id: 'edge-' + index,
      from: 'task-' + index,
      to: node.id,
      type: 'control',
    })),
    artifacts: {},
    meta: { total_nodes: count, completed_nodes: 0, failed_nodes: 0, blocked_nodes: 0, in_progress_nodes: 0, ready_nodes: 0 },
    created_at: '2026-10-23T00:00:00Z',
    updated_at: '2026-10-23T00:00:00Z',
  };
};

describe('large plan layout baseline', () => {
  it('lays out 300 nodes within a generous CI budget', () => {
    const started = performance.now();
    const layout = buildGraphLayout(makePlan(300));
    const elapsed = performance.now() - started;
    expect(layout.nodes).toHaveLength(300);
    expect(layout.edges).toHaveLength(299);
    expect(elapsed).toBeLessThan(2000);
  });

  it('lays out an 800-node chain without crashing', () => {
    const started = performance.now();
    const layout = buildGraphLayout(makePlan(800));
    const elapsed = performance.now() - started;
    expect(layout.nodes).toHaveLength(800);
    expect(elapsed).toBeLessThan(4000);
  });
});
