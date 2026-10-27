import { beforeEach, describe, expect, it, vi } from 'vitest';

const http = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('../../src/services/api/httpClient', () => ({
  httpClient: { post: http.post },
}));

import { executePlan } from '../../src/services/api/researchAssistantApi';

const identity = { userId: 'user-1', sessionId: 'session-1' };

describe('executePlan trigger', () => {
  beforeEach(() => {
    http.post.mockReset();
    http.post.mockResolvedValue({ data: { message: 'Plan execution started', plan_id: 'plan-1' } });
  });

  it('sends the auto trigger when the plan was started automatically', async () => {
    await executePlan('plan-1', identity, 'auto');
    expect(http.post).toHaveBeenCalledWith('/api/plans/plan-1/execute', { trigger: 'auto' }, { headers: {} });
  });

  it('defaults to manual so existing call sites keep working', async () => {
    await executePlan('plan-1', identity);
    expect(http.post).toHaveBeenCalledWith('/api/plans/plan-1/execute', { trigger: 'manual' }, { headers: {} });
  });
});
