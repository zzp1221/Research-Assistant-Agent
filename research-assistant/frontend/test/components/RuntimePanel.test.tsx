import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import type { SandboxProbeReport } from '../../src/contracts/api';
import { RuntimePanel } from '../../src/features/settings/RuntimePanel';
import { getSandboxCapabilities } from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({ getSandboxCapabilities: vi.fn() }));
vi.mock('../../src/services/desktopMetrics', () => ({
  getDesktopMetrics: vi.fn().mockRejectedValue(new Error('desktop metrics unavailable')),
}));
vi.mock('../../src/services/desktopRuntime', () => ({
  getDesktopRuntimeStatus: vi.fn().mockResolvedValue({ supported: false, provider: 'native' }),
}));
vi.mock('../../src/services/desktopBridge', () => ({ getDesktopBridge: () => undefined }));

const verifiedText = '全部通过：可以使用 AppContainer';
const blockedText = '隔离未获验证：要求强隔离或断网的任务将被阻断';

function completeReport(): SandboxProbeReport {
  return {
    version: 'native.isolation-probe/v2', runner: 'appcontainer', environment_key: 'test-runtime-hash',
    appcontainer_ok: true, attempted: 8, passed: 8, failed: 0, errors: 0, inconclusive: 0,
    cases: [
      'python_stdlib', 'multiprocessing', 'workspace_write', 'process_identity',
      'workspace_escape', 'network_connect', 'network_online', 'job_cleanup',
    ].map((name, index) => ({ name, security: index >= 3, passed: true, status: 'pass' })),
  };
}

function showReport(probe: SandboxProbeReport) {
  vi.mocked(getSandboxCapabilities).mockResolvedValue({ provider: 'native', isolation: 'appcontainer', network_enforced: 'os', probe });
  render(<RuntimePanel />);
}

describe('RuntimePanel isolation evidence', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
  });

  it('shows success for a complete current verified report', async () => {
    showReport(completeReport());
    expect(await screen.findByText(verifiedText)).toBeVisible();
    expect(screen.getByText('已执行 8 项，通过 8 项，失败 0 项，错误 0 项，无法判定 0 项')).toBeVisible();
    expect(screen.queryByText(blockedText)).not.toBeInTheDocument();
  });

  it('keeps an inconclusive network result distinct from a successful denial', async () => {
    const probe = completeReport();
    Object.assign(probe, { appcontainer_ok: false, passed: 7, inconclusive: 1 });
    Object.assign(probe.cases![5], { status: 'inconclusive', passed: false, detail: 'controlled network target unavailable' });
    showReport(probe);
    expect(await screen.findByText(blockedText)).toBeVisible();
    const item = screen.getByText('network_connect').closest('li')!;
    expect(within(item).getByText('无法判定')).toBeVisible();
    expect(within(item).queryByText('通过')).not.toBeInTheDocument();
    expect(screen.getByText('已执行 8 项，通过 7 项，失败 0 项，错误 0 项，无法判定 1 项')).toBeVisible();
    expect(screen.queryByText(verifiedText)).not.toBeInTheDocument();
  });

  it('shows security failures, errors and blocking together with their counts', async () => {
    const probe = completeReport();
    Object.assign(probe, { appcontainer_ok: false, passed: 6, failed: 1, errors: 1, reason: 'isolation not verified: workspace_escape (fail)' });
    Object.assign(probe.cases![4], { status: 'fail', passed: false, detail: 'host sentinel was modified' });
    Object.assign(probe.cases![7], { status: 'error', passed: false, detail: 'probe timed out' });
    showReport(probe);
    expect(await screen.findByText(blockedText)).toBeVisible();
    expect(screen.getByText(probe.reason!)).toBeVisible();
    expect(within(screen.getByText('workspace_escape').closest('li')!).getByText('失败')).toBeVisible();
    expect(within(screen.getByText('job_cleanup').closest('li')!).getByText('错误')).toBeVisible();
    expect(screen.getByText('已执行 8 项，通过 6 项，失败 1 项，错误 1 项，无法判定 0 项')).toBeVisible();
  });

  it('does not trust legacy passed flags without current per-case statuses', async () => {
    showReport({ appcontainer_ok: true, cases: [{ name: 'workspace_escape', security: true, passed: true }] });
    expect(await screen.findByText(blockedText)).toBeVisible();
    expect(screen.getByText('待重新验证')).toBeVisible();
    expect(screen.queryByText(verifiedText)).not.toBeInTheDocument();
  });

  it.each([
    ['empty report', (probe: SandboxProbeReport) => { probe.cases = []; }],
    ['missing case', (probe: SandboxProbeReport) => { probe.cases!.pop(); }],
    ['duplicate case', (probe: SandboxProbeReport) => { probe.cases![7].name = 'network_online'; }],
    ['unknown case', (probe: SandboxProbeReport) => { probe.cases![7].name = 'unknown_probe'; }],
    ['old version', (probe: SandboxProbeReport) => { probe.version = 'native.isolation-probe/v1'; }],
    ['wrong runner', (probe: SandboxProbeReport) => { probe.runner = 'lowil'; }],
    ['missing fingerprint', (probe: SandboxProbeReport) => { probe.environment_key = ''; }],
    ['inconsistent pass flag', (probe: SandboxProbeReport) => { probe.cases![0].passed = false; }],
    ['inconsistent status', (probe: SandboxProbeReport) => { probe.cases![0].status = 'inconclusive'; }],
  ])('does not show a verified banner for %s', async (_name, mutate) => {
    const probe = completeReport();
    mutate(probe);
    showReport(probe);
    expect(await screen.findByText(blockedText)).toBeVisible();
    expect(screen.queryByText(verifiedText)).not.toBeInTheDocument();
  });
});
