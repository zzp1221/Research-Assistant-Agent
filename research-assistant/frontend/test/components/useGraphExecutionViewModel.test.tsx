import { describe, expect, it, vi } from 'vitest';
import type { PropsWithChildren } from 'react';
import { act, renderHook } from '@testing-library/react';
import type { Task } from '../../src/contracts/api';
import { ResearchAssistantRuntimeContext } from '../../src/app/context/ResearchAssistantRuntimeContext';
import type { ResearchAssistantRuntimeContextValue } from '../../src/app/context/ResearchAssistantRuntimeContext';
import { resolvePlanObservation, useGraphExecutionViewModel } from '../../src/app/viewModels/useGraphExecutionViewModel';
import type { useResearchAssistantLayoutState } from '../../src/app/hooks/useResearchAssistantLayoutState';

const PLAN_ID = 'plan-active';
const OTHER_PLAN_ID = 'plan-other';

const selectedTask: Task = {
  ID: 'task-1',
  Name: 'Run baseline',
  Type: 'experiment_run',
  Description: 'Run baseline',
  AssignedTo: 'data_agent',
  Status: 'ready',
  Dependencies: [],
};

interface RuntimeStubOptions {
  isExecuting?: boolean;
  connectionState?: 'idle' | 'connecting' | 'live' | 'reconnecting' | 'interrupted';
  connectionDetail?: string;
  startAccepted?: boolean;
  serverPlanStatus?: string;
  planPaused?: boolean;
  observedPlanId?: string;
  withSelectedTask?: boolean;
  withReconnect?: boolean;
  withObservePlan?: boolean;
  withResumePlan?: boolean;
}

const makeRuntimeValue = (options: RuntimeStubOptions = {}) => {
  const handleExecuteTask = vi.fn();
  const handleRunAllTasks = vi.fn();
  const handleCancelPlan = vi.fn();
  const handleRetryFailedPlan = vi.fn();
  const handleApproveAndRun = vi.fn();
  const handleReconnect = vi.fn(async () => undefined);
  const observePlan = vi.fn(async () => undefined);
  const handleResumePlan = vi.fn(async () => undefined);

  const value = {
    state: {
      executionState: {
        selectedTask: options.withSelectedTask ? selectedTask : null,
        nodeStates: {},
        displayMode: 'logs',
        isExecuting: options.isExecuting ?? false,
        approvalResolved: false,
        connectionState: options.connectionState,
        connectionDetail: options.connectionDetail,
        startAccepted: options.startAccepted,
        serverPlanStatus: options.serverPlanStatus,
        planPaused: options.planPaused,
        observedPlanId: options.observedPlanId,
      },
      selectedTaskState: { logs: '', result: '', code: '', structuredData: '', imageBase64: '' },
    },
    actions: {
      onNodeClick: vi.fn(),
      handleOpenTaskView: vi.fn(),
      handleExecuteTask,
      handleRunAllTasks,
      handleApproveAndRun,
      handleRuntimeActionDecision: vi.fn(),
      handleCancelPlan,
      handleRetryFailedPlan,
      setDisplayMode: vi.fn(),
      closeTaskPanel: vi.fn(),
      resetRuntimeState: vi.fn(),
      ...(options.withReconnect === false ? {} : { handleReconnect }),
      ...(options.withObservePlan === false ? {} : { observePlan }),
      ...(options.withResumePlan === false ? {} : { handleResumePlan }),
    },
    meta: { appendSelectedTaskLog: vi.fn() },
  } as unknown as ResearchAssistantRuntimeContextValue;

  return { value, handleExecuteTask, handleRunAllTasks, handleCancelPlan, handleRetryFailedPlan, handleApproveAndRun, handleReconnect, observePlan, handleResumePlan };
};

const layout = { sidebarWidth: 440 } as unknown as ReturnType<typeof useResearchAssistantLayoutState>;

const renderViewModel = (
  runtime: ResearchAssistantRuntimeContextValue,
  plan: { id?: string | null; status?: string | null } = {},
) => {
  const wrapper = ({ children }: PropsWithChildren) => (
    <ResearchAssistantRuntimeContext.Provider value={runtime}>{children}</ResearchAssistantRuntimeContext.Provider>
  );
  return renderHook(
    () => useGraphExecutionViewModel({
      nodes: [],
      edges: [],
      onNodesChange: vi.fn(),
      onEdgesChange: vi.fn(),
      intentContext: null,
      activePlanId: plan.id === undefined ? PLAN_ID : plan.id,
      activePlanStatus: plan.status === undefined ? 'pending' : plan.status,
      activePlanRuntimeActions: [],
      activePlanQuestions: [],
      layout,
      logsEndRef: { current: null },
    }),
    { wrapper },
  );
};

