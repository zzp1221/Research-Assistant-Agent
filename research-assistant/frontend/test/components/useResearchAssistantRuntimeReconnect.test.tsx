import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PlanEvent } from '../../src/contracts/api';
import { useResearchAssistantRuntime } from '../../src/app/hooks/useResearchAssistantRuntime';

const apiMocks = vi.hoisted(() => ({
  createPlanEventSource: vi.fn(),
  executePlan: vi.fn(),
}));

vi.mock('../../src/services/api/researchAssistantApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../src/services/api/researchAssistantApi')>();
  return {
    ...actual,
    createPlanEventSource: apiMocks.createPlanEventSource,
    executePlan: apiMocks.executePlan,
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
  private readonly onPlanEvent: (event: PlanEvent) => void;
  private readonly onError: (() => void) | undefined;

  constructor(handlers: StreamHandlers) {
    this.onPlanEvent = handlers.onPlanEvent;
    this.onError = handlers.onError;
  }

  emit(event: Partial<PlanEvent> & { seq?: number }): void {
    this.onPlanEvent({
      plan_id: 'plan-1',
      event_type: 'task_log',
      timestamp: '2026-10-26T00:00:00Z',
      ...event,
    } as PlanEvent);
  }

  fail(): void {
    this.onError?.();
  }
}

const identity = { userId: 'user-1', sessionId: 'session-1' };

const renderRuntime = () => {
  const appendChatMessage = vi.fn();
  const setNodes = vi.fn();
  const refreshPlanState = vi.fn();
  const hook = renderHook(() => useResearchAssistantRuntime({
    nodes: [],
    setNodes,
    appendChatMessage,
    identity,
    refreshPlanState,
  }));
  return { ...hook, appendChatMessage, setNodes, refreshPlanState };
};

describe('useResearchAssistantRuntime plan stream reconnect', () => {
  let streams: FakeStream[];

  beforeEach(() => {
    streams = [];
    apiMocks.createPlanEventSource.mockReset();
    apiMocks.executePlan.mockReset();
    apiMocks.executePlan.mockResolvedValue({ message: 'started' });
    apiMocks.createPlanEventSource.mockImplementation(async (
      _planId: string,
      _identity: unknown,
      handlers: StreamHandlers,
      options: StreamOptions,
    ) => {
      const stream = new FakeStream(handlers);
      stream.options = options;
      streams.push(stream);
      return stream as unknown as EventSource;
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('backs off, reconnects with a fresh ticket and cursor, then resumes once', async () => {
    const { result, appendChatMessage } = renderRuntime();

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });

    expect(streams).toHaveLength(1);
    expect(result.current.executionState.isExecuting).toBe(true);

    // Equal seq values must be ignored, including after the first delivery.
    await act(async () => {
      streams[0].emit({ event_type: 'runtime_action_requested', seq: 5, payload: { action: { title: '安装依赖' } } });
    });
    const messagesAfterFirstEvent = appendChatMessage.mock.calls.length;
    await act(async () => {
      streams[0].emit({ event_type: 'runtime_action_requested', seq: 5, payload: { action: { title: '安装依赖' } } });
    });
    expect(appendChatMessage.mock.calls.length).toBe(messagesAfterFirstEvent);

    vi.useFakeTimers();

    await act(async () => {
      streams[0].fail();
    });
    expect(result.current.executionState.isExecuting).toBe(true);
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: expect.stringContaining('1 秒后自动重连'),
    }));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(streams).toHaveLength(2);
    expect(streams[1].options).toEqual({ afterSeq: 5, forceTicket: true });
    expect(streams[1].close).not.toHaveBeenCalled();

    await act(async () => {
      streams[1].emit({ event_type: 'task_log', seq: 6, task_id: 'task-1', payload: { message: 'resumed' } });
    });
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: '计划事件流已恢复（从序号 6 继续）',
    }));

    await act(async () => {
      streams[1].emit({ event_type: 'plan_completed', seq: 7, payload: {} });
    });
    expect(streams[1].close).toHaveBeenCalled();
    expect(result.current.executionState.isExecuting).toBe(false);
  });

  it('stops after the reconnect budget is exhausted', async () => {
    const { result, appendChatMessage } = renderRuntime();

    await act(async () => {
      await result.current.handleRunAllTasks('plan-1');
    });

    vi.useFakeTimers();

    for (let attempt = 0; attempt < 6; attempt += 1) {
      await act(async () => {
        streams[streams.length - 1].fail();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });
    }

    // Six reconnects were scheduled; the next failure exhausts the budget.
    await act(async () => {
      streams[streams.length - 1].fail();
    });

    expect(streams).toHaveLength(7);
    expect(result.current.executionState.isExecuting).toBe(false);
    expect(appendChatMessage).toHaveBeenCalledWith(expect.objectContaining({
      text: '计划事件流已中断；请刷新计划状态后重试。',
    }));
  });
});
