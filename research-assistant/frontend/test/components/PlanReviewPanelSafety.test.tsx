import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PlanEvent, PlanGraph, PlanRevisionSummary } from '../../src/contracts/api';
import { PlanReviewPanel } from '../../src/features/review/PlanReviewPanel';
import * as api from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  listRevisions: vi.fn(), getRevision: vi.fn(), createRevision: vi.fn(),
  validateRevision: vi.fn(), approveRevision: vi.fn(), rejectRevision: vi.fn(),
  executeRevision: vi.fn(), getPlanDiff: vi.fn(), getPlan: vi.fn(), getPlanEvents: vi.fn(),
}));

const graph = {
  id: 'plan-1', title: '研究计划', user_intent: '研究', intent_type: 'Code_Execution',
  status: 'awaiting_review', current_revision: 2, trace_id: 'trace',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 10, max_duration_seconds: 600, max_cost_usd: 1, max_tokens: 1000 },
  usage: { task_attempts: 0 },
  nodes: [{
    id: 'n1', name: '原始节点', type: 'generate_code', description: '', assigned_to: 'coder_agent',
    status: 'pending', dependencies: [], required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, timeout_seconds: 30,
    run_count: 0, execution_epoch: 0, inputs: { nested: { seed: 1 } },
    contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
  }], edges: [], artifacts: {},
} as PlanGraph;

const summary = (revision: number, planId = 'plan-1'): PlanRevisionSummary => ({
  id: `${planId}-r${revision}`, plan_id: planId, revision, parent_revision: revision - 1,
  status: 'draft', created_at: '2026-11-09T01:00:00Z',
});

let serverRevisions: PlanRevisionSummary[];
const identity = { userId: 'user', sessionId: 'session' };
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

const nativeShow = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const nativeClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true, value(this: HTMLDialogElement) { this.open = true; },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true, value(this: HTMLDialogElement) { this.open = false; },
  });
});
afterAll(() => {
  if (nativeShow) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', nativeShow);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  if (nativeClose) Object.defineProperty(HTMLDialogElement.prototype, 'close', nativeClose);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
});

beforeEach(() => {
  vi.resetAllMocks();
  serverRevisions = [summary(1), summary(2)];
  vi.mocked(api.listRevisions).mockImplementation(async () => ({
    revisions: [...serverRevisions], current_revision: 2,
    latest_revision: Math.max(...serverRevisions.map((entry) => entry.revision)),
  }));
  vi.mocked(api.getRevision).mockImplementation(async (planId, revision) => ({
    ...summary(revision, planId), graph: { ...structuredClone(graph), id: planId },
  }));
  vi.mocked(api.validateRevision).mockResolvedValue({ valid: true, errors: [] });
  vi.mocked(api.approveRevision).mockResolvedValue({ status: 'approved', review_status: 'approved' });
  vi.mocked(api.executeRevision).mockResolvedValue({ message: 'started' });
  vi.mocked(api.getPlanDiff).mockResolvedValue([]);
  vi.mocked(api.getPlanEvents).mockResolvedValue([]);
  vi.mocked(api.getPlan).mockResolvedValue({ ...graph, status: 'pending' });
  vi.mocked(api.createRevision).mockImplementation(async () => {
    serverRevisions.push(summary(3));
    return { revision: summary(3), plan: {} };
  });
});

const editName = (name = '本地编辑') => fireEvent.change(screen.getByDisplayValue('原始节点'), { target: { value: name } });
const validate = async () => {
  fireEvent.click(screen.getByRole('button', { name: /^校验$/ }));
  await waitFor(() => expect(screen.getByRole('button', { name: /批准并运行 r2/ })).toBeEnabled());
};