describe('resolvePlanObservation', () => {
  it('uses the runtime server status when it belongs to the active plan', () => {
    const snapshot = resolvePlanObservation(PLAN_ID, 'pending', {
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      connectionState: 'live',
    });
    expect(snapshot.status).toBe('in_progress');
    expect(snapshot.businessState).toBe('observing');
    expect(snapshot.connectionState).toBe('live');
    expect(snapshot.observationMismatch).toBe(false);
  });

  it('does not borrow a stale plan status when the observed plan differs', () => {
    const snapshot = resolvePlanObservation(PLAN_ID, 'ready', {
      observedPlanId: OTHER_PLAN_ID,
      serverPlanStatus: 'in_progress',
      planPaused: true,
      startAccepted: true,
      connectionState: 'live',
    });
    expect(snapshot.status).toBe('ready');
    expect(snapshot.businessState).toBe('runnable');
    expect(snapshot.paused).toBe(false);
    expect(snapshot.startAccepted).toBe(false);
    expect(snapshot.connectionState).toBeUndefined();
    expect(snapshot.observationMismatch).toBe(true);
  });

  it('keeps the plan snapshot while the runtime has no observation yet', () => {
    const snapshot = resolvePlanObservation(PLAN_ID, 'awaiting_input', {});
    expect(snapshot.status).toBe('awaiting_input');
    expect(snapshot.businessState).toBe('awaiting_input');
    expect(snapshot.startAccepted).toBe(false);
    expect(snapshot.observationMismatch).toBe(false);
  });
});

