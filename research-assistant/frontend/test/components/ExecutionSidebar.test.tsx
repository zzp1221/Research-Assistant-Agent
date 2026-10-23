import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ExecutionSidebar } from '../../src/features/execution/ExecutionSidebar';
import type { Task } from '../../src/contracts/api';

const validation = {
  version: 'benchmark.validation/v1',
  status: 'validated',
  contract_id: 'contract-1',
  primary_metric: 'macro_f1',
  direction: 'maximize',
  public_metrics: { macro_f1: 0.74 },
  hidden_metrics: { macro_f1: 1 },
  hidden_sample_count: 4,
  target_reached: true,
  protected_files_valid: true,
  checks: ['metrics were recomputed by the backend'],
  validated_at: '2026-10-22T00:00:00Z',
};

const task: Task = {
  ID: 'task-bench',
  Name: 'Validate Hidden Benchmark Evidence',
  Type: 'benchmark_validate',
  Description: 'Validate hidden benchmark evidence',
  AssignedTo: 'benchmark_agent',
  Status: 'completed',
  Dependencies: [],
};

describe('ExecutionSidebar benchmark report', () => {
  it('routes benchmark nodes to the readable evaluation report', async () => {
    render(
      <ExecutionSidebar
        selectedTask={task}
        width="420px"
        isExecuting={false}
        requiresApproval={false}
        displayMode="report"
        executionLogs=""
        executionResult=""
        executionCode=""
        executionStructuredData={JSON.stringify(validation)}
        executionImage=""
        logsEndRef={createRef<HTMLDivElement>()}
        onClose={vi.fn()}
        onExecute={vi.fn()}
        onChangeDisplayMode={vi.fn()}
      />,
    );

    expect(screen.getByText('评测报告')).toBeInTheDocument();
    expect(await screen.findByText('已通过隐藏标签验收')).toBeInTheDocument();
    expect((await screen.findAllByText('macro_f1')).length).toBeGreaterThan(0);
    expect(await screen.findByText('metrics were recomputed by the backend')).toBeInTheDocument();
  });
});
