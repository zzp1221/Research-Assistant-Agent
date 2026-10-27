import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  AUTO_RUN_ON_CREATE_STORAGE_KEY,
  AutoRunOnCreateContext,
  isAutoRunPlanEligible,
  useResearchAssistantChatFlow,
  type PlanReadyOptions,
} from '../../src/app/hooks/useResearchAssistantChatFlow';
import { uiText } from '../../src/app/constants/uiText';
import { ChatComposer } from '../../src/features/chat/ChatComposer';
import type { PlanGraph } from '../../src/contracts/api';
import * as api from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  PLAN_CREATE_TIMEOUT_MESSAGE: '计划创建超时；请稍后刷新计划库确认是否已创建。',
  chat: vi.fn(),
  createPlan: vi.fn(),
  getMe: vi.fn(),
  getPlan: vi.fn(),
  listPlans: vi.fn(),
  uploadFile: vi.fn(),
}));

const makePlanGraph = (overrides: Partial<PlanGraph> = {}): PlanGraph => ({
  id: 'plan-auto',
  user_intent: '画一个正弦函数和余弦函数的对比图',
  intent_type: 'Code_Execution',
  status: 'ready',
  trace_id: 'trace-auto',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 2, max_duration_seconds: 600 },
  usage: { task_attempts: 0 },
  nodes: [{ id: 'node-plot', type: 'render_plot' }] as unknown as PlanGraph['nodes'],
  edges: [],
  ...overrides,
});

const noop = () => undefined;

function Harness({ onPlanReady }: { onPlanReady: (planGraph: PlanGraph, options: PlanReadyOptions) => void }) {
  const flow = useResearchAssistantChatFlow({
    onPlanGraphChanged: noop,
    onPlanReady,
  });

  return (
    <AutoRunOnCreateContext.Provider value={flow.autoRunControl}>
      <div data-testid="chat-log">
        {flow.chatHistory.map((message, index) => (
          <p key={`${message.role}-${index}`} data-testid="chat-message" data-role={message.role}>
            {message.text}
          </p>
        ))}
      </div>
      <ChatComposer
        prompt={flow.prompt}
        hasConversation={flow.chatHistory.some((message) => message.role === 'user')}
        loading={flow.loading}
        isLoggedIn={flow.isLoggedIn}
        showSuggestions={false}
        pendingAttachments={flow.pendingAttachments}
        uploadingAttachments={flow.uploadingAttachments}
        attachmentError={flow.attachmentError}
        setPrompt={flow.setPrompt}
        setShowSuggestions={noop}
        onSendMessage={() => void flow.handleSendMessage()}
        onAttachFiles={flow.handleAttachFiles}
        onRemoveAttachment={flow.handleRemoveAttachment}
        onOpenAttachment={noop}
      />
    </AutoRunOnCreateContext.Provider>
  );
}

const chatTexts = () => screen.getAllByTestId('chat-message').map((node) => node.textContent ?? '');

describe('isAutoRunPlanEligible', () => {
  it('requires the switch, a Code_Execution plan, no approval and a plot node', () => {
    expect(isAutoRunPlanEligible(makePlanGraph(), true)).toBe(true);
    expect(isAutoRunPlanEligible(makePlanGraph(), false)).toBe(false);
    expect(isAutoRunPlanEligible(makePlanGraph({ intent_type: 'General' }), true)).toBe(false);
    expect(
      isAutoRunPlanEligible(makePlanGraph({ approval: { required: true, status: 'awaiting_approval' } }), true),
    ).toBe(false);
    expect(
      isAutoRunPlanEligible(
        makePlanGraph({ nodes: [{ id: 'node-code', type: 'execute_code' }] as unknown as PlanGraph['nodes'] }),
        true,
      ),
    ).toBe(false);
  });
});

