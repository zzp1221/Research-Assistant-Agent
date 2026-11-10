import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { PlanReviewPanel } from '../../src/features/review/PlanReviewPanel';
import { graphFromForm, formFromGraph, isFormDirty } from '../../src/features/review/reviewDraft';

const api = vi.hoisted(() => ({
  listRevisions: vi.fn(),
  getRevision: vi.fn(),
  createRevision: vi.fn(),
  validateRevision: vi.fn(),
  approveRevision: vi.fn(),
  rejectRevision: vi.fn(),
  executeRevision: vi.fn(),
  getPlanDiff: vi.fn(),
  getPlan: vi.fn(),
  getPlanEvents: vi.fn(),
}));

vi.mock('../../src/services/api/researchAssistantApi', () => api);

interface NodeSpec {
  id: string;
  name: string;
  retryLimit: number;
  dependencies: string[];
  inputs?: Record<string, unknown>;
}

const defaultNodes = (firstName = 'Generate'): NodeSpec[] => [
  { id: 'n1', name: firstName, retryLimit: 1, dependencies: [], inputs: { prompt: 'v1' } },
  { id: 'n2', name: 'Run', retryLimit: 0, dependencies: ['n1'], inputs: {} },
];

const node = (spec: NodeSpec) => ({
  id: spec.id,
  name: spec.name,
  type: spec.id === 'n1' ? 'generate_code' : 'execute_code',
  description: `${spec.name} description`,
  assigned_to: 'coder_agent',
  status: 'pending',
  dependencies: [...spec.dependencies],
  required_artifacts: [],
  output_artifacts: [],
  parallelizable: false,
  priority: 0,
  retry_limit: spec.retryLimit,
  run_count: 0,
  execution_epoch: 0,
  ...(spec.inputs ? { inputs: { ...spec.inputs } } : {}),
  contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
});

/** A fresh graph per call: the panel must never mutate the fixture. */
const makeGraph = (firstName = 'Generate', budget: Record<string, number> = {}) => ({
  id: 'plan-1',
  title: 'Attention reproduction',
  user_intent: 'reproduce attention',
  intent_type: 'Paper_Reproduction',
  status: 'awaiting_review',
  trace_id: 'trace',
  approval: { required: false, status: 'not_required' },
  budget: {
    max_task_attempts: 10,
    max_duration_seconds: 600,
    max_cost_usd: 1.5,
    max_tokens: 10000,
    ...budget,
  },
  usage: { task_attempts: 0 },
  nodes: defaultNodes(firstName).map(node),
  edges: [{ id: 'e1', from: 'n1', to: 'n2', type: 'control' }],
  artifacts: {},
});

const summary = (revision: number) => ({
  id: `rev-${revision}`,
  plan_id: 'plan-1',
  revision,
  parent_revision: revision - 1,
  status: 'draft',
  created_at: '2026-10-29T01:00:00Z',
  reason: `r${revision} change`,
  hash: `hash-${revision}`,
});

const revisionList = (count: number, currentRevision = 1) => ({
  revisions: Array.from({ length: count }, (_, index) => summary(index + 1)),
  current_revision: currentRevision,
  latest_revision: count,
});

const conflictError = (serverRevision: number) => ({
  response: {
    status: 409,
    data: { code: 'revision_conflict', current_revision: serverRevision, error: 'revision conflict' },
  },
});

const serverError = (status: number, code: string) => ({
  response: { status, data: { code, error: `${code} failed` } },
});

const defer = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

const identity = { userId: 'user-1', sessionId: 'session-1' };

const renderPanel = (props: Partial<Parameters<typeof PlanReviewPanel>[0]> = {}) => {
  const onClose = vi.fn();
  const onSaved = vi.fn();
  const onExecutionStarted = vi.fn();
  const onApplied = vi.fn();
  const view = render(
    <PlanReviewPanel
      planId="plan-1"
      identity={identity}
      onClose={onClose}
      onSaved={onSaved}
      onExecutionStarted={onExecutionStarted}
      onApplied={onApplied}
      {...props}
    />,
  );
  return { ...view, onClose, onSaved, onExecutionStarted, onApplied };
};