describe('useGraphExecutionViewModel business gating', () => {
  it('shows cancel (not run) for a running plan discovered after refresh', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'in_progress', connectionState: 'live' });
    const { result } = renderViewModel(runtime.value, { status: 'pending' });

    const props = result.current.graphPanelProps;
    expect(props.planStatus).toBe('in_progress');
    expect(props.canRun).toBe(false);
    expect(props.isExecuting).toBe(false);
    expect(props.connectionState).toBe('live');
  });

  it('uses the runtime snapshot as the business state and passes it to the canvas', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'awaiting_input' });
    const { result } = renderViewModel(runtime.value, { status: 'pending' });
    expect(result.current.graphPanelProps.planStatus).toBe('awaiting_input');
    expect(result.current.graphPanelProps.canRun).toBe(false);
    expect(result.current.planBusinessState).toBe('awaiting_input');
  });

  it.each(['awaiting_input', 'awaiting_approval', 'awaiting_review', 'blocked', 'paused', 'completed', 'failed', 'canceled', 'weird_state'])(
    'refuses a plain start for %s',
    (serverPlanStatus) => {
      const runtime = makeRuntimeValue({
        observedPlanId: PLAN_ID,
        serverPlanStatus: serverPlanStatus === 'paused' ? 'in_progress' : serverPlanStatus,
        planPaused: serverPlanStatus === 'paused',
      });
      const { result } = renderViewModel(runtime.value, { status: 'pending' });
      expect(result.current.graphPanelProps.canRun).toBe(false);
    },
  );

  it('allows a start for a runnable plan that was not accepted yet', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'ready' });
    const { result } = renderViewModel(runtime.value, { status: 'ready' });
    expect(result.current.graphPanelProps.canRun).toBe(true);
    expect(result.current.graphPanelProps.startAccepted).toBe(false);
    expect(result.current.graphPanelProps.planPaused).toBe(false);
  });

  it('forbids a second start after the 202 acknowledgement', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'pending', startAccepted: true });
    const { result } = renderViewModel(runtime.value, { status: 'pending' });
    expect(result.current.graphPanelProps.canRun).toBe(false);
    expect(result.current.graphPanelProps.startAccepted).toBe(true);
  });

  it('forbids a start while the plan is paused even if the status is runnable', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'ready', planPaused: true });
    const { result } = renderViewModel(runtime.value, { status: 'ready' });
    expect(result.current.graphPanelProps.canRun).toBe(false);
    expect(result.current.graphPanelProps.planPaused).toBe(true);
  });

  it('keeps the approval gate but disables the plan-level approve while a runtime action is pending', () => {
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'awaiting_approval' });
    const wrapper = ({ children }: PropsWithChildren) => (
      <ResearchAssistantRuntimeContext.Provider value={runtime.value}>{children}</ResearchAssistantRuntimeContext.Provider>
    );
    const { result } = renderHook(
      () => useGraphExecutionViewModel({
        nodes: [],
        edges: [],
        onNodesChange: vi.fn(),
        onEdgesChange: vi.fn(),
        intentContext: null,
        activePlanId: PLAN_ID,
        activePlanStatus: 'awaiting_approval',
        activePlanRuntimeActions: [{
          id: 'ra-1',
          plan_id: PLAN_ID,
          task_id: 'task-1',
          node_type: 'install_dependencies',
          kind: 'dependency_install',
          title: '安装依赖',
          status: 'pending',
        }],
        layout,
        logsEndRef: { current: null },
      }),
      { wrapper },
    );

    expect(result.current.graphPanelProps.requiresApproval).toBe(false);
    expect(result.current.graphPanelProps.canRun).toBe(false);
  });

  it.each(['awaiting_approval', 'awaiting_input'] as const)(
    'keeps the %s waiting gate available while the runtime observes the plan as executing',
    (serverPlanStatus) => {
      const runtime = makeRuntimeValue({
        observedPlanId: PLAN_ID,
        serverPlanStatus,
        isExecuting: true,
      });
      const { result } = renderViewModel(runtime.value, { status: serverPlanStatus });

      const props = result.current.graphPanelProps;
      // The runtime reports isExecuting for these started-but-waiting plans;
      // the business state must still drive the approval/question entry.
      expect(props.isExecuting).toBe(true);
      expect(props.planStatus).toBe(serverPlanStatus);
      expect(props.canRun).toBe(false);
      expect(props.requiresApproval).toBe(serverPlanStatus === 'awaiting_approval');
      expect(props.startAccepted).toBe(false);
    },
  );

  it('keeps the plan-level approval clickable (not a run) while executing', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'awaiting_approval',
      isExecuting: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'awaiting_approval' });

    result.current.graphPanelProps.onApproveAndRun();
    expect(runtime.handleApproveAndRun).toHaveBeenCalledWith(PLAN_ID);
    expect(runtime.handleRunAllTasks).not.toHaveBeenCalled();
  });

  it('wires retry and review entries separately for a failed terminal plan', () => {
    const onOpenReview = vi.fn();
    const runtime = makeRuntimeValue({ observedPlanId: PLAN_ID, serverPlanStatus: 'failed' });
    const wrapper = ({ children }: PropsWithChildren) => (
      <ResearchAssistantRuntimeContext.Provider value={runtime.value}>{children}</ResearchAssistantRuntimeContext.Provider>
    );
    const { result } = renderHook(
      () => useGraphExecutionViewModel({
        nodes: [],
        edges: [],
        onNodesChange: vi.fn(),
        onEdgesChange: vi.fn(),
        intentContext: null,
        activePlanId: PLAN_ID,
        activePlanStatus: 'failed',
        onOpenReview,
        layout,
        logsEndRef: { current: null },
      }),
      { wrapper },
    );

    const props = result.current.graphPanelProps;
    expect(props.planStatus).toBe('failed');
    expect(props.canRun).toBe(false);
    // The canvas decides between the two from the failed-node count; the view
    // model must hand over both handlers, and retry must stay node-scoped.
    expect(typeof props.onRetryFailed).toBe('function');
    expect(props.onOpenReview).toBe(onOpenReview);
    props.onRetryFailed();
    expect(runtime.handleRetryFailedPlan).toHaveBeenCalledWith(PLAN_ID);
    expect(runtime.handleRunAllTasks).not.toHaveBeenCalled();
  });

  it('ignores the observation of a different plan', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: OTHER_PLAN_ID,
      serverPlanStatus: 'in_progress',
      planPaused: true,
      connectionState: 'live',
    });
    const { result } = renderViewModel(runtime.value, { status: 'ready' });

    expect(result.current.graphPanelProps.planStatus).toBe('ready');
    expect(result.current.graphPanelProps.canRun).toBe(true);
    expect(result.current.graphPanelProps.planPaused).toBe(false);
    expect(result.current.graphPanelProps.connectionState).toBeUndefined();
    expect(result.current.planObservation.observationMismatch).toBe(true);
  });

  it('reconnects without executing when the stream was interrupted', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      connectionState: 'interrupted',
      connectionDetail: '重连次数已用尽',
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    expect(result.current.graphPanelProps.connectionState).toBe('interrupted');
    expect(result.current.graphPanelProps.connectionDetail).toBe('重连次数已用尽');
    expect(result.current.graphPanelProps.canRun).toBe(false);

    result.current.graphPanelProps.onReconnect?.();
    expect(runtime.handleReconnect).toHaveBeenCalledTimes(1);
    expect(runtime.observePlan).not.toHaveBeenCalled();
    expect(runtime.handleRunAllTasks).not.toHaveBeenCalled();
    expect(runtime.handleRetryFailedPlan).not.toHaveBeenCalled();
  });

  it('falls back to observePlan when the runtime has no handleReconnect yet', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      connectionState: 'interrupted',
      withReconnect: false,
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    result.current.graphPanelProps.onReconnect?.();
    expect(runtime.observePlan).toHaveBeenCalledWith(PLAN_ID);
    expect(runtime.handleRunAllTasks).not.toHaveBeenCalled();
  });

  it('exposes no reconnect action without an active plan', () => {
    const runtime = makeRuntimeValue({ connectionState: 'interrupted' });
    const { result } = renderViewModel(runtime.value, { id: null, status: null });
    expect(result.current.graphPanelProps.onReconnect).toBeUndefined();
  });
});

