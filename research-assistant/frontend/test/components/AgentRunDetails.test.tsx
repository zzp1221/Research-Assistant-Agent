import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { AgentRunState, GraphTask, Task } from '../../src/contracts/api';
import { AgentRunDetails } from '../../src/features/execution/AgentRunDetails';
import { ExecutionSidebar } from '../../src/features/execution/ExecutionSidebar';
import { graphTaskToTask } from '../../src/features/plan-graph/buildGraphLayout';

const run: AgentRunState = { version: 'agent.run/v1', id: 'run', checkpoint_ref: 'private-ref', status: 'stopped', stop_reason: 'agent_max_steps', steps: 6, max_steps: 6, tool_calls: 3, max_tool_calls: 12, updated_at: '' };

it('preserves API diagnostics through the graph mapping and actual sidebar', () => {
  const node: GraphTask = {
    id: 'task', name: 'Research step', type: 'custom', description: 'Task', assigned_to: 'profile:research', status: 'failed', dependencies: [],
    required_artifacts: [], output_artifacts: [], parallelizable: false, priority: 1, retry_limit: 1, run_count: 2, execution_epoch: 2,
    contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] }, metadata: { agent_run: run },
    recovery: { chain_id: 'chain', max_actions: 2, used_actions: 1, http_attempts: 4, max_http_attempts: 24, replans: 0, decisions: [{ action: 'local_repair', reason: 'schema mismatch', consumed: 1, remaining: 1, at: '' }] },
    failure: { class: 'output', code: 'output_invalid', stage: 'output', message: 'required metric missing', fingerprint: 'fingerprint', state_hash: 'hash', recoverable: true },
    replan_proposal: { reason: 'choose an approved alternative', failure_fingerprint: 'fingerprint', base_revision: 2, requires_review: true, frozen_contract_hash: 'contract-hash', invalidate_artifacts: ['report'] },
  };
  const task = graphTaskToTask(node);
  expect(task.AgentRun).toBe(run);
  expect(task.Recovery).toBe(node.recovery);
  render(<ExecutionSidebar selectedTask={task} width="420px" isExecuting={false} requiresApproval={false} displayMode="logs" executionLogs="" executionResult="" executionCode="" executionStructuredData="" executionImage="" logsEndRef={createRef<HTMLDivElement>()} onClose={vi.fn()} onExecute={vi.fn()} onChangeDisplayMode={vi.fn()} />);
  expect(screen.getByText('当前阶段：已停止')).toBeTruthy();
  expect(screen.getByText('模型步数 6/6 · 工具调用 3/12')).toBeTruthy();
  expect(screen.getByText('恢复动作剩余 1/2 · HTTP 尝试 4/24')).toBeTruthy();
  expect(screen.getByText('停止原因：agent_max_steps')).toBeTruthy();
  expect(screen.getByText('失败类型：输出验收失败 · 输出验收')).toBeTruthy();
  expect(screen.getByText('重规划提案 · 待审阅 · r2')).toBeTruthy();
  expect(screen.queryByText('private-ref')).toBeNull();
});

it('does not fabricate run counters for legacy tasks', () => {
  const task: Task = { ID: 'old', Name: 'Legacy', Description: '', AssignedTo: 'coder_agent', Status: 'ready', Dependencies: [] };
  const { container } = render(<AgentRunDetails task={task} />);
  expect(container.textContent).toBe('');
});
