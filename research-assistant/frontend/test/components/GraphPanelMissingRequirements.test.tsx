import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GraphPanel } from '../../src/features/plan-graph/GraphPanel';
import type { MissingRequirement } from '../../src/contracts/api';

const baseProps = {
  nodes: [],
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

const missingRequirements: MissingRequirement[] = [
  {
    kind: 'paper_metadata',
    name: '论文正文',
    detail: 'PDF 没有可提取的文本层',
    remediation: '上传带文本层的论文 PDF',
  },
  {
    kind: 'dependency',
    name: 'torch',
    detail: '版本未锁定',
  },
];

describe('GraphPanel missing requirements', () => {
  it('renders kind, detail and an explicit remediation line per item', () => {
    const { container } = render(
      <GraphPanel {...baseProps} missingRequirements={missingRequirements} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /待办 2/ }));

    expect(screen.getByText('缺失材料 · 2 项')).toBeInTheDocument();
    expect(screen.getByText('paper_metadata')).toBeInTheDocument();
    expect(screen.getByText('PDF 没有可提取的文本层')).toBeInTheDocument();
    expect(screen.getByText('dependency')).toBeInTheDocument();
    expect(screen.getByText('版本未锁定')).toBeInTheDocument();

    const items = container.querySelectorAll('section[aria-label="缺失材料"] li');
    expect(items).toHaveLength(2);
    expect(items[0]?.textContent).toContain('修复建议: 上传带文本层的论文 PDF');
    // Items without a backend remediation keep the actionable default.
    expect(items[1]?.textContent).toContain('修复建议: 请补充该材料后重试。');
  });

  it('keeps the current behavior when there are no missing requirements', () => {
    render(<GraphPanel {...baseProps} missingRequirements={[]} />);
    expect(screen.queryByText(/缺失材料/)).not.toBeInTheDocument();
  });
});