const revisionButton = (revision: number) => {
  const match = screen.getAllByRole('button', { name: new RegExp(`^r${revision}\\b`) });
  if (match.length !== 1) throw new Error(`expected exactly one r${revision} button, got ${match.length}`);
  return match[0];
};

const leaveDialogTitle = () => screen.queryByText(/有未保存的修改$/);
const saveButton = () => screen.getByRole('button', { name: '保存草稿' });
const validateButton = () => screen.getByRole('button', { name: /^(校验|保存并校验)$/ });

beforeEach(() => {
  Object.values(api).forEach((mock) => mock.mockReset());
  api.listRevisions.mockResolvedValue(revisionList(2));
  api.getRevision.mockImplementation(async (_planId: string, revision: number) => ({
    ...summary(revision),
    graph: makeGraph(),
  }));
  api.createRevision.mockResolvedValue({ revision: summary(3), plan: {} });
  api.validateRevision.mockResolvedValue({ valid: true, errors: [] });
  api.approveRevision.mockResolvedValue({ status: 'approved', review_status: 'approved' });
  api.rejectRevision.mockResolvedValue({ status: 'rejected', review_status: 'rejected' });
  api.executeRevision.mockResolvedValue({ message: 'started' });
  api.getPlanDiff.mockResolvedValue([
    { kind: 'node_modified', node_id: 'n1', field: 'name', before: 'Generate', after: 'Generate v2' },
  ]);
  api.getPlan.mockResolvedValue({ status: 'pending', current_revision: 2 });
  api.getPlanEvents.mockResolvedValue([]);
});

describe('PlanReviewPanel revision baseline', () => {
  it('lists revisions and loads the selected graph', async () => {
    renderPanel();
    expect(await screen.findByText('r1')).toBeInTheDocument();
    expect(screen.getByText('r2')).toBeInTheDocument();
    expect(await screen.findByDisplayValue('Generate')).toBeInTheDocument();
    expect(screen.getByText(/正在编辑 r2/)).toBeInTheDocument();
    expect(screen.getByText(/当前执行 r1/)).toBeInTheDocument();
    expect(screen.getByText(/最新 r2/)).toBeInTheDocument();
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('已保存');
  });
});

describe('R-01 修改后校验使用服务端返回的新版本', () => {
  it('saves first, then validates the revision number from the response', async () => {
    const { onClose, onSaved, onExecutionStarted, onApplied } = renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('最大 tokens'), { target: { value: '20000' } });
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('有未保存修改');

    await userEvent.click(validateButton());
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));

    const payload = api.createRevision.mock.calls[0][1];
    expect(payload.base_revision).toBe(2);
    await waitFor(() => expect(api.validateRevision).toHaveBeenCalledWith('plan-1', 3));
    // Never validate the revision that is no longer the target.
    expect(api.validateRevision).not.toHaveBeenCalledWith('plan-1', 2);
    await waitFor(() => expect(screen.getByText(/r3 校验通过/)).toBeInTheDocument());

    // The save is a separate notification: the panel stays open.
    expect(onSaved).toHaveBeenCalledWith(3);
    expect(onClose).not.toHaveBeenCalled();
    expect(onExecutionStarted).not.toHaveBeenCalled();
    expect(onApplied).not.toHaveBeenCalled();
    expect(screen.getByText(/正在编辑 r3/)).toBeInTheDocument();
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('已保存');
    // The submitted draft became the new baseline: no leftover dirty state.
    expect(screen.getByLabelText('最大 tokens')).toHaveValue('20000');
  });

  it('keeps required graph fields instead of dropping them from the payload', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('最大 tokens'), { target: { value: '12345' } });
    await userEvent.click(saveButton());
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));

    const graph = api.createRevision.mock.calls[0][1].graph;
    expect(graph.budget.max_duration_seconds).toBe(600);
    expect(graph.budget.max_tokens).toBe(12345);
    expect(graph.nodes[0].retry_limit).toBe(1);
    expect(graph.nodes[1].retry_limit).toBe(0);
  });
});

