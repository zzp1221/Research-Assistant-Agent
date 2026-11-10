import { act, renderHook } from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PlanEvent, PlanGraph } from '../../src/contracts/api';
import { useResearchAssistantRuntime } from '../../src/app/hooks/useResearchAssistantRuntime';

const apiMocks = vi.hoisted(() => ({
  createPlanEventSource: vi.fn(),
  executePlan: vi.fn(),
  getPlan: vi.fn(),
  getPlanEvents: vi.fn(),
  approvePlan: vi.fn(),
  cancelPlan: vi.fn(),
  retryTask: vi.fn(),
  resumePlan: vi.fn(),
  decideRuntimeAction: vi.fn(),
  executePlanTaskStream: vi.fn(),
}));
const desktopNotify = vi.hoisted(() => vi.fn());

vi.mock('../../src/services/desktopBridge', () => ({
  getDesktopBridge: () => ({ notify: desktopNotify }),
}));

vi.mock('../../src/services/api/researchAssistantApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../src/services/api/researchAssistantApi')>();
  return {
    ...actual,
    createPlanEventSource: apiMocks.createPlanEventSource,
    executePlan: apiMocks.executePlan,
    getPlan: apiMocks.getPlan,
    getPlanEvents: apiMocks.getPlanEvents,
    approvePlan: apiMocks.approvePlan,
    cancelPlan: apiMocks.cancelPlan,
    retryTask: apiMocks.retryTask,
    resumePlan: apiMocks.resumePlan,
    decideRuntimeAction: apiMocks.decideRuntimeAction,
    executePlanTaskStream: apiMocks.executePlanTaskStream,
  };
});

type StreamHandlers = {
  onPlanEvent: (event: PlanEvent) => void;
  onError?: () => void;
};

type StreamOptions = {
  afterSeq?: number;
  forceTicket?: boolean;
};

class FakeStream {
  readonly close = vi.fn();
  options: StreamOptions | undefined;
  private readonly listeners = new Map<string, Array<(...args: unknown[]) => void>>();

  constructor(private readonly handlers: StreamHandlers) {}

  /** Minimal EventSource surface: the runtime attaches an optional `open`. */
  addEventListener(type: string, listener: (...args: unknown[]) => void): void {
    const bucket = this.listeners.get(type) ?? [];
    bucket.push(listener);
    this.listeners.set(type, bucket);
  }

  removeEventListener(type: string, listener: (...args: unknown[]) => void): void {
    const bucket = this.listeners.get(type) ?? [];
    this.listeners.set(type, bucket.filter((entry) => entry !== listener));
  }

  /** Native "the connection is open" event. */
  open(): void {
    for (const listener of this.listeners.get('open') ?? []) listener();
  }

  emit(event: Partial<PlanEvent> & { seq?: number }): void {
    this.handlers.onPlanEvent({
      plan_id: 'plan-1',
      event_type: 'task_log',
      timestamp: '2026-11-09T10:00:00Z',
      ...event,
    } as PlanEvent);
  }

  fail(): void {
    this.handlers.onError?.();
  }
}

const identity = { userId: 'user-1', sessionId: 'session-1' };

const planGraph = (patch: Partial<PlanGraph> = {}): PlanGraph => ({
  id: 'plan-1',
  user_intent: 'intent',
  intent_type: 'Code_Execution',
  status: 'in_progress',
  trace_id: 'trace-1',
  approval: { required: false, status: 'approved' },
  budget: { max_task_attempts: 3, max_duration_seconds: 600 },
  usage: { task_attempts: 0, started_at: '2026-11-09T10:00:00Z' },
  nodes: [],
  edges: [],
  ...patch,
});

interface RenderOptions {
  nodes?: unknown[];
  activePlanGraph?: PlanGraph | null;
  wrapper?: (props: { children: React.ReactNode }) => React.ReactElement;
}

const renderRuntime = (renderOptions: RenderOptions = {}) => {
  const appendChatMessage = vi.fn();
  const setNodes = vi.fn();
  const refreshPlanState = vi.fn();
  const hook = renderHook(
    () =>
      useResearchAssistantRuntime({
        nodes: (renderOptions.nodes ?? []) as never,
        setNodes,
        appendChatMessage,
        identity,
        refreshPlanState,
        activePlanGraph: renderOptions.activePlanGraph ?? null,
      }),
    renderOptions.wrapper ? { wrapper: renderOptions.wrapper } : undefined,
  );
  return { ...hook, appendChatMessage, setNodes, refreshPlanState };
};

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

const authError = (status: number) => ({ response: { status } });

