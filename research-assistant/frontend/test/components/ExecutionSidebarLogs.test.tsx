import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ExecutionSidebar } from '../../src/features/execution/ExecutionSidebar';
import type { Task } from '../../src/contracts/api';
import { DEFAULT_LOG_TAIL } from '../../src/features/execution/logWindow';

const task: Task = {
  ID: 'task-log',
  Name: 'Collect runtime logs',
  Type: 'execute_code',
  Description: 'Collect runtime logs',
  AssignedTo: 'coder_agent',
  Status: 'failed',
  Dependencies: [],
};

const logLines = Array.from({ length: 1000 }, (_, index) => {
  const line = index + 1;
  if (line % 250 === 0) return `line-${line} [ERROR] boom ${line}`;
  if (line % 250 === 125) return `line-${line} [WARN] slow ${line}`;
  return `line-${line} normal output`;
});
const rawLogs = logLines.join('\n');

const renderSidebar = (logs = rawLogs) => render(
  <ExecutionSidebar
    selectedTask={task}
    width="420px"
    isExecuting={false}
    requiresApproval={false}
    displayMode="logs"
    executionLogs={logs}
    executionResult=""
    executionCode=""
    executionStructuredData=""
    executionImage=""
    logsEndRef={createRef<HTMLDivElement>()}
    onClose={vi.fn()}
    onExecute={vi.fn()}
    onChangeDisplayMode={vi.fn()}
  />,
);

describe('ExecutionSidebar log windowing', () => {
  it('renders only the last 500 lines and offers the full log', () => {
    renderSidebar();

    expect(screen.getByText('line-1000 [ERROR] boom 1000')).toBeInTheDocument();
    expect(screen.queryByText('line-500 normal output')).not.toBeInTheDocument();
    expect(screen.getByText(/已隐藏 500 行/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '显示全部（1000 行）' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '下载完整日志' })).toBeInTheDocument();
  });

  it('filters by level without rendering the full log', () => {
    renderSidebar();

    fireEvent.click(screen.getByRole('button', { name: '错误' }));
    expect(screen.getByText('line-1000 [ERROR] boom 1000')).toBeInTheDocument();
    expect(screen.queryByText('line-999 normal output')).not.toBeInTheDocument();
    expect(screen.queryByText(/已隐藏/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '警告' }));
    expect(screen.getByText('line-875 [WARN] slow 875')).toBeInTheDocument();
    expect(screen.queryByText('line-1000 [ERROR] boom 1000')).not.toBeInTheDocument();
  });

  it('expands to the full log on demand and can collapse again', () => {
    renderSidebar();

    fireEvent.click(screen.getByRole('button', { name: '显示全部（1000 行）' }));
    expect(screen.getByText('line-1 normal output')).toBeInTheDocument();
    expect(screen.queryByText(/已隐藏/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '收起（最近 500 行）' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '收起（最近 500 行）' }));
    expect(screen.queryByText('line-1 normal output')).not.toBeInTheDocument();
    expect(screen.getByText(/已隐藏 500 行/)).toBeInTheDocument();
  });

  it('caps the DOM at 500 rows for a 10k-line log', () => {
    const logs = Array.from({ length: 10_000 }, (_, index) => `bulk-${index + 1}`).join('\n');
    const { container } = renderSidebar(logs);

    expect(container.querySelectorAll('[data-log-row]')).toHaveLength(DEFAULT_LOG_TAIL);
    expect(screen.getByText(/已隐藏 9500 行/)).toBeInTheDocument();
    expect(screen.queryByText('bulk-1')).not.toBeInTheDocument();
    expect(screen.getByText('bulk-10000')).toBeInTheDocument();
  });

  it('shows the empty filter hint when no line matches', () => {
    renderSidebar('plain output\nstill plain\n');
    expect(screen.queryByRole('button', { name: '显示全部（2 行）' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '错误' }));
    expect(screen.getByText('当前级别没有匹配的日志；可切换到“全部”查看。')).toBeInTheDocument();
  });
});