describe('R-02 离开保护：保存 / 放弃 / 继续编辑', () => {
  it('asks before closing with unsaved edits and keeps them when editing continues', async () => {
    const { onClose } = renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(screen.getByRole('button', { name: '关闭审阅面板' }));
    expect(leaveDialogTitle()).toBeInTheDocument();
    expect(screen.getByText(/r2 有未保存的修改/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(leaveDialogTitle()).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    // The draft survives the cancelled leave intact.
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate v2');
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('有未保存修改');
  });

  it('restores the baseline and proceeds when the edits are discarded', async () => {
    const { onClose } = renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(screen.getByRole('button', { name: '关闭审阅面板' }));
    await userEvent.click(screen.getByRole('button', { name: '放弃修改' }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/r2 有未保存的修改/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate');
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('已保存');
  });

  it('saves then proceeds when "save and continue" is chosen', async () => {
    const { onClose, onSaved } = renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(screen.getByRole('button', { name: '关闭审阅面板' }));
    await userEvent.click(screen.getByRole('button', { name: '保存后继续' }));

    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));
    expect(api.createRevision.mock.calls[0][1].base_revision).toBe(2);
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(3));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('guards revision switching through the same dialog', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(revisionButton(1));
    expect(screen.getByText(/r2 有未保存的修改/)).toBeInTheDocument();
    // Still editing r2 while the prompt is open.
    expect(revisionButton(2)).toHaveAttribute('aria-current', 'true');

    await userEvent.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(revisionButton(2)).toHaveAttribute('aria-current', 'true');
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate v2');

    await userEvent.click(revisionButton(1));
    await userEvent.click(screen.getByRole('button', { name: '放弃修改' }));
    await waitFor(() => expect(revisionButton(1)).toHaveAttribute('aria-current', 'true'));
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate');
  });

  it('exposes a parent guard that resolves only after the three-way choice', async () => {
    const registerGuard = vi.fn();
    renderPanel({ onRegisterLeaveGuard: registerGuard });
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    const guard = registerGuard.mock.calls.map((call) => call[0]).filter(Boolean).pop();
    expect(typeof guard).toBe('function');

    const stayProceed = vi.fn();
    const stayed = guard(stayProceed);
    await waitFor(() => expect(leaveDialogTitle()).toBeInTheDocument());
    await userEvent.click(screen.getByRole('button', { name: '继续编辑' }));
    await expect(stayed).resolves.toBe(false);
    expect(stayProceed).not.toHaveBeenCalled();

    const leaveProceed = vi.fn();
    const left = guard(leaveProceed);
    await waitFor(() => expect(leaveDialogTitle()).toBeInTheDocument());
    await userEvent.click(screen.getByRole('button', { name: '放弃修改' }));
    await expect(left).resolves.toBe(true);
    expect(leaveProceed).toHaveBeenCalledTimes(1);
  });

  it('keeps the draft when Escape is pressed inside the prompt', async () => {
    const { onClose } = renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(screen.getByRole('button', { name: '关闭审阅面板' }));
    const dialog = document.querySelector('dialog');
    expect(dialog).not.toBeNull();
    fireEvent.keyDown(dialog as HTMLDialogElement, { key: 'Escape' });

    await waitFor(() => expect(leaveDialogTitle()).not.toBeInTheDocument());
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate v2');
  });
});

describe('R-03 改回原值即 clean', () => {
  it('drops dirty and the leave prompt when the original value is restored', async () => {
    const { onClose } = renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('有未保存修改');

    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate' } });
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('已保存');
    expect(saveButton()).toBeDisabled();

    await userEvent.click(screen.getByRole('button', { name: '关闭审阅面板' }));
    expect(leaveDialogTitle()).not.toBeInTheDocument();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('R-04 嵌套 inputs、非法数字与校验失效', () => {
  it('detects nested input edits and submits them', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('输入 JSON'), {
      target: { value: '{"prompt":"v2","nested":{"depth":2}}' },
    });
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('有未保存修改');

    await userEvent.click(saveButton());
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));
    const graph = api.createRevision.mock.calls[0][1].graph;
    expect(graph.nodes[0].inputs).toEqual({ prompt: 'v2', nested: { depth: 2 } });
  });

  it('reports an illegal number on its own field and never saves it', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');

    const tokens = screen.getByLabelText('最大 tokens');
    fireEvent.change(tokens, { target: { value: 'abc' } });
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('有未保存修改');

    await userEvent.click(saveButton());
    await waitFor(() => expect(screen.getByText(/有 1 处字段不合法/)).toBeInTheDocument());
    expect(api.createRevision).not.toHaveBeenCalled();
    expect(screen.getAllByText(/最大 tokens必须是数字/).length).toBeGreaterThan(0);
    expect(tokens).toHaveAttribute('aria-invalid', 'true');
    // The raw text is kept: the edit is not silently zeroed away.
    expect(tokens).toHaveValue('abc');
  });

  it('invalidates an existing validation result as soon as the draft changes', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(api.validateRevision).toHaveBeenCalledWith('plan-1', 2));
    expect(await screen.findByText(/校验通过（r2）/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });
    expect(screen.getByText(/未校验（r2）/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /批准并运行 r2/ })).toBeDisabled();
  });
});

