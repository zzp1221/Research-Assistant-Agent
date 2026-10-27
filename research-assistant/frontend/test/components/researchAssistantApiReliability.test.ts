import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { httpClient } from '../../src/services/api/httpClient';
import {
  PLAN_CREATE_TIMEOUT_MESSAGE,
  createPlan,
  createPlanEventSource,
} from '../../src/services/api/researchAssistantApi';

const identity = { userId: 'user-1', sessionId: 'session-1' };

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  readonly url: string;
  readonly options?: EventSourceInit;
  readonly listeners = new Map<string, (event: MessageEvent) => void>();
  onerror: ((this: EventSource, ev: Event) => unknown) | null = null;
  closed = false;

  constructor(url: string, options?: EventSourceInit) {
    this.url = url;
    this.options = options;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    this.listeners.set(type, listener as (event: MessageEvent) => void);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, payload: unknown): void {
    this.listeners.get(type)?.({ data: JSON.stringify(payload) } as MessageEvent);
  }

  emitError(): void {
    this.onerror?.call(this as unknown as EventSource, {} as Event);
  }
}

const latestSource = (): FakeEventSource => {
  const source = FakeEventSource.instances.at(-1);
  if (!source) throw new Error('expected an EventSource to be created');
  return source;
};

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('createPlan', () => {
  it('sends an Idempotency-Key and the extended plan timeout', async () => {
    const post = vi.spyOn(httpClient, 'post').mockResolvedValue({ data: { message: 'ok' } } as never);

    const response = await createPlan('分析这篇论文', identity);

    expect(response).toEqual({ message: 'ok' });
    const [url, body, config] = post.mock.calls[0]!;
    expect(url).toBe('/api/plan');
    expect(body).toMatchObject({ intent: '分析这篇论文', attachments: [] });
    expect(config?.timeout).toBe(240_000);
    const headers = config?.headers as Record<string, unknown>;
    expect(typeof headers['Idempotency-Key']).toBe('string');
    expect(String(headers['Idempotency-Key'])).not.toHaveLength(0);
  });

  it('falls back to a timestamp key when crypto.randomUUID is unavailable', async () => {
    vi.stubGlobal('crypto', {});
    const post = vi.spyOn(httpClient, 'post').mockResolvedValue({ data: { message: 'ok' } } as never);

    await createPlan('分析这篇论文', identity);

    const config = post.mock.calls[0]![2];
    const headers = config?.headers as Record<string, unknown>;
    expect(String(headers['Idempotency-Key'])).toMatch(/^idem-/);
  });

  it('maps an ECONNABORTED timeout to the user-facing isolation message', async () => {
    const timeoutError = Object.assign(new Error('timeout of 180000ms exceeded'), {
      code: 'ECONNABORTED',
    });
    vi.spyOn(httpClient, 'post').mockRejectedValue(timeoutError);

    await expect(createPlan('分析这篇论文', identity)).rejects.toThrow(PLAN_CREATE_TIMEOUT_MESSAGE);
    await expect(createPlan('分析这篇论文', identity)).rejects.toThrow(
      '计划创建超时；请稍后刷新计划库确认是否已创建。',
    );
  });

  it('rethrows non-timeout errors unchanged', async () => {
    const serverError = Object.assign(new Error('internal error'), {
      isAxiosError: true,
      response: { status: 500 },
    });
    vi.spyOn(httpClient, 'post').mockRejectedValue(serverError);

    await expect(createPlan('分析这篇论文', identity)).rejects.toBe(serverError);
  });
});

describe('createPlanEventSource', () => {
  it('keeps cookie mode ticket-free when forceTicket is not set', async () => {
    const get = vi.spyOn(httpClient, 'get');
    const onPlanEvent = vi.fn();

    await createPlanEventSource('plan-1', identity, { onPlanEvent });

    expect(get).not.toHaveBeenCalled();
    const source = latestSource();
    const url = new URL(source.url);
    expect(url.pathname).toBe('/api/plans/plan-1/stream');
    expect(url.search).toBe('');
    expect(source.options?.withCredentials).toBe(true);
  });

  it('fetches a fresh ticket and forwards after_seq when reconnecting', async () => {
    const get = vi.spyOn(httpClient, 'get').mockResolvedValue({ data: { ticket: 'ticket/abc+' } } as never);

    await createPlanEventSource(
      'plan-1',
      identity,
      { onPlanEvent: vi.fn() },
      { afterSeq: 42, forceTicket: true },
    );

    expect(get).toHaveBeenCalledTimes(1);
    expect(get.mock.calls[0]![0]).toBe('/api/plans/plan-1/stream-ticket');
    const url = new URL(latestSource().url);
    expect(url.searchParams.get('ticket')).toBe('ticket/abc+');
    expect(url.searchParams.get('after_seq')).toBe('42');
  });

  it('omits after_seq for the first connection and wires both handlers', async () => {
    const onPlanEvent = vi.fn();
    const onError = vi.fn();

    await createPlanEventSource('plan-1', identity, { onPlanEvent, onError }, { afterSeq: 0 });

    const source = latestSource();
    expect(new URL(source.url).searchParams.has('after_seq')).toBe(false);
    expect(source.options?.withCredentials).toBe(true);

    source.emit('plan_event', { plan_id: 'plan-1', event_type: 'task_started', seq: 4, timestamp: '2026-10-26T00:00:00Z' });
    expect(onPlanEvent).toHaveBeenCalledTimes(1);
    expect(onPlanEvent).toHaveBeenCalledWith(expect.objectContaining({ event_type: 'task_started', seq: 4 }));

    source.emitError();
    expect(onError).toHaveBeenCalledTimes(1);
  });
});
