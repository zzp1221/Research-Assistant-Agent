import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ExecutionSidebar } from '../../src/features/execution/ExecutionSidebar';
import type { Task } from '../../src/contracts/api';

const readyTask: Task = {
  ID: 'task-error',
  Name: 'Install dependencies',
  Type: 'install_dependencies',
  Description: 'Install dependencies',
  AssignedTo: 'sandbox_agent',
  Status: 'ready',
  Dependencies: [],
};

interface RenderOptions {
  onExecute?: () => void | Promise<unknown>;
  task?: Task;
  logs?: string;
}

const renderSidebar = ({ onExecute = vi.fn(), task = readyTask, logs = '[INFO] starting install' }: RenderOptions = {}) => render(
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
    onExecute={onExecute}
    onChangeDisplayMode={vi.fn()}
  />,
);

describe('ExecutionSidebar API error display', () => {
  it('formats a rejected execute request instead of showing a generic failure', async () => {
    const onExecute = vi.fn(() => Promise.reject({
      response: {
        status: 409,
        data: { code: 'plan_paused', error: 'plan is paused; resume it before running' },
      },
    }));
    renderSidebar({ onExecute });

    fireEvent.click(screen.getByRole('button', { name: /启动任务/ }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('计划已暂停');
    expect(alert).toHaveTextContent('修复建议:');
    expect(alert).toHaveTextContent('plan_paused');
    expect(alert).not.toHaveTextContent('at ');
  });

  it('surfaces the last streamed error line with a remediation', () => {
    renderSidebar({
      task: { ...readyTask, Status: 'failed' },
      logs: '[INFO] starting install\n[error] cannot reach package index',
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('请求失败');
    expect(alert).toHaveTextContent('cannot reach package index');
    expect(alert).toHaveTextContent('修复建议:');
  });
});