describe('R-05 409 冲突保留本地修改', () => {
  it('keeps the draft and the base revision without approving or auto-retrying', async () => {
    api.createRevision.mockRejectedValue(conflictError(4));
    const { onSaved } = renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('最大 tokens'), { target: { value: '20000' } });
    await userEvent.click(saveButton());

    await waitFor(() => expect(screen.getByText(/revision_conflict/)).toBeInTheDocument());
    expect(screen.getByText(/本地修改仍然保留/)).toBeInTheDocument();
    expect(api.createRevision).toHaveBeenCalledTimes(1);
    expect(api.approveRevision).not.toHaveBeenCalled();
    expect(api.executeRevision).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
    // The edit is untouched and execution stays locked.
    expect(screen.getByLabelText('最大 tokens')).toHaveValue('20000');
    expect(screen.getByRole('button', { name: /批准并运行 r2/ })).toBeDisabled();
    expect(screen.getByText(/服务端已有 r4/)).toBeInTheDocument();

    // A second attempt still submits the original base: no silent overwrite.
    await userEvent.click(saveButton());
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(2));
    expect(api.createRevision.mock.calls[1][1].base_revision).toBe(2);
  });
});

describe('R-06 校验失败不审批', () => {
  it('shows the server issues and keeps approve and execute locked', async () => {
    api.validateRevision.mockResolvedValue({
      valid: false,
      errors: [{ node_id: 'n1', field: 'timeout_seconds', message: '超时必须是数字' }],
    });
    renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(api.validateRevision).toHaveBeenCalledWith('plan-1', 2));
    expect(await screen.findByText(/校验未通过（r2）/)).toBeInTheDocument();
    expect(screen.getByText('超时必须是数字')).toBeInTheDocument();

    const approve = screen.getByRole('button', { name: /批准并运行 r2/ });
    expect(approve).toBeDisabled();
    fireEvent.click(approve);
    expect(api.approveRevision).not.toHaveBeenCalled();
    expect(api.executeRevision).not.toHaveBeenCalled();
    // The draft is still there for the user to fix.
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate');
  });

  it('keeps the draft and blocks approval when the validation request itself fails', async () => {
    api.validateRevision.mockRejectedValue(serverError(503, 'plan_running'));
    renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'Generate v2' } });

    await userEvent.click(validateButton());
    await waitFor(() => expect(api.createRevision).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByText(/校验请求失败/)).toBeInTheDocument());

    expect(api.approveRevision).not.toHaveBeenCalled();
    expect(api.executeRevision).not.toHaveBeenCalled();
    expect(screen.getByLabelText('节点名称')).toHaveValue('Generate v2');
  });
});

