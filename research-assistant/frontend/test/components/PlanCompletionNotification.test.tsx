import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { planNotificationText, usePlanCompletionNotification } from '../../src/app/hooks/usePlanCompletionNotification';
import { installDesktopBridge } from '../../src/services/desktopBridge';
import type { DesktopBridge } from '../../src/contracts/desktop';

function Probe({ planId, status, outcome }: { planId: string; status: string; outcome?: string }) {
  usePlanCompletionNotification(planId, status, outcome);
  return null;
}

function bridge(): DesktopBridge {
  return {
    needsOnboarding: () => false,
    completeOnboarding: vi.fn(),
    testConnection: vi.fn(),
    notify: vi.fn(),
    exportDiagnostics: vi.fn(),
    openLogs: vi.fn(),
    chooseDirectory: vi.fn().mockResolvedValue(''),
    showInFolder: vi.fn(),
    checkForUpdate: vi.fn().mockResolvedValue({ available: false }),
    downloadUpdate: vi.fn().mockResolvedValue({ available: false }),
    installUpdate: vi.fn(),
    getRuntimeSettings: vi.fn().mockResolvedValue({ provider: 'auto' }),
    updateProvider: vi.fn().mockResolvedValue(undefined),
    installRuntime: vi.fn().mockResolvedValue({ supported: true }),
    cleanupRuntimes: vi.fn().mockResolvedValue({ supported: true }),
    retry: vi.fn(),
  };
}

describe('usePlanCompletionNotification', () => {
  it('notifies when a plan reaches a terminal state', () => {
    const fake = bridge();
    const restore = installDesktopBridge(fake);
    try {
      const view = render(<Probe planId="p1" status="running" />);
      view.rerender(<Probe planId="p1" status="completed" />);
      expect(fake.notify).toHaveBeenCalledWith('计划已完成', expect.any(String));
    } finally {
      restore();
    }
  });

  it('does not notify for a first render already completed', () => {
    const fake = bridge();
    const restore = installDesktopBridge(fake);
    try {
      render(<Probe planId="p1" status="completed" />);
      expect(fake.notify).not.toHaveBeenCalled();
    } finally {
      restore();
    }
  });
});
describe('planNotificationText', () => {
  it('never claims full success for a partial plan', () => {
    const text = planNotificationText('completed', 'partial');
    expect(text?.title).toContain('部分完成');
    expect(text?.body).toContain('可选工作');
  });

  it('reports a blocked plan as missing conditions, not a plain failure', () => {
    const text = planNotificationText('failed', 'blocked');
    expect(text?.title).toContain('缺少条件');
    expect(text?.title).not.toContain('失败');
  });

  it('keeps smoke and mock downgraded', () => {
    expect(planNotificationText('completed', 'smoke')?.title).toContain('冒烟');
    expect(planNotificationText('completed', 'mock')?.title).toContain('模拟');
  });

  it('does not notify a non-terminal status', () => {
    expect(planNotificationText('running', '')).toBeNull();
  });
});

describe('usePlanCompletionNotification with an outcome', () => {
  it('uses the partial copy when the plan completed partially', () => {
    const fake = bridge();
    const restore = installDesktopBridge(fake);
    try {
      const view = render(<Probe planId="p2" status="running" />);
      view.rerender(<Probe planId="p2" status="completed" outcome="partial" />);
      expect(fake.notify).toHaveBeenCalledWith('计划部分完成', expect.any(String));
    } finally {
      restore();
    }
  });
});