describe('PlanReviewPanel safety boundaries', () => {
  it('allows editing a legacy graph whose optional display title and node name are empty', async () => {
    const legacy = structuredClone(graph);
    delete legacy.title;
    legacy.nodes[0].name = '';
    vi.mocked(api.getRevision).mockImplementation(async (planId, revision) => ({ ...summary(revision, planId), graph: legacy }));
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    const description = await screen.findByRole('textbox', { name: '节点描述' });
    fireEvent.change(description, { target: { value: '补充研究说明' } });
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledOnce());
    expect(vi.mocked(api.createRevision).mock.calls[0][1].graph.title).toBe('');
  });

  it('keeps an unresolved save frozen when a history read does not yet show the submitted revision', async () => {
    vi.mocked(api.createRevision).mockRejectedValue({ code: 'ERR_NETWORK', message: 'Network Error' });
    render(<PlanReviewPanel planId="plan-1" identity={identity} onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    editName();
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await screen.findByText('保存结果待确认', { exact: true });
    expect(screen.getByDisplayValue('本地编辑')).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '核对服务端版本' }));
    await waitFor(() => expect(api.listRevisions).toHaveBeenCalledTimes(2));
    expect(screen.getByText('保存结果待确认', { exact: true })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    expect(api.createRevision).toHaveBeenCalledTimes(1);
  });

  it('blocks the approved retry entry while execution acceptance is unknown', async () => {
    vi.mocked(api.executeRevision).mockRejectedValue({ code: 'ERR_NETWORK', message: 'Network Error' });
    render(<PlanReviewPanel planId="plan-1" identity={identity} onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    await validate();
    fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await screen.findByText('启动结果待确认', { exact: true });
    const retry = screen.getByRole('button', { name: /(批准并运行|重试启动) r2/ });
    expect(retry).toBeDisabled();
    fireEvent.click(retry);
    fireEvent.click(screen.getByRole('button', { name: '核对启动状态' }));
    await waitFor(() => expect(api.getPlan).toHaveBeenCalled());
    expect(screen.getByText('启动结果待确认', { exact: true })).toBeInTheDocument();
    expect(api.executeRevision).toHaveBeenCalledTimes(1);
  });

  it('requires saving and validating edits made after a definite start rejection', async () => {
    vi.mocked(api.executeRevision).mockRejectedValue({ response: { status: 409, data: { code: 'plan_paused', error: 'paused' } } });
    render(<PlanReviewPanel planId="plan-1" identity={identity} onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    await validate();
    fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(screen.getByRole('button', { name: /重试启动 r2/ })).toBeEnabled());
    editName();
    expect(screen.getByRole('button', { name: /重试启动 r2/ })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /重试启动 r2/ }));
    expect(api.executeRevision).toHaveBeenCalledTimes(1);
  });

  it('does not repeat approval or start execution after an ambiguous approval failure', async () => {
    vi.mocked(api.approveRevision).mockRejectedValue({ response: { status: 503, data: { error: 'temporarily unavailable' } } });
    render(<PlanReviewPanel planId="plan-1" identity={identity} onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    await validate();
    fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(api.approveRevision).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole('button', { name: /批准并运行 r2/ })).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    expect(api.approveRevision).toHaveBeenCalledOnce();
    expect(api.executeRevision).not.toHaveBeenCalled();
  });

  it('recognizes the first numbered start after a successfully read empty history', async () => {
    vi.mocked(api.executeRevision).mockRejectedValue({ code: 'ERR_NETWORK', message: 'Network Error' });
    vi.mocked(api.getPlanEvents).mockResolvedValueOnce([]).mockResolvedValue([{
      plan_id: 'plan-1', event_type: 'plan_revision_executed', seq: 1,
      payload: { revision: 2 }, timestamp: '2000-01-01T00:00:00Z',
    } as PlanEvent]);
    vi.mocked(api.getPlan).mockResolvedValue({ ...graph, status: 'completed' });
    const onStarted = vi.fn();
    render(<PlanReviewPanel planId="plan-1" identity={identity} onClose={vi.fn()} onExecutionStarted={onStarted} />);
    await screen.findByDisplayValue('原始节点');
    await validate();
    fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(onStarted).toHaveBeenCalledExactlyOnceWith(2));
    expect(api.executeRevision).toHaveBeenCalledOnce();
  });

  it('keeps a conflicted local draft editable after learning about a newer server revision', async () => {
    vi.mocked(api.createRevision).mockImplementation(async () => {
      serverRevisions.push(summary(3));
      throw { response: { status: 409, data: { code: 'revision_conflict', current_revision: 3 } } };
    });
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    editName();
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await screen.findByRole('button', { name: /以服务端 r3 为基准继续编辑/ });
    await waitFor(() => expect(screen.getByText('最新 r3')).toBeInTheDocument());
    expect(screen.getByDisplayValue('本地编辑')).toBeEnabled();
    expect(api.createRevision).toHaveBeenCalledTimes(1);
    expect(api.approveRevision).not.toHaveBeenCalled();
  });

  it('lets an explicit conflict resolution save the retained draft against the compared server base', async () => {
    vi.mocked(api.createRevision).mockImplementationOnce(async () => {
      serverRevisions.push(summary(3));
      throw { response: { status: 409, data: { code: 'revision_conflict', current_revision: 3 } } };
    }).mockResolvedValueOnce({ revision: summary(4), plan: {} });
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    editName();
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    const adopt = await screen.findByRole('button', { name: /以服务端 r3 为基准继续编辑/ });
    fireEvent.click(adopt);
    expect(screen.getByDisplayValue('本地编辑')).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.createRevision).mock.calls[1][1].base_revision).toBe(3);
    expect(vi.mocked(api.createRevision).mock.calls[1][1].graph.nodes?.[0].name).toBe('本地编辑');
  });

  it('allows explicitly forking an unchanged historical snapshot into a new saved revision', async () => {
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    fireEvent.click(screen.getByRole('button', { name: /^r1/ }));
    await screen.findByText('· 历史版本只读');
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: /基于此版本创建草稿/ }));
    await waitFor(() => expect(screen.getByRole('button', { name: '保存草稿' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.createRevision).mock.calls[0][1].base_revision).toBe(2);
  });

  it('returns a historical snapshot to read-only after leaving an unsaved fork', async () => {
    const comparison = deferred<Awaited<ReturnType<typeof api.getPlanDiff>>>();
    vi.mocked(api.getPlanDiff).mockReturnValue(comparison.promise);
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    fireEvent.click(screen.getByRole('button', { name: /^r1/ }));
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: /基于此版本创建草稿/ }));
    await waitFor(() => expect(api.getPlanDiff).toHaveBeenCalledExactlyOnceWith('plan-1', 1, 2));
    // Finish the automatic comparison before exercising version navigation.
    await act(async () => { comparison.resolve([]); });
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: /^r2/ }));
    fireEvent.click(await screen.findByRole('button', { name: '放弃修改' }));
    await waitFor(() => expect(screen.getByText('正在编辑 r2')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /^r1/ }));
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeDisabled());
  });

  it('saves and validates a newly forked snapshot before it can be approved', async () => {
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} />);
    await screen.findByDisplayValue('原始节点');
    fireEvent.click(screen.getByRole('button', { name: /^r1/ }));
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: /基于此版本创建草稿/ }));
    await waitFor(() => expect(screen.getByRole('button', { name: '保存并校验' })).toBeEnabled());
    expect(screen.getByText('有未保存修改', { exact: true })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '保存并校验' }));
    await waitFor(() => expect(api.validateRevision).toHaveBeenCalledExactlyOnceWith('plan-1', 3));
    expect(api.createRevision).toHaveBeenCalledOnce();
    expect(api.approveRevision).not.toHaveBeenCalled();
  });

  it('ignores a save response arriving after the panel unmounts', async () => {
    const request = deferred<Awaited<ReturnType<typeof api.createRevision>>>();
    vi.mocked(api.createRevision).mockReturnValue(request.promise);
    const onSaved = vi.fn();
    const view = render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} onSaved={onSaved} />);
    await screen.findByDisplayValue('原始节点');
    editName();
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledOnce());
    view.unmount();
    await act(async () => { request.resolve({ revision: summary(3), plan: {} }); });
    expect(onSaved).not.toHaveBeenCalled();
    expect(api.getRevision).not.toHaveBeenCalledWith('plan-1', 3);
  });

  it('does not let an old generation completion unlock a new plan submission', async () => {
    const first = deferred<Awaited<ReturnType<typeof api.createRevision>>>();
    const second = deferred<Awaited<ReturnType<typeof api.createRevision>>>();
    vi.mocked(api.createRevision).mockImplementation((planId) => planId === 'plan-1' ? first.promise : second.promise);
    const onSaved = vi.fn();
    const view = render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} onSaved={onSaved} />);
    await screen.findByDisplayValue('原始节点');
    editName('计划A编辑');
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));
    view.rerender(<PlanReviewPanel planId="plan-2" onClose={vi.fn()} onSaved={onSaved} />);
    await waitFor(() => expect(screen.getByDisplayValue('原始节点')).toBeEnabled());
    editName('计划B编辑');
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(2));
    await act(async () => { first.resolve({ revision: summary(3), plan: {} }); });
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    expect(screen.getByDisplayValue('计划B编辑')).toBeDisabled();
    expect(onSaved).not.toHaveBeenCalled();
    await act(async () => { second.resolve({ revision: summary(3, 'plan-2'), plan: {} }); });
    await waitFor(() => expect(onSaved).toHaveBeenCalledExactlyOnceWith(3));
  });

  it('deduplicates simultaneous parent leave requests without replacing the first continuation', async () => {
    type LeaveGuard = (proceed: () => void) => Promise<boolean>;
    let guard: LeaveGuard | null = null;
    const register = (value: LeaveGuard | null) => { guard = value; };
    render(<PlanReviewPanel planId="plan-1" onClose={vi.fn()} onRegisterLeaveGuard={register} />);
    await screen.findByDisplayValue('原始节点');
    editName();
    const first = vi.fn();
    const second = vi.fn();
    await act(async () => { void guard?.(first); void guard?.(second); });
    fireEvent.click(screen.getByRole('button', { name: '放弃修改' }));
    await waitFor(() => expect(first).toHaveBeenCalledOnce());
    expect(second).not.toHaveBeenCalled();
  });
});