describe('R-07 双击只有一条在途流程', () => {
  it('creates one revision for a double-clicked save', async () => {
    const pending = defer<{ revision: ReturnType<typeof summary>; plan: unknown }>();
    api.createRevision.mockReturnValue(pending.promise);
    const { onSaved } = renderPanel();
    await screen.findByDisplayValue('Generate');
    fireEvent.change(screen.getByLabelText('最大 tokens'), { target: { value: '20000' } });

    act(() => {
      fireEvent.click(saveButton());
      fireEvent.click(saveButton());
    });
    expect(api.createRevision).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve({ revision: summary(3), plan: {} });
      await pending.promise;
    });
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(3));
    expect(api.createRevision).toHaveBeenCalledTimes(1);
  });

  it('approves and executes once for a double-clicked run', async () => {
    const approving = defer<{ status: string; review_status: string }>();
    api.approveRevision.mockReturnValue(approving.promise);
    renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(screen.getByText(/校验通过（r2）/)).toBeInTheDocument());

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
      fireEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    });
    expect(api.approveRevision).toHaveBeenCalledTimes(1);

    await act(async () => {
      approving.resolve({ status: 'approved', review_status: 'approved' });
      await approving.promise;
    });
    await waitFor(() => expect(api.executeRevision).toHaveBeenCalledTimes(1));
    expect(api.executeRevision).toHaveBeenCalledWith('plan-1', 2);
    expect(api.approveRevision).toHaveBeenCalledTimes(1);
  });
});

