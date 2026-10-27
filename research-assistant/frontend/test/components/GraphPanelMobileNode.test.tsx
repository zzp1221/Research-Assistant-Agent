import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GraphPanel } from '../../src/features/plan-graph/GraphPanel';
import type { Task } from '../../src/contracts/api';

vi.mock('../../src/features/plan-graph/GraphCanvas', () => ({
  default: () => <div data-testid="graph-canvas" />,
}));

const task: Task = {
  ID: 'task-1',
  Name: 'Prepare dataset',
  Type: 'experiment_dataset_prepare',
  Description: 'Prepare dataset',
  AssignedTo: 'data_agent',
  Status: 'ready',
  Dependencies: [],
};

const baseProps = {
  nodes: [
    {
      id: task.ID,
      position: { x: 0, y: 0 },
      data: { status: task.Status, task },
    },
  ],
  edges: [],
  onNodesChange: vi.fn(),
  onEdgesChange: vi.fn(),
  onNodeClick: vi.fn(),
  intentContext: null,
  runtimeActions: [],
  runtimePermissionMode: 'confirm_important',
  runAllText: '运行计划',
  graphTitle: '多智能体执行流程',
  graphHint: '等待生成执行计划',
  isExecuting: false,
  requiresApproval: false,
  onRunAll: vi.fn(),
  onApproveAndRun: vi.fn(),
  onCancel: vi.fn(),
  onRetryFailed: vi.fn(),
};

describe('GraphPanel mobile node access', () => {
  it('exposes a 查看节点 control that opens the node detail', async () => {
    const onNodeClick = vi.fn();
    render(<GraphPanel {...baseProps} onNodeClick={onNodeClick} />);

    expect(await screen.findByTestId('graph-canvas')).toBeInTheDocument();
    const viewNode = screen.getByRole('button', { name: '查看节点详情' });
    expect(viewNode).toHaveTextContent('查看节点');

    fireEvent.click(viewNode);
    expect(onNodeClick).toHaveBeenCalledTimes(1);
    expect(onNodeClick.mock.calls[0]?.[1]).toMatchObject({ id: task.ID });
  });
});