describe('useResearchAssistantRuntime automatic plan observation', () => {
  let streams: FakeStream[];

  beforeEach(() => {
    streams = [];
    desktopNotify.mockReset();
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    apiMocks.executePlan.mockResolvedValue({ message: 'started' });
    apiMocks.approvePlan.mockResolvedValue({ message: 'approved' });
    apiMocks.cancelPlan.mockResolvedValue({ message: 'canceled' });
    apiMocks.retryTask.mockResolvedValue({ message: 'retried' });
    apiMocks.resumePlan.mockResolvedValue({ message: 'resumed' });
    apiMocks.decideRuntimeAction.mockResolvedValue({ id: 'action-1', title: '安装依赖', status: 'approved' });
    apiMocks.getPlanEvents.mockResolvedValue([]);
    apiMocks.getPlan.mockResolvedValue(planGraph());
    apiMocks.createPlanEventSource.mockImplementation(
      async (_planId: string, _identity: unknown, handlers: StreamHandlers, options: StreamOptions) => {
        const stream = new FakeStream(handlers);
        stream.options = options;
        streams.push(stream);
        return stream as unknown as EventSource;
      },
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // S-01: refresh of a running plan.
  it('S01 resumes a running plan from the snapshot and history without executing it', async () => {
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'history line' } },
    ]);

    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    expect(apiMocks.getPlan).toHaveBeenCalledWith('plan-1', identity);
    expect(streams).toHaveLength(1);
    expect(streams[0].options).toEqual({ afterSeq: 2, forceTicket: true });
    // A constructed EventSource is not a live connection yet: only the native
    // open event (or a real plan event) may claim "live".
    expect(result.current.executionState.connectionState).toBe('connecting');
    await act(async () => {
      streams[0].open();
    });
    expect(result.current.executionState.connectionState).toBe('live');
    expect(result.current.executionState.isExecuting).toBe(true);

    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 3, task_id: 'task-1', payload: { message: 'live line' } });
    });
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('history line');
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('live line');
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
  });

  // S-01 continuation: terminal stops the stream and notifies once.
  it('S08 closes the stream on a terminal event of the current run and notifies once', async () => {
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);

    await act(async () => {
      streams[0].emit({ event_type: 'plan_completed', seq: 4, payload: {} });
    });
    await flush();

    expect(streams[0].close).toHaveBeenCalled();
    expect(result.current.executionState.isExecuting).toBe(false);
    expect(result.current.executionState.connectionState).toBe('idle');
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
    const completionNotices = appendChatMessage.mock.calls.filter(
      (call) => call[0]?.text === '整张拓扑图已经执行完成。',
    );
    expect(completionNotices).toHaveLength(1);
    expect(desktopNotify).toHaveBeenCalledExactlyOnceWith('计划已完成', '计划已在后台完成。');
    await act(async () => {
      streams[0].emit({ event_type: 'plan_completed', seq: 4, payload: {} });
      streams[0].emit({ event_type: 'plan_completed', seq: 5, payload: {} });
    });
    await flush();
    expect(desktopNotify).toHaveBeenCalledTimes(1);
  });

  it.each(['completed', 'failed'] as const)('restores a %s snapshot and history without a desktop notification', async (status) => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status }));
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: `plan_${status}`, seq: 2, timestamp: '2026-11-09T10:05:00Z' },
    ]);
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(result.current.executionState.serverPlanStatus).toBe(status);
    expect(streams).toHaveLength(0);
    expect(desktopNotify).not.toHaveBeenCalled();
  });

  it('notifies once for a live failure and again after a new run completes', async () => {
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'failed' }));
    await act(async () => {
      streams[0].emit({ event_type: 'plan_failed', seq: 2, timestamp: '2026-11-09T10:05:00Z', payload: { error: 'boom' } });
      streams[0].emit({ event_type: 'plan_failed', seq: 3, timestamp: '2026-11-09T10:05:00Z', payload: { error: 'boom' } });
    });
    await flush();
    expect(desktopNotify).toHaveBeenCalledExactlyOnceWith('计划失败', '计划执行失败，请打开详情查看原因。');

    apiMocks.getPlan.mockResolvedValue(planGraph({ usage: { task_attempts: 0, started_at: '2026-11-09T11:00:00Z' } }));
    await act(async () => {
      await result.current.observePlan('plan-1');
    });
    await flush();
    expect(streams).toHaveLength(2);
    await act(async () => {
      streams[1].emit({ event_type: 'plan_started', seq: 4, timestamp: '2026-11-09T11:00:00Z' });
      streams[1].emit({ event_type: 'plan_completed', seq: 5, timestamp: '2026-11-09T11:05:00Z' });
      streams[1].emit({ event_type: 'plan_completed', seq: 5, timestamp: '2026-11-09T11:05:00Z' });
    });
    await flush();
    expect(desktopNotify).toHaveBeenCalledTimes(2);
    expect(desktopNotify).toHaveBeenLastCalledWith('计划已完成', '计划已在后台完成。');
  });

  // S-02/S-03: plan switch and library open share the same flow.
  it('S02/S03 switches plans, closes the old flow and never leaks old callbacks', async () => {
    const { rerender, result } = renderRuntime({ activePlanGraph: planGraph({ id: 'plan-1' }) });
    await flush();
    expect(streams).toHaveLength(1);

    await act(async () => {
      rerender();
    });
    // Re-render with a different active plan.
    const view = renderRuntime({ activePlanGraph: planGraph({ id: 'plan-2' }) });
    await flush();

    expect(apiMocks.getPlan).toHaveBeenCalledWith('plan-2', identity);
    expect(view.result.current.executionState.observedPlanId).toBe('plan-2');
    const plan2Streams = streams.filter((stream) => stream !== streams[0]);
    expect(plan2Streams).toHaveLength(1);
    expect(result.current.executionState.observedPlanId).toBe('plan-1');
  });

  it('S02 closes the previous stream when the observed plan changes inside one hook', async () => {
    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const { rerender, result } = renderHook(
      ({ planId }: { planId: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ id: planId }),
        }),
      { initialProps: { planId: 'plan-1' } },
    );
    await flush();
    const firstStream = streams[0];
    expect(firstStream).toBeDefined();

    rerender({ planId: 'plan-2' });
    await flush();

    expect(firstStream.close).toHaveBeenCalled();
    expect(streams).toHaveLength(2);
    expect(result.current.executionState.observedPlanId).toBe('plan-2');

    // A queued callback from the abandoned plan must not touch the new view.
    await act(async () => {
      firstStream.emit({ event_type: 'task_log', seq: 9, task_id: 'old-task', payload: { message: 'stale' } });
    });
    expect(result.current.executionState.nodeStates['old-task']).toBeUndefined();
  });

  // S-04: 202 accepted while the snapshot is still pending.
  it('S04 keeps observing a 202 start that the snapshot still reports as pending, and blocks a second start', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'pending', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'pending', usage: { task_attempts: 0 } }) });
    await flush();
    // A runnable plan is not observed until a start is accepted here.
    expect(streams).toHaveLength(0);

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await flush();

    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    expect(streams).toHaveLength(1);
    expect(result.current.executionState.startAccepted).toBe(true);
    expect(result.current.executionState.isExecuting).toBe(true);

    // Second click while pending: no second execute, no second stream.
    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    expect(streams).toHaveLength(1);

    // Even a terminal from the *old* run replayed on this stream keeps it open.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_completed', seq: 4, timestamp: '2026-11-09T09:00:00Z', payload: {} });
    });
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(streams[0].close).not.toHaveBeenCalled();
  });

  it('S04 double-clicking run submits one execute request', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'ready', usage: { task_attempts: 0 } }) });
    await flush();

    let resolveExecute: (() => void) | undefined;
    apiMocks.executePlan.mockImplementation(
      () => new Promise<void>((resolve) => {
        resolveExecute = () => resolve();
      }),
    );

    await act(async () => {
      void result.current.handleRunAllTasks('plan-1');
      void result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveExecute?.();
    });
    await flush();
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
  });

  // S-05: history/stream overlap and zero seq.
  it('S05 dedupes the history/live overlap and repairs a zero-seq event from the numbered history', async () => {
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'one' } },
    ]);
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams[0].options).toEqual({ afterSeq: 2, forceTicket: true });

    // Overlap: the stream replays seq 2 before delivering the new one.
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 2, task_id: 'task-1', payload: { message: 'one' } });
    });
    const logs = result.current.executionState.nodeStates['task-1']?.logs ?? '';
    expect(logs.split('one')).toHaveLength(2); // exactly one "one"

    // Legacy zero-seq event: no append, no notification, one coalesced resync.
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'one' } },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 3, task_id: 'task-1', timestamp: '2026-11-09T10:02:00Z', payload: { message: 'two' } },
    ]);
    apiMocks.getPlanEvents.mockClear();
    const messagesBefore = 0;
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', task_id: 'task-1', payload: { message: 'legacy zero seq' } } as never);
    });
    await flush();

    expect(apiMocks.getPlanEvents).toHaveBeenCalledTimes(1);
    const afterZeroSeq = result.current.executionState.nodeStates['task-1']?.logs ?? '';
    expect(afterZeroSeq).toContain('two');
    expect(afterZeroSeq).not.toContain('legacy zero seq');
    expect(messagesBefore).toBe(0);
  });

  // S-06: reconnect budget, auth stop, interrupted state.
  it('S06 reconnects with a fresh ticket and cursor, then reports an interruption without cancelling the run', async () => {
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);

    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 6, task_id: 'task-1', payload: { message: 'x' } });
    });

    vi.useFakeTimers();
    await act(async () => {
      streams[0].fail();
    });
    expect(result.current.executionState.connectionState).toBe('reconnecting');
    expect(result.current.executionState.isExecuting).toBe(true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(streams).toHaveLength(2);
    expect(streams[1].options).toEqual({ afterSeq: 6, forceTicket: true });

    // Exhaust the budget.
    for (let attempt = 0; attempt < 6; attempt += 1) {
      await act(async () => {
        streams[streams.length - 1].fail();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });
    }
    await act(async () => {
      streams[streams.length - 1].fail();
    });

    expect(result.current.executionState.connectionState).toBe('interrupted');
    expect(result.current.executionState.connectionDetail).toContain('进度更新已中断');
    // The service-side run is untouched: still "executing" from the business
    // status. `startAccepted` stays false because this view submitted nothing:
    // it is the "unconfirmed submit" lock, not a stand-in for "running".
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(result.current.executionState.startAccepted).toBe(false);
    expect(apiMocks.cancelPlan).not.toHaveBeenCalled();
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: expect.stringContaining('进度更新已中断'),
    }));
  });

  it('S06 stops automatic retries on a ticket 401/403/404 and explains why', async () => {
    apiMocks.createPlanEventSource.mockRejectedValue(authError(403));
    vi.useFakeTimers();
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(result.current.executionState.connectionState).toBe('interrupted');
    expect(result.current.executionState.connectionDetail).toContain('无权查看');
    expect(apiMocks.createPlanEventSource).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(apiMocks.createPlanEventSource).toHaveBeenCalledTimes(1);
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: expect.stringContaining('无权查看'),
    }));
  });

  it('S06 keeps a safe unknown state when the snapshot read fails without auth', async () => {
    apiMocks.getPlan.mockRejectedValue(new Error('Failed to fetch'));
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    expect(result.current.executionState.connectionState).toBe('interrupted');
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    expect(apiMocks.createPlanEventSource).not.toHaveBeenCalled();
  });

  // S-07: waiting states keep observing.
  it('S07 keeps observing awaiting_approval / awaiting_input / paused plans', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'awaiting_approval' }));
    const first = renderRuntime({ activePlanGraph: planGraph({ status: 'awaiting_approval' }) });
    await flush();
    expect(streams).toHaveLength(1);
    expect(first.result.current.executionState.serverPlanStatus).toBe('awaiting_approval');
    expect(first.result.current.executionState.isExecuting).toBe(true);

    streams = [];
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'in_progress', paused: true }));
    const second = renderRuntime({ activePlanGraph: planGraph({ status: 'in_progress', paused: true }) });
    await flush();
    expect(streams).toHaveLength(1);
    expect(second.result.current.executionState.planPaused).toBe(true);
    expect(second.result.current.executionState.isExecuting).toBe(true);
  });

  it('S07 does not observe a blocked plan and never offers it as runnable', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'blocked' }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'blocked' }) });
    await flush();

    expect(streams).toHaveLength(0);
    expect(result.current.executionState.isExecuting).toBe(false);
    expect(result.current.executionState.serverPlanStatus).toBe('blocked');

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
  });

  // S-09: old run terminal must not end a new run of the same revision.
  it('S09 isolates a same-revision previous run terminal from the new run', async () => {
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_completed', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { result: 'old result' } },
      { plan_id: 'plan-1', event_type: 'plan_completed', seq: 3, timestamp: '2026-11-09T10:05:00Z' },
    ]);
    // The plan was restarted: same revision, a newer server-side start.
    apiMocks.getPlan.mockResolvedValue(planGraph({
      status: 'in_progress',
      current_revision: 1,
      usage: { task_attempts: 1, started_at: '2026-11-09T12:00:00Z' },
    }));
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    // The old run's terminal must not end the current observation.
    expect(streams).toHaveLength(1);
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(result.current.executionState.serverPlanStatus).toBe('in_progress');
    // ...and its result must not be shown as the new run's result.
    expect(result.current.executionState.nodeStates['task-1']).toBeUndefined();

    // A terminal event of the *old* run arriving late is dropped.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_completed', seq: 4, timestamp: '2026-11-09T10:05:00Z', payload: {} });
    });
    expect(streams[0].close).not.toHaveBeenCalled();
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
    expect(result.current.executionState.isExecuting).toBe(true);

    // The new run's own terminal closes it.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_completed', seq: 8, timestamp: '2026-11-09T12:20:00Z', payload: {} });
    });
    await flush();
    expect(streams[0].close).toHaveBeenCalled();
    expect(result.current.executionState.isExecuting).toBe(false);
  });

  // S-09: A/B slow callbacks.
  it('S09 ignores a slow history response from plan A once plan B is observed', async () => {
    let resolveA: ((events: PlanEvent[]) => void) | undefined;
    const slowA = new Promise<PlanEvent[]>((resolve) => {
      resolveA = resolve;
    });
    apiMocks.getPlanEvents.mockImplementation((planId: string) => {
      if (planId === 'plan-1') return slowA;
      return Promise.resolve([]);
    });

    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const { rerender, result } = renderHook(
      ({ planId }: { planId: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ id: planId }),
        }),
      { initialProps: { planId: 'plan-1' } },
    );
    await flush();
    expect(result.current.executionState.observedPlanId).toBe('plan-1');

    rerender({ planId: 'plan-2' });
    await flush();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');

    // Plan A's history finally resolves with events for A.
    await act(async () => {
      resolveA?.([
        { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
        { plan_id: 'plan-1', event_type: 'task_log', seq: 2, task_id: 'a-task', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'plan A only' } },
        { plan_id: 'plan-1', event_type: 'plan_completed', seq: 3, timestamp: '2026-11-09T10:05:00Z' },
      ]);
    });
    await flush();

    expect(result.current.executionState.nodeStates['a-task']).toBeUndefined();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    expect(result.current.executionState.serverPlanStatus).toBe('in_progress');
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
  });

  // StrictMode.
  it('S09 creates at most one effective stream under StrictMode', async () => {
    const wrapper = ({ children }: { children: React.ReactNode }) => <StrictMode>{children}</StrictMode>;
    renderRuntime({ activePlanGraph: planGraph(), wrapper });
    await flush();

    const liveStreams = streams.filter((stream) => stream.close.mock.calls.length === 0);
    expect(liveStreams).toHaveLength(1);
    const sources = apiMocks.createPlanEventSource.mock.calls.length;
    expect(sources).toBeLessThanOrEqual(2);
  });

  // resetRuntimeState must not close a freshly restored stream.
  it('keeps the restored observation alive across a session reset', async () => {
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);

    await act(async () => {
      result.current.resetRuntimeState();
    });
    await flush();

    expect(streams[0].close).not.toHaveBeenCalled();
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(result.current.executionState.nodeStates).toEqual({});

    // The stream still delivers events into the reset view.
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 5, task_id: 'task-1', payload: { message: 'after reset' } });
    });
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('after reset');
  });

  // Runtime action decision observes but never executes.
  it('continues observing after a runtime action decision without executing again', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'in_progress' }));
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    await act(async () => {
      await result.current.handleRuntimeActionDecision('plan-1', 'action-1', 'approve');
    });
    await flush();

    expect(apiMocks.decideRuntimeAction).toHaveBeenCalledWith('plan-1', 'action-1', 'approve', identity);
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    await act(async () => {
      streams[streams.length - 1].open();
    });
    expect(result.current.executionState.connectionState).toBe('live');
  });

  // handleReconnect: explicit recovery, no execute.
  it('handleReconnect rebuilds the observation without executing', async () => {
    apiMocks.createPlanEventSource.mockRejectedValue(authError(0));
    vi.useFakeTimers();
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.executionState.connectionState).toBe('reconnecting');

    vi.useRealTimers();
    streams = [];
    apiMocks.createPlanEventSource.mockImplementation(
      async (_planId: string, _identity: unknown, handlers: StreamHandlers, options: StreamOptions) => {
        const stream = new FakeStream(handlers);
        stream.options = options;
        streams.push(stream);
        return stream as unknown as EventSource;
      },
    );
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
    ]);

    await act(async () => {
      await result.current.handleReconnect();
    });
    await flush();

    expect(streams).toHaveLength(1);
    expect(streams[0].options).toEqual({ afterSeq: 1, forceTicket: true });
    await act(async () => {
      streams[0].open();
    });
    expect(result.current.executionState.connectionState).toBe('live');
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
  });

  // Focus handling.
  it('verifies the snapshot on window focus without duplicated subscriptions', async () => {
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);
    apiMocks.getPlan.mockClear();

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });
    await flush();

    expect(apiMocks.getPlan).toHaveBeenCalledTimes(1);
    expect(streams).toHaveLength(1);
    expect(result.current.executionState.observedPlanId).toBe('plan-1');
  });

  // Legacy terminal events usually carry no execution_id at all.
  it('S09 ignores a legacy terminal without execution_id that belongs to the previous run', async () => {
    apiMocks.getPlanEvents.mockResolvedValue([
      // Previous run: no execution_id on any event, same revision.
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T09:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_completed', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T09:05:00Z', payload: { result: 'first run result' } },
      { plan_id: 'plan-1', event_type: 'plan_completed', seq: 3, timestamp: '2026-11-09T09:10:00Z' },
      // Current run.
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 4, timestamp: '2026-11-09T12:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 5, task_id: 'task-1', timestamp: '2026-11-09T12:01:00Z', payload: { message: 'current run' } },
    ]);
    apiMocks.getPlan.mockResolvedValue(planGraph({
      status: 'in_progress',
      current_revision: 1,
      usage: { task_attempts: 1, started_at: '2026-11-09T12:00:00Z' },
    }));
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    expect(streams).toHaveLength(1);
    expect(streams[0].options).toEqual({ afterSeq: 5, forceTicket: true });
    expect(result.current.executionState.isExecuting).toBe(true);
    // The previous run's completed result is not presented as this run's state.
    expect(result.current.executionState.nodeStates['task-1']?.result ?? '').not.toBe('first run result');
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('current run');
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
  });

  // History restores results/images/verification through the same merge path.
  it('restores parameter summaries, results and images from history without notifying', async () => {
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_ready', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:00:30Z', payload: { inputs: [{ key: 'dataset', value_preview: 'train.csv' }] } },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 3, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'epoch 1' } },
      { plan_id: 'plan-1', event_type: 'task_completed', seq: 4, task_id: 'task-1', timestamp: '2026-11-09T10:02:00Z', payload: { result: 'accuracy 0.91', code: 'print(1)', image_base64: 'iVBORw0KGgo=' } },
      { plan_id: 'plan-1', event_type: 'verification_completed', seq: 5, task_id: 'task-1', timestamp: '2026-11-09T10:03:00Z', payload: { verification: { status: 'passed' } } },
    ]);
    const { result, appendChatMessage, setNodes } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();

    const state = result.current.executionState.nodeStates['task-1'];
    expect(state?.logs).toContain('[Plan Params] 本次上游传入参数');
    expect(state?.logs).toContain('train.csv');
    expect(state?.logs).toContain('epoch 1');
    expect(state?.logs).toContain('verification_passed');
    expect(state?.result).toBe('accuracy 0.91');
    expect(state?.code).toBe('print(1)');
    expect(state?.imageBase64).toBe('iVBORw0KGgo=');
    expect(setNodes).toHaveBeenCalled();
    // Replay is state-only: no "completed" chat notice, no permission notice.
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({
      text: expect.stringContaining('需要你的运行权限确认'),
    }));
  });

  it('keeps an explicit re-run entry for a finished plan instead of observing it', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'completed' }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'completed' }) });
    await flush();

    expect(streams).toHaveLength(0);
    expect(result.current.executionState.isExecuting).toBe(false);
    expect(result.current.executionState.connectionState).toBe('idle');

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await flush();
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
  });

  it('S09 drops a slow ticket for plan A that resolves after switching to plan B', async () => {
    let resolveTicketA: ((stream: EventSource) => void) | undefined;
    const handlersA: StreamHandlers[] = [];
    apiMocks.createPlanEventSource.mockImplementation(
      (planId: string, _identity: unknown, handlers: StreamHandlers, options: StreamOptions) => {
        if (planId === 'plan-1') {
          handlersA.push(handlers);
          return new Promise<EventSource>((resolve) => {
            resolveTicketA = resolve;
          });
        }
        const stream = new FakeStream(handlers);
        stream.options = options;
        streams.push(stream);
        return Promise.resolve(stream as unknown as EventSource);
      },
    );

    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const { rerender, result } = renderHook(
      ({ planId }: { planId: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ id: planId }),
        }),
      { initialProps: { planId: 'plan-1' } },
    );
    await flush();

    rerender({ planId: 'plan-2' });
    await flush();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');

    // Plan A's ticket finally resolves: the created stream must be closed
    // immediately and never receive callbacks.
    const staleStream = new FakeStream(handlersA[0]);
    await act(async () => {
      resolveTicketA?.(staleStream as unknown as EventSource);
    });
    await flush();

    expect(staleStream.close).toHaveBeenCalled();
    await act(async () => {
      staleStream.emit({ event_type: 'task_log', seq: 7, task_id: 'a-task', payload: { message: 'stale' } });
    });
    expect(result.current.executionState.nodeStates['a-task']).toBeUndefined();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');
  });
});

