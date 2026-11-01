import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Onboarding } from '../../src/features/onboarding/Onboarding';
import type { DesktopBridge } from '../../src/contracts/desktop';

function makeBridge(overrides: Partial<DesktopBridge> = {}): DesktopBridge {
  return {
    needsOnboarding: () => true,
    completeOnboarding: vi.fn().mockResolvedValue(undefined),
    testConnection: vi.fn().mockResolvedValue({ ok: true }),
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
    ...overrides,
  };
}

describe('Onboarding', () => {
  it('walks through the wizard and saves credentials', async () => {
    const completeOnboarding = vi.fn().mockResolvedValue(undefined);
    const testConnection = vi.fn().mockResolvedValue({ ok: true });
    const onComplete = vi.fn();
    render(<Onboarding onComplete={onComplete} bridge={makeBridge({ completeOnboarding, testConnection })} />);

    fireEvent.click(screen.getByText('开始'));
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'sk-test' } });
    fireEvent.click(screen.getByText('测试连接'));
    await waitFor(() => expect(screen.getByText('连接成功')).toBeInTheDocument());
    expect(testConnection).toHaveBeenCalledWith(expect.objectContaining({ apiKey: 'sk-test' }));

    fireEvent.click(screen.getByText('下一步'));
    fireEvent.click(screen.getByText('下一步'));
    fireEvent.click(screen.getByText('进入工作台'));

    await waitFor(() => expect(completeOnboarding).toHaveBeenCalled());
    expect(onComplete).toHaveBeenCalled();
  });

  it('shows connection errors without blocking the wizard', async () => {
    render(
      <Onboarding
        onComplete={vi.fn()}
        bridge={makeBridge({ testConnection: vi.fn().mockResolvedValue({ ok: false, error: 'bad key' }) })}
      />,
    );
    fireEvent.click(screen.getByText('开始'));
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'sk-bad' } });
    fireEvent.click(screen.getByText('测试连接'));
    await waitFor(() => expect(screen.getByText('bad key')).toBeInTheDocument());
  });
});