describe('R-08 历史只读、启动重试与结果不明', () => {
  it('keeps a history revision read-only and shows its diff against the latest', async () => {
    renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(revisionButton(1));
    await waitFor(() => expect(revisionButton(1)).toHaveAttribute('aria-current', 'true'));
    expect((await screen.findAllByText(/历史版本只读/)).length).toBeGreaterThan(0);
    expect(screen.getByLabelText('最大 tokens')).toBeDisabled();
    expect(screen.getByLabelText('节点名称')).toBeDisabled();
    expect(saveButton()).toBeDisabled();
    expect(api.createRevision).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole('button', { name: /基于此版本创建草稿/ }));
    await waitFor(() => expect(api.getPlanDiff).toHaveBeenCalledWith('plan-1', 1, 2));
    expect(await screen.findByText(/DIFF r1 → r2/)).toBeInTheDocument();
    expect(screen.getByText('Generate v2')).toBeInTheDocument();
    // The fork is an explicit action: now the form is editable again.
    await waitFor(() => expect(screen.getByLabelText('节点名称')).not.toBeDisabled());
    expect(screen.getByText(/基于 r1 的新草稿/)).toBeInTheDocument();
  });

  it('offers an execute-only retry after a definite startup rejection', async () => {
    api.executeRevision.mockRejectedValue({
      response: { status: 409, data: { code: 'plan_running', error: 'plan already running' } },
    });
    const { onExecutionStarted } = renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(screen.getByText(/校验通过（r2）/)).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(api.executeRevision).toHaveBeenCalledTimes(1));
    expect(api.approveRevision).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.getByText(/plan_running/)).toBeInTheDocument());
    expect(screen.getAllByText(/已批准，启动失败/).length).toBeGreaterThan(0);
    expect(screen.getByText(/可以只重试启动/)).toBeInTheDocument();
    expect(onExecutionStarted).not.toHaveBeenCalled();

    const retry = await screen.findByRole('button', { name: /重试启动 r2/ });
    api.executeRevision.mockResolvedValue({ message: 'started' });
    await userEvent.click(retry);
    await waitFor(() => expect(api.executeRevision).toHaveBeenCalledTimes(2));
    expect(api.executeRevision).toHaveBeenLastCalledWith('plan-1', 2);
    // The approval was not repeated for the same revision.
    expect(api.approveRevision).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(onExecutionStarted).toHaveBeenCalledWith(2));
  });

  it('queries the server first and keeps the lock when a start response is lost', async () => {
    api.executeRevision.mockRejectedValue({ message: 'Network Error' });
    api.getPlan.mockResolvedValue({ status: 'pending', current_revision: 2 });
    const { onExecutionStarted } = renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(screen.getByText(/校验通过（r2）/)).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(api.executeRevision).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('review-state-chip')).toHaveTextContent('启动结果待确认'));

    // The lost response was resolved by reading the plan, not by re-executing.
    expect(api.getPlan).toHaveBeenCalledWith('plan-1', identity);
    expect(api.executeRevision).toHaveBeenCalledTimes(1);
    expect(onExecutionStarted).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /重试启动 r2/ })).not.toBeInTheDocument();

    // A pending snapshot is not proof of "not started", so re-checking keeps
    // the lock instead of unlocking a duplicate start.
    await userEvent.click(screen.getByRole('button', { name: '核对启动状态' }));
    await waitFor(() => expect(screen.getByText(/仍无法确认 r2 的启动状态/)).toBeInTheDocument());
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('启动结果待确认');
    expect(api.executeRevision).toHaveBeenCalledTimes(1);
    expect(onExecutionStarted).not.toHaveBeenCalled();
  });

  it('stops before executing when the approval outcome is unknown', async () => {
    api.approveRevision.mockRejectedValue(serverError(502, 'plan_update_failed'));
    renderPanel();
    await screen.findByDisplayValue('Generate');

    await userEvent.click(validateButton());
    await waitFor(() => expect(screen.getByText(/校验通过（r2）/)).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: /批准并运行 r2/ }));
    await waitFor(() => expect(screen.getByTestId('review-state-chip')).toHaveTextContent('批准结果待确认'));
    expect(api.approveRevision).toHaveBeenCalledTimes(1);
    // A 5xx may have already approved: never start on top of it.
    expect(api.executeRevision).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '核对批准状态' })).toBeInTheDocument();

    api.listRevisions.mockResolvedValue({
      revisions: [{ ...summary(1) }, { ...summary(2), status: 'approved' }],
      current_revision: 1,
      latest_revision: 2,
    });
    await userEvent.click(screen.getByRole('button', { name: '核对批准状态' }));
    await waitFor(() => expect(screen.getByText(/可以只重试启动/)).toBeInTheDocument());
    expect(api.approveRevision).toHaveBeenCalledTimes(1);
    expect(api.executeRevision).not.toHaveBeenCalled();
  });
});