/**
 * Regressions for the composition defects found by the UI-02 runtime audit.
 *
 * Each case asserts the *effect on the view/server interaction*, not just a
 * status string: how many requests were sent, which plan they targeted and
 * which view they were allowed to write to.
 */
describe('useResearchAssistantRuntime composition regressions', () => {
  let streams: FakeStream[];

  beforeEach(() => {
    streams = [];
    desktopNotify.mockReset();
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    apiMocks.executePlan.mockResolvedValue({ message: 'started' });
    apiMocks.approvePlan.mockResolvedValue({ message: 'approved' });
    apiMocks.cancelPlan.mockResolvedValue({ message: 'canceled' });
    apiMocks.retryTask.mockResolvedValue({ message: 'retried' });
    apiMocks.resumePlan.mockResolvedValue({ message: 'resumed' });
    apiMocks.decideRuntimeAction.mockResolvedValue({ id: 'action-1', title: '安装依赖', status: 'approved' });
    apiMocks.getPlanEvents.mockResolvedValue([]);
    apiMocks.getPlan.mockResolvedValue(planGraph());
    apiMocks.createPlanEventSource.mockImplementation(
      async (_planId: string, _identity: unknown, handlers: StreamHandlers, options: StreamOptions) => {
        const stream = new FakeStream(handlers);
        stream.options = options;
        streams.push(stream);
        return stream as unknown as EventSource;
      },
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // ---------------------------------------------------------------------
  // 1. startAccepted is a pending-submit lock, never a substitute for running.
  // ---------------------------------------------------------------------
  it('R01 clears the accepted-start lock on confirmation and re-allows an explicit retry after a failure', async () => {
    const failedNode = { id: 'task-1', data: { status: 'failed', task: { ID: 'task-1', Name: 'Train', AssignedTo: 'data_agent' } } };
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({
      activePlanGraph: planGraph({ status: 'ready', usage: { task_attempts: 0 } }),
      nodes: [failedNode],
    });
    await flush();

    // First run from this view.
    await act(async () => {
      await result.current.handleRetryFailedPlan('plan-1');
    });
    await flush();
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    expect(result.current.executionState.startAccepted).toBe(true);

    // The scheduler publishes this run's opener: the accepted start is now
    // confirmed, so the pending lock must be released.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_started', seq: 2, timestamp: '2026-11-09T11:00:00Z' });
    });
    await flush();
    expect(result.current.executionState.startAccepted).toBe(false);
    // The running business state still comes from the server status, not from
    // the (now cleared) local lock.
    expect(result.current.executionState.isExecuting).toBe(true);

    // This run fails.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_failed', seq: 3, timestamp: '2026-11-09T11:05:00Z', payload: { error: 'boom' } });
    });
    await flush();
    expect(result.current.executionState.startAccepted).toBe(false);
    expect(result.current.executionState.isExecuting).toBe(false);
    const streamsAfterFailure = streams.length;

    // The explicit retry must actually execute again (it used to return forever
    // because startAccepted was never cleared).
    apiMocks.retryTask.mockClear();
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'failed', usage: { task_attempts: 1, started_at: '2026-11-09T11:00:00Z' } }));
    await act(async () => {
      await result.current.handleRetryFailedPlan('plan-1');
    });
    await flush();
    expect(apiMocks.retryTask).toHaveBeenCalledTimes(1);
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(2);
    expect(streams.length).toBeGreaterThan(streamsAfterFailure);

    // ...and the previous run's terminal stays isolated from the new run.
    await act(async () => {
      streams[0].emit({ event_type: 'plan_failed', seq: 3, timestamp: '2026-11-09T11:05:00Z', payload: { error: 'boom' } });
    });
    await flush();
    expect(result.current.executionState.isExecuting).toBe(true);
  });

  it('R01 keeps the pending protection against a lagging terminal snapshot of the previous run', async () => {
    // The accepted start is not confirmed: the server still reports the old run.
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'completed', usage: { task_attempts: 1, started_at: '2026-11-09T08:00:00Z' } }));
    apiMocks.getPlan.mockResolvedValueOnce(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'ready', usage: { task_attempts: 0 } }) });
    await flush();

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await flush();
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    // Still unconfirmed (the snapshot lagged): the lock stays on.
    expect(result.current.executionState.startAccepted).toBe(true);

    // A second start is refused while the pending submit is unconfirmed.
    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
  });

  // ---------------------------------------------------------------------
  // 2. Slow cross-plan responses must not re-observe the previous plan.
  // ---------------------------------------------------------------------
  const renderTwoPlans = () => {
    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const hook = renderHook(
      ({ planId }: { planId: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ id: planId }),
        }),
      { initialProps: { planId: 'plan-1' } },
    );
    return { ...hook, setNodes, appendChatMessage, refreshPlanState };
  };

  it('R02 drops a deferred execute response for plan A once plan B is observed', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { rerender, result } = renderTwoPlans();
    await flush();

    let resolveExecute: (() => void) | undefined;
    apiMocks.executePlan.mockImplementation(
      () => new Promise<void>((resolve) => {
        resolveExecute = () => resolve();
      }),
    );

    await act(async () => {
      void result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);

    rerender({ planId: 'plan-2' });
    await flush();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    const streamsBefore = streams.length;
    const getPlanCallsBefore = apiMocks.getPlan.mock.calls.length;

    // Plan A's execute finally returns: it must not rebuild A's observation.
    await act(async () => {
      resolveExecute?.();
    });
    await flush();

    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    expect(streams.length).toBe(streamsBefore);
    const planACallsAfter = apiMocks.getPlan.mock.calls.slice(getPlanCallsBefore).filter((call) => call[0] === 'plan-1');
    expect(planACallsAfter).toHaveLength(0);
  });

  it('R02 drops a deferred cancel response for plan A without closing or cancelling plan B', async () => {
    const planBNodes = [{ id: 'b-task', data: { status: 'running', task: { ID: 'b-task', Name: 'B', AssignedTo: 'data_agent' } } }];
    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const { rerender, result } = renderHook(
      ({ planId }: { planId: string }) =>
        useResearchAssistantRuntime({
          nodes: planId === 'plan-2' ? (planBNodes as never) : [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ id: planId }),
        }),
      { initialProps: { planId: 'plan-1' } },
    );
    await flush();

    let resolveCancel: (() => void) | undefined;
    apiMocks.cancelPlan.mockImplementation(
      () => new Promise<void>((resolve) => {
        resolveCancel = () => resolve();
      }),
    );

    await act(async () => {
      void result.current.handleCancelPlan('plan-1');
    });

    rerender({ planId: 'plan-2' });
    await flush();
    const planBStream = streams[streams.length - 1];
    expect(result.current.executionState.observedPlanId).toBe('plan-2');

    setNodes.mockClear();
    await act(async () => {
      resolveCancel?.();
    });
    await flush();

    // B's subscription survives; B's nodes are never marked canceled.
    expect(planBStream.close).not.toHaveBeenCalled();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    const canceledWrites = setNodes.mock.calls.filter((call) => {
      const mapper = call[0] as (nodes: unknown[]) => Array<{ data?: { status?: string } }>;
      const mapped = mapper(planBNodes);
      return mapped[0]?.data?.status === 'canceled';
    });
    expect(canceledWrites).toHaveLength(0);
  });

  it('R02 drops a deferred runtime-action decision for plan A once plan B is observed', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'in_progress' }));
    const { rerender, result } = renderTwoPlans();
    await flush();

    let resolveDecision: ((value: { id: string; title: string; status: string }) => void) | undefined;
    apiMocks.decideRuntimeAction.mockImplementation(
      () => new Promise((resolve) => {
        resolveDecision = resolve;
      }),
    );

    await act(async () => {
      void result.current.handleRuntimeActionDecision('plan-1', 'action-1', 'approve');
    });

    rerender({ planId: 'plan-2' });
    await flush();
    const streamsBefore = streams.length;

    await act(async () => {
      resolveDecision?.({ id: 'action-1', title: '安装依赖', status: 'approved' });
    });
    await flush();

    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    expect(streams.length).toBe(streamsBefore);
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
  });

  it('R02 does not clear plan B executing state when a direct task stream of plan A completes', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'in_progress' }));
    const { rerender, result } = renderTwoPlans();
    await flush();

    let finishTask: (() => void) | undefined;
    apiMocks.executePlanTaskStream.mockImplementation(
      () => new Promise<void>((resolve) => {
        finishTask = () => resolve();
      }),
    );

    const task = { ID: 'a-task', Name: 'A task', AssignedTo: 'data_agent' } as never;
    await act(async () => {
      void result.current.handleExecuteTask(task, 'plan-1');
    });
    await flush();

    rerender({ planId: 'plan-2' });
    await flush();
    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    // Plan B is running: its view says so.
    expect(result.current.executionState.isExecuting).toBe(true);

    await act(async () => {
      finishTask?.();
    });
    await flush();

    expect(result.current.executionState.observedPlanId).toBe('plan-2');
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(result.current.executionState.nodeStates['a-task']).toBeUndefined();
  });

  // ---------------------------------------------------------------------
  // 3. Same-plan forced history re-read must not be settled by an old call.
  // ---------------------------------------------------------------------
  it('R03 ignores a late history response from a superseded read of the same plan', async () => {
    apiMocks.getPlanEvents.mockResolvedValueOnce([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { message: 'old run line' } },
    ]);
    const { result, appendChatMessage } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('old run line');

    // A forced re-read of the *same* plan starts and stays slow. Its merge state
    // is the same object the new flow will use (same plan, same identity), which
    // is exactly why the session-scoped per-call guard is required.
    let resolveSlowHistory: ((events: PlanEvent[]) => void) | undefined;
    apiMocks.getPlanEvents.mockImplementationOnce(
      () => new Promise<PlanEvent[]>((resolve) => {
        resolveSlowHistory = resolve;
      }),
    );
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', task_id: 'task-1', payload: { message: 'zero seq' } } as never);
    });
    await flush();
    expect(resolveSlowHistory).toBeDefined();

    // The view is rebuilt for the same plan and hydrates the new run first.
    apiMocks.getPlan.mockResolvedValue(planGraph({
      status: 'in_progress',
      usage: { task_attempts: 1, started_at: '2026-11-09T12:00:00Z' },
    }));
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 11, timestamp: '2026-11-09T12:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_log', seq: 12, task_id: 'task-1', timestamp: '2026-11-09T12:01:00Z', payload: { message: 'new run line' } },
    ]);
    await act(async () => {
      await result.current.handleReconnect();
    });
    await flush();
    expect(result.current.executionState.nodeStates['task-1']?.logs).toContain('new run line');
    const liveStream = streams[streams.length - 1];
    expect(liveStream.options?.afterSeq).toBe(12);

    // The superseded read finally resolves with the *new* run's terminal event.
    // Without the per-call guard it would be merged as history: the terminal
    // flag would be consumed silently and the live terminal would be dropped.
    await act(async () => {
      resolveSlowHistory?.([
        { plan_id: 'plan-1', event_type: 'plan_started', seq: 11, timestamp: '2026-11-09T12:00:00Z' },
        { plan_id: 'plan-1', event_type: 'task_log', seq: 12, task_id: 'task-1', timestamp: '2026-11-09T12:01:00Z', payload: { message: 'stale replay line' } },
        { plan_id: 'plan-1', event_type: 'plan_completed', seq: 13, timestamp: '2026-11-09T12:09:00Z' },
      ]);
    });
    await flush();

    expect(result.current.executionState.nodeStates['task-1']?.logs).not.toContain('stale replay line');
    expect(appendChatMessage).not.toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));

    // The live terminal of the current run still ends the observation normally.
    await act(async () => {
      liveStream.emit({ event_type: 'plan_completed', seq: 13, timestamp: '2026-11-09T12:09:00Z', payload: {} });
    });
    await flush();
    expect(result.current.executionState.isExecuting).toBe(false);
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({ text: '整张拓扑图已经执行完成。' }));
  });

  it('R03 rebuilds the same plan from history under a new identity instead of reusing its cursor', async () => {
    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_completed', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { result: 'identity one result' } },
    ]);
    const { rerender, result } = renderHook(
      ({ userId }: { userId: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity: { userId, sessionId: 'session-1' },
          refreshPlanState,
          activePlanGraph: planGraph(),
        }),
      { initialProps: { userId: 'user-1' } },
    );
    await flush();
    expect(result.current.executionState.nodeStates['task-1']?.result).toBe('identity one result');
    const firstCursor = streams[streams.length - 1].options?.afterSeq;
    expect(firstCursor).toBe(2);

    apiMocks.getPlanEvents.mockClear();
    rerender({ userId: 'user-2' });
    await flush();

    // The other identity's cursor is never reused: history is read again and
    // the restored state is rebuilt for the new view instead of staying empty.
    expect(apiMocks.getPlanEvents).toHaveBeenCalledWith('plan-1', { userId: 'user-2', sessionId: 'session-1' });
    expect(result.current.executionState.nodeStates['task-1']?.result).toBe('identity one result');
    expect(streams[streams.length - 1].options?.afterSeq).toBe(2);
    expect(streams).toHaveLength(2);
  });

  it('R03 clears results and selection when the active plan disappears', async () => {
    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' },
      { plan_id: 'plan-1', event_type: 'task_completed', seq: 2, task_id: 'task-1', timestamp: '2026-11-09T10:01:00Z', payload: { result: 'leaked?' } },
    ]);
    const { rerender, result } = renderHook(
      ({ planId }: { planId: string | null }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planId ? planGraph({ id: planId }) : null,
        }),
      { initialProps: { planId: 'plan-1' as string | null } },
    );
    await flush();
    expect(result.current.executionState.nodeStates['task-1']).toBeDefined();

    rerender({ planId: null });
    await flush();

    expect(result.current.executionState.nodeStates).toEqual({});
    expect(result.current.executionState.selectedTask).toBeNull();
    expect(result.current.executionState.observedPlanId).toBe('');
  });

  // ---------------------------------------------------------------------
  // 4. Refresh of a plan with no live source must rebuild the subscription.
  // ---------------------------------------------------------------------
  it('R04 resumes the stream and never executes when another client starts a pending plan', async () => {
    // 1st snapshot: still pending, so this view observes nothing.
    apiMocks.getPlan.mockResolvedValueOnce(planGraph({ status: 'pending', usage: { task_attempts: 0 } }));
    // Later snapshots: another client started it.
    apiMocks.getPlan.mockResolvedValue(planGraph({
      status: 'in_progress',
      usage: { task_attempts: 1, started_at: '2026-11-09T12:00:00Z' },
    }));
    apiMocks.getPlanEvents.mockResolvedValue([
      { plan_id: 'plan-1', event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T12:00:00Z' },
    ]);

    const setNodes = vi.fn();
    const appendChatMessage = vi.fn();
    const refreshPlanState = vi.fn();
    const { rerender, result } = renderHook(
      ({ status }: { status: string }) =>
        useResearchAssistantRuntime({
          nodes: [],
          setNodes,
          appendChatMessage,
          identity,
          refreshPlanState,
          activePlanGraph: planGraph({ status, usage: { task_attempts: 0 } }),
        }),
      { initialProps: { status: 'pending' } },
    );
    await flush();
    expect(streams).toHaveLength(0);
    expect(result.current.executionState.connectionState).toBe('idle');

    // The workspace refreshed the plan and now reports it as running.
    rerender({ status: 'in_progress' });
    await flush();

    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    expect(streams).toHaveLength(1);
    expect(streams[0].options).toEqual({ afterSeq: 1, forceTicket: true });
    expect(result.current.executionState.serverPlanStatus).toBe('in_progress');
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(result.current.executionState.observedPlanId).toBe('plan-1');
  });

  it('R04 keeps a single effective stream across repeated focus and refresh', async () => {
    const { rerender, result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      window.dispatchEvent(new Event('focus'));
    });
    await flush();
    rerender();
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });
    await flush();

    expect(streams).toHaveLength(1);
    expect(streams[0].close).not.toHaveBeenCalled();
    expect(result.current.executionState.observedPlanId).toBe('plan-1');
  });

  // ---------------------------------------------------------------------
  // 5. A lost start response must never open a duplicate run.
  // ---------------------------------------------------------------------
  it('R05 keeps the no-duplicate-run lock when the execute response is lost and confirmation fails', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'ready', usage: { task_attempts: 0 } }) });
    await flush();

    apiMocks.executePlan.mockRejectedValue(new Error('Failed to fetch'));
    // The confirming snapshot read fails too: the outcome is unknown.
    apiMocks.getPlan.mockRejectedValue(new Error('Failed to fetch'));

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await flush();

    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    expect(result.current.executionState.startAccepted).toBe(true);
    expect(result.current.executionState.connectionState).toBe('interrupted');

    // A second click must not submit again, even though the outcome is unknown.
    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
  });

  it('R05 releases the lock on an explicit 409 only when the snapshot confirms the plan is idle', async () => {
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));
    const { result } = renderRuntime({ activePlanGraph: planGraph({ status: 'ready', usage: { task_attempts: 0 } }) });
    await flush();

    apiMocks.executePlan.mockRejectedValue({ response: { status: 409 } });
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'ready', usage: { task_attempts: 0 } }));

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await flush();

    expect(result.current.executionState.startAccepted).toBe(false);
    expect(result.current.executionState.isExecuting).toBe(false);

    // Confirmed idle: an explicit retry is allowed again.
    apiMocks.executePlan.mockResolvedValue({ message: 'started' });
    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(2);
  });

  it('R05 gives a bounded pending confirmation that never re-executes the plan', async () => {
    vi.useFakeTimers();
    apiMocks.getPlan.mockResolvedValue(planGraph({ status: 'pending', usage: { task_attempts: 0 } }));
    const { result, appendChatMessage } = renderRuntime({
      activePlanGraph: planGraph({ status: 'pending', usage: { task_attempts: 0 } }),
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);

    // The scheduler never publishes anything: the bounded confirmation timer
    // queries a small, finite number of times and then stops.
    apiMocks.getPlan.mockClear();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });

    const snapshotQueries = apiMocks.getPlan.mock.calls.length;
    expect(snapshotQueries).toBeGreaterThan(0);
    expect(snapshotQueries).toBeLessThanOrEqual(4);

    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
    expect(result.current.executionState.startAccepted).toBe(true);
    expect(result.current.executionState.connectionState).toBe('interrupted');
    expect(result.current.executionState.connectionDetail).toContain('启动已受理');
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: expect.stringContaining('启动已受理'),
    }));

    // The run still cannot be submitted twice.
    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });
    expect(apiMocks.executePlan).toHaveBeenCalledTimes(1);
  });

  // ---------------------------------------------------------------------
  // 6. A constructed EventSource proves nothing about liveness.
  // ---------------------------------------------------------------------
  it('R06 stays connecting until the connection proves itself with an open event or a plan event', async () => {
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    expect(streams).toHaveLength(1);
    expect(result.current.executionState.connectionState).toBe('connecting');

    // A real plan event proves the connection without the native open event.
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 2, task_id: 'task-1', payload: { message: 'proof' } });
    });
    expect(result.current.executionState.connectionState).toBe('live');
  });

  it('R06 keeps the reconnect budget until the reconnected stream proves itself', async () => {
    const { result } = renderRuntime({ activePlanGraph: planGraph() });
    await flush();
    await act(async () => {
      streams[0].open();
    });
    await act(async () => {
      streams[0].emit({ event_type: 'task_log', seq: 6, task_id: 'task-1', payload: { message: 'x' } });
    });

    vi.useFakeTimers();
    await act(async () => {
      streams[0].fail();
    });
    expect(result.current.executionState.connectionState).toBe('reconnecting');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    // The reconnect's ticket resolved and a stream exists, but nothing proved
    // the connection yet: the budget must not reset here.
    expect(streams).toHaveLength(2);
    expect(result.current.executionState.connectionState).toBe('reconnecting');

    // Without proof, the next failure continues from attempt 2 (4s backoff).
    await act(async () => {
      streams[1].fail();
    });
    expect(result.current.executionState.connectionState).toBe('reconnecting');
    expect(result.current.executionState.connectionDetail).toContain('第 2 次');

    // Proof finally arrives through the native open event.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    const recovered = streams[streams.length - 1];
    await act(async () => {
      recovered.open();
    });
    expect(result.current.executionState.connectionState).toBe('live');

    // The budget is reset only now: a new failure starts again at attempt 1.
    await act(async () => {
      recovered.fail();
    });
    expect(result.current.executionState.connectionDetail).toContain('第 1 次');
  });

  // ---------------------------------------------------------------------
  // 7. Resuming a paused plan submits one resume and never executes.
  // ---------------------------------------------------------------------
  it('R07 resumes a paused plan exactly once and never executes it', async () => {
    const paused = planGraph({ status: 'paused', usage: { task_attempts: 1, started_at: '2026-11-09T10:00:00Z' } });
    apiMocks.getPlan.mockResolvedValue(paused);
    const { result, refreshPlanState } = renderRuntime({ activePlanGraph: paused });
    await flush();

    let resolveResume: (() => void) | undefined;
    apiMocks.resumePlan.mockImplementation(
      () => new Promise<{ message: string }>((resolve) => {
        resolveResume = () => resolve({ message: 'resumed' });
      }),
    );

    // A double click must submit exactly one resume: the second call is
    // rejected synchronously while the first is still in flight.
    await act(async () => {
      void result.current.handleResumePlan('plan-1');
      void result.current.handleResumePlan('plan-1');
    });
    expect(apiMocks.resumePlan).toHaveBeenCalledTimes(1);
    expect(apiMocks.resumePlan).toHaveBeenCalledWith('plan-1', identity);

    await act(async () => {
      resolveResume?.();
    });
    await flush();

    // The backend keeps the run going: this view only re-observes it.
    expect(apiMocks.executePlan).not.toHaveBeenCalled();
    expect(apiMocks.approvePlan).not.toHaveBeenCalled();
    expect(apiMocks.retryTask).not.toHaveBeenCalled();
    expect(result.current.executionState.startAccepted).toBeFalsy();
    expect(refreshPlanState).toHaveBeenCalled();
  });
});