describe('useGraphExecutionViewModel paused plan resume', () => {
  it('maps onResume to the runtime resume action for the active plan', async () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      planPaused: true,
      isExecuting: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    const props = result.current.graphPanelProps;
    expect(props.planPaused).toBe(true);
    expect(props.isExecuting).toBe(true);
    expect(props.canRun).toBe(false);
    expect(typeof props.onResume).toBe('function');

    await act(async () => {
      props.onResume?.();
    });

    expect(runtime.handleResumePlan).toHaveBeenCalledTimes(1);
    expect(runtime.handleResumePlan).toHaveBeenCalledWith(PLAN_ID);
    // Resume must never fall back to execute, approve or retry.
    expect(runtime.handleRunAllTasks).not.toHaveBeenCalled();
    expect(runtime.handleApproveAndRun).not.toHaveBeenCalled();
    expect(runtime.handleRetryFailedPlan).not.toHaveBeenCalled();
    expect(runtime.handleCancelPlan).not.toHaveBeenCalled();
  });

  it('resumes the active plan, not the plan the runtime still observes', async () => {
    const runtime = makeRuntimeValue({
      observedPlanId: OTHER_PLAN_ID,
      serverPlanStatus: 'in_progress',
      planPaused: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    // The observation of another plan must not produce a resume entry here.
    expect(result.current.graphPanelProps.planPaused).toBe(false);
    expect(result.current.planObservation.observationMismatch).toBe(true);

    await act(async () => {
      result.current.graphPanelProps.onResume?.();
    });

    expect(runtime.handleResumePlan).toHaveBeenCalledWith(PLAN_ID);
    expect(runtime.handleResumePlan).not.toHaveBeenCalledWith(OTHER_PLAN_ID);
  });

  it('exposes no resume action while the runtime has none yet', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      planPaused: true,
      withResumePlan: false,
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    expect(result.current.graphPanelProps.planPaused).toBe(true);
    expect(result.current.graphPanelProps.onResume).toBeUndefined();
  });

  it('exposes no resume action without an active plan', () => {
    const runtime = makeRuntimeValue({ planPaused: true });
    const { result } = renderViewModel(runtime.value, { id: null, status: null });
    expect(result.current.graphPanelProps.onResume).toBeUndefined();
  });
});

describe('useGraphExecutionViewModel execution sidebar gating', () => {
  it('blocks node execution while the server reports the plan running', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'in_progress',
      withSelectedTask: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'in_progress' });

    const sidebar = result.current.executionSidebarProps;
    expect(sidebar?.isExecuting).toBe(true);

    act(() => {
      void sidebar?.onExecute();
    });
    expect(runtime.handleExecuteTask).not.toHaveBeenCalled();
  });

  it('blocks node execution after an accepted start that is still pending', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'pending',
      startAccepted: true,
      withSelectedTask: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'pending' });

    const sidebar = result.current.executionSidebarProps;
    expect(sidebar?.isExecuting).toBe(true);
    act(() => {
      void sidebar?.onExecute();
    });
    expect(runtime.handleExecuteTask).not.toHaveBeenCalled();
  });

  it('keeps node execution available for a runnable plan', () => {
    const runtime = makeRuntimeValue({
      observedPlanId: PLAN_ID,
      serverPlanStatus: 'ready',
      withSelectedTask: true,
    });
    const { result } = renderViewModel(runtime.value, { status: 'ready' });

    const sidebar = result.current.executionSidebarProps;
    expect(sidebar?.isExecuting).toBe(false);
    act(() => {
      void sidebar?.onExecute();
    });
    expect(runtime.handleExecuteTask).toHaveBeenCalledTimes(1);
  });
});