describe('R-09 保存成功后基线与慢返回隔离', () => {
  it('reconciles a 5xx save that was already written without creating a duplicate', async () => {
    api.createRevision.mockRejectedValue(serverError(500, 'plan_update_failed'));
    const { onSaved } = renderPanel();
    await screen.findByDisplayValue('Generate');

    fireEvent.change(screen.getByLabelText('最大 tokens'), { target: { value: '20000' } });
    await userEvent.click(saveButton());

    await waitFor(() => expect(screen.getByTestId('review-state-chip')).toHaveTextContent('保存结果待确认'));
    expect(api.createRevision).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText('最大 tokens')).toHaveValue('20000');
    expect(screen.getByRole('button', { name: /批准并运行 r2/ })).toBeDisabled();

    // The write did land: the server holds r3 with exactly the local content.
    api.listRevisions.mockResolvedValue(revisionList(3, 2));
    api.getRevision.mockImplementation(async (_planId: string, revision: number) => ({
      ...summary(revision),
      graph: revision === 3 ? makeGraph('Generate', { max_tokens: 20000 }) : makeGraph(),
    }));

    await userEvent.click(screen.getByRole('button', { name: '核对服务端版本' }));
    await waitFor(() => expect(screen.getByText(/已找到服务端 r3/)).toBeInTheDocument());

    // No second createRevision: reconciliation adopted the existing row.
    expect(api.createRevision).toHaveBeenCalledTimes(1);
    expect(onSaved).toHaveBeenCalledWith(3);
    expect(screen.getByText(/正在编辑 r3/)).toBeInTheDocument();
    expect(screen.getByTestId('review-state-chip')).toHaveTextContent('已保存');
  });

  it('does not let a slow response from the previous plan repaint the new one', async () => {
    const slow = defer<{ revision: number; graph: unknown }>();
    api.listRevisions.mockImplementation(async (planId: string) =>
      planId === 'plan-A' ? { revisions: [summary(2)], current_revision: 1, latest_revision: 2 } : { revisions: [summary(5)], current_revision: 5, latest_revision: 5 },
    );
    api.getRevision.mockImplementation((planId: string, revision: number) =>
      planId === 'plan-A'
        ? slow.promise
        : Promise.resolve({ ...summary(revision), graph: makeGraph('Plan B node') }),
    );

    const view = renderPanel({ planId: 'plan-A' });
    await waitFor(() => expect(api.getRevision).toHaveBeenCalledWith('plan-A', 2));

    view.rerender(
      <PlanReviewPanel
        planId="plan-B"
        identity={identity}
        onClose={view.onClose}
        onSaved={view.onSaved}
        onExecutionStarted={view.onExecutionStarted}
      />,
    );
    expect(await screen.findByDisplayValue('Plan B node')).toBeInTheDocument();

    await act(async () => {
      slow.resolve({ ...summary(2), graph: makeGraph('Stale A node') });
      await slow.promise;
    });

    expect(screen.queryByDisplayValue('Stale A node')).not.toBeInTheDocument();
    expect(screen.getByDisplayValue('Plan B node')).toBeInTheDocument();
    expect(screen.getByText(/正在编辑 r5/)).toBeInTheDocument();
  });
});

describe('reviewDraft graph construction', () => {
  it('never deletes required numeric fields from the submitted graph', () => {
    const base = makeGraph();
    const form = formFromGraph(base as never);
    expect(form.maxDurationSeconds).toBe('600');

    // A cleared required field is a validation problem, not a reason to send a
    // graph that would fail server-side.
    const graph = graphFromForm(base as never, { ...form, maxDurationSeconds: '   ' });
    expect(graph.budget.max_duration_seconds).toBe(600);
    expect(graph.nodes[0].retry_limit).toBe(1);
  });

  it('does not invent zeroes or an empty inputs object for broken drafts', () => {
    const base = makeGraph();
    const form = formFromGraph(base as never);
    const broken = {
      ...form,
      maxCostUSD: '',
      nodes: form.nodes.map((entry) => (entry.id === 'n1' ? { ...entry, inputsText: '{not json' } : entry)),
    };
    const graph = graphFromForm(base as never, broken);
    expect(graph.budget.max_cost_usd).toBeUndefined();
    expect(graph.nodes[0].inputs).toEqual({ prompt: 'v1' });
  });

  it('treats text that differs only by surrounding whitespace as clean', () => {
    const base = makeGraph();
    const form = formFromGraph(base as never);
    const padded = { ...form, title: `  ${form.title}  ` };
    expect(isFormDirty(padded, form)).toBe(false);
    // ...and the same whitespace never reaches the server.
    expect(graphFromForm(base as never, padded).title).toBe(form.title);
  });
});