describe('auto-run on create switch', () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    vi.mocked(api.getMe).mockResolvedValue({ user_id: 'user-1', session_id: 'session-1' });
    vi.mocked(api.listPlans).mockResolvedValue({ plans: [], total: 0, next_cursor: '' });
  });

  it('persists the toggle and passes autoRun to the flow before starting', async () => {
    const user = userEvent.setup();
    const onPlanReady = vi.fn();
    vi.mocked(api.createPlan).mockResolvedValue({
      message: 'ok',
      plan_graph: makePlanGraph(),
    });

    const { unmount } = render(<Harness onPlanReady={onPlanReady} />);
    await waitFor(() => expect(api.getMe).toHaveBeenCalled());

    const toggle = screen.getByRole('switch', { name: uiText.autoRunSwitchLabel });
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByText(uiText.autoRunSwitchHint)).toBeInTheDocument();
    expect(AUTO_RUN_ON_CREATE_STORAGE_KEY).toBe('research-assistant.auto-run-on-create.v1');

    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    expect(window.localStorage.getItem(AUTO_RUN_ON_CREATE_STORAGE_KEY)).toBe('1');

    unmount();
    render(<Harness onPlanReady={onPlanReady} />);
    const persistedToggle = screen.getByRole('switch', { name: uiText.autoRunSwitchLabel });
    expect(persistedToggle).toHaveAttribute('aria-checked', 'true');
    await user.click(persistedToggle);
    expect(persistedToggle).toHaveAttribute('aria-checked', 'false');
    expect(window.localStorage.getItem(AUTO_RUN_ON_CREATE_STORAGE_KEY)).toBe('0');
    await user.click(persistedToggle);

    await user.type(screen.getByRole('textbox'), '画一个正弦函数和余弦函数的对比图');
    await user.click(screen.getByRole('button', { name: '发送消息' }));

    await waitFor(() => expect(onPlanReady).toHaveBeenCalledTimes(1));
    expect(onPlanReady).toHaveBeenCalledWith(expect.objectContaining({ id: 'plan-auto' }), { autoRun: true }, null);

    const texts = chatTexts();
    expect(texts).toContain(uiText.planGeneratedBrief);
    expect(texts).toContain(uiText.planAutoStarted);
    expect(texts.indexOf(uiText.planAutoStarted)).toBe(texts.indexOf(uiText.planGeneratedBrief) + 1);
    expect(texts).not.toContain(uiText.planGenerated);
  });

  it('defaults to manual so plan creation never starts a run by itself', async () => {
    const user = userEvent.setup();
    const onPlanReady = vi.fn();
    vi.mocked(api.createPlan).mockResolvedValue({
      message: 'ok',
      plan_graph: makePlanGraph(),
    });

    render(<Harness onPlanReady={onPlanReady} />);
    await waitFor(() => expect(api.getMe).toHaveBeenCalled());
    expect(screen.getByRole('switch', { name: uiText.autoRunSwitchLabel })).toHaveAttribute('aria-checked', 'false');
    await user.type(screen.getByRole('textbox'), '画一个正弦函数和余弦函数的对比图');
    await user.click(screen.getByRole('button', { name: '发送消息' }));

    await waitFor(() => expect(onPlanReady).toHaveBeenCalledTimes(1));
    expect(onPlanReady).toHaveBeenCalledWith(expect.anything(), { autoRun: false }, null);

    const texts = chatTexts();
    expect(texts).toContain(uiText.planGenerated);
    expect(texts).not.toContain(uiText.planAutoStarted);
    expect(texts).not.toContain(uiText.planGeneratedBrief);
  });

  it('never claims an automatic start when plan approval is required', async () => {
    const user = userEvent.setup();
    const onPlanReady = vi.fn();
    vi.mocked(api.createPlan).mockResolvedValue({
      message: 'ok',
      plan_graph: makePlanGraph({ approval: { required: true, status: 'awaiting_approval' } }),
    });

    render(<Harness onPlanReady={onPlanReady} />);
    await waitFor(() => expect(api.getMe).toHaveBeenCalled());
    await user.click(screen.getByRole('switch', { name: uiText.autoRunSwitchLabel }));
    await user.type(screen.getByRole('textbox'), '复现这篇论文的核心实验');
    await user.click(screen.getByRole('button', { name: '发送消息' }));

    await waitFor(() => expect(onPlanReady).toHaveBeenCalledTimes(1));
    expect(onPlanReady).toHaveBeenCalledWith(expect.anything(), { autoRun: false }, null);

    const texts = chatTexts();
    expect(texts).toContain(uiText.planGenerated);
    expect(texts).not.toContain(uiText.planAutoStarted);
    expect(texts).not.toContain(uiText.planGeneratedBrief);
  });
});
