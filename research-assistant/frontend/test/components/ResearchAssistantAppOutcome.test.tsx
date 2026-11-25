import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import ResearchAssistantApp from '../../src/app/ResearchAssistantApp';
import type { OutcomeDetails, PlanGraph } from '../../src/contracts/api';
import { useResearchAssistantChatFlow } from '../../src/app/hooks/useResearchAssistantChatFlow';
import { useResearchAssistantRuntime } from '../../src/app/hooks/useResearchAssistantRuntime';
import { useResearchAssistantLayoutState } from '../../src/app/hooks/useResearchAssistantLayoutState';
import { usePdfAssistFlow } from '../../src/app/hooks/usePdfAssistFlow';
import { useWorkbenchBudget } from '../../src/app/hooks/useWorkbenchBudget';

vi.mock('../../src/app/hooks/useResearchAssistantChatFlow', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../src/app/hooks/useResearchAssistantChatFlow')>(),
  useResearchAssistantChatFlow: vi.fn(),
}));
vi.mock('../../src/app/hooks/useResearchAssistantRuntime', () => ({ useResearchAssistantRuntime: vi.fn() }));
vi.mock('../../src/app/hooks/useResearchAssistantLayoutState', () => ({ useResearchAssistantLayoutState: vi.fn() }));
vi.mock('../../src/app/hooks/usePdfAssistFlow', () => ({ usePdfAssistFlow: vi.fn() }));
vi.mock('../../src/app/hooks/useWorkbenchBudget', () => ({ useWorkbenchBudget: vi.fn() }));
vi.mock('../../src/app/components/LeftWorkspace', () => ({ LeftWorkspaceChat: () => null, LeftWorkspacePdf: () => null }));
vi.mock('../../src/app/components/ResearchAssistantAppShell', async () => {
  const { GraphPanel } = await import('../../src/features/plan-graph/GraphPanel');
  return {
    // Keep the real App → Workspace → view model → GraphPanel data path.
    // This host must never inject fixture outcome fields of its own.
    ResearchAssistantAppShell: ({ graphExecutionViewModel }: {
      graphExecutionViewModel: ReturnType<typeof import('../../src/app/viewModels/useGraphExecutionViewModel').useGraphExecutionViewModel>;
    }) => <GraphPanel {...graphExecutionViewModel.graphPanelProps} />,
  };
});
vi.mock('../../src/features/plan-graph/GraphCanvas', () => ({ default: () => <div /> }));
vi.mock('../../src/services/api/researchAssistantApi', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../src/services/api/researchAssistantApi')>(),
  listApprovals: vi.fn(async () => ({ items: [] })),
}));
vi.mock('../../src/services/desktopBridge', () => ({
  getDesktopBridge: () => ({ needsOnboarding: async () => false }),
}));

const makeDetails = (prefix = 'A'): OutcomeDetails => ({
  mode: 'acceptance',
  accepted_goals: [{ id: `${prefix}-goal`, statement: `${prefix}核心实验已验收`, verified: true, evidence_refs: [`${prefix}-report`] }],
  optional_losses: [{ task_id: `${prefix}-metric`, name: `${prefix}可选指标`, status: 'failed', reason: `${prefix}指标数据集缺失` }],
  limitations: [`${prefix}可选指标相关结论不能宣称完整`],
});

const makePlan = (overrides: Partial<PlanGraph> = {}): PlanGraph => ({
  id: 'plan-a',
  user_intent: '复现核心实验并计算可选指标',
  intent_type: 'Code_Execution',
  status: 'completed',
  outcome: 'partial',
  outcome_reason: 'A核心目标通过，可选工作未完成',
  outcome_details: makeDetails(),
  trace_id: 'trace-a',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 2, max_duration_seconds: 600 },
  usage: { task_attempts: 0 },
  nodes: [],
  edges: [],
  ...overrides,
});

const setPlan = (plan: PlanGraph) => {
  vi.mocked(useResearchAssistantChatFlow).mockReturnValue({
    activePlanId: plan.id,
    activePlanStatus: plan.status,
    activePlanGraph: plan,
    intentContext: null,
    chatHistory: [],
    pendingAttachments: [],
    documentReferences: [],
    sessionSummaries: [],
    prompt: '',
    requestIdentity: {},
    runtimePermissionMode: 'confirm_important',
    appendChatMessage: vi.fn(),
    refreshActivePlan: vi.fn(async () => undefined),
    autoRunControl: { enabled: false, setEnabled: vi.fn() },
  } as unknown as ReturnType<typeof useResearchAssistantChatFlow>);
};

const renderPlan = (plan = makePlan()) => {
  setPlan(plan);
  const view = render(<ResearchAssistantApp />);
  return {
    ...view,
    updatePlan: (next: PlanGraph) => {
      setPlan(next);
      view.rerender(<ResearchAssistantApp />);
    },
  };
};

const expectNoADetails = () => {
  for (const text of ['A核心实验已验收', 'A-report', 'A可选指标', 'A指标数据集缺失', 'A可选指标相关结论不能宣称完整']) {
    expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
  }
};

describe('ResearchAssistantApp outcome wiring', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.history.replaceState(null, '', '/');
    vi.mocked(useResearchAssistantLayoutState).mockReturnValue({ sidebarWidth: 440, leftPanelWidth: 40, chatCollapsed: false } as ReturnType<typeof useResearchAssistantLayoutState>);
    vi.mocked(useWorkbenchBudget).mockReturnValue({ summary: null, budget: null, alerts: [], loading: false, error: '', refresh: vi.fn() });
    vi.mocked(usePdfAssistFlow).mockReturnValue({ pdfUrl: null, documentError: '' } as ReturnType<typeof usePdfAssistFlow>);
    vi.mocked(useResearchAssistantRuntime).mockReturnValue({
      executionState: { selectedTask: null, nodeStates: {}, displayMode: 'logs', isExecuting: false, approvalResolved: false },
      selectedTaskState: { logs: '', result: '', code: '', structuredData: '', imageBase64: '' },
      handleRunAllTasks: vi.fn(),
      appendSelectedTaskLog: vi.fn(),
      onNodeClick: vi.fn(),
    } as unknown as ReturnType<typeof useResearchAssistantRuntime>);
  });

  it('passes optional losses through the real application wiring', () => {
    renderPlan();
    const banner = within(screen.getByTestId('plan-outcome-details'));
    expect(banner.getByText(/A指标数据集缺失/)).toBeInTheDocument();
    expect(banner.getByText(/A可选指标相关结论不能宣称完整/)).toBeInTheDocument();
  });

  it('shows the accepted core goal, evidence and reason from the active plan', () => {
    renderPlan();
    const banner = screen.getByTestId('plan-outcome-details');
    expect(banner).toHaveTextContent('部分完成');
    expect(banner).toHaveTextContent('A核心实验已验收');
    expect(banner).toHaveTextContent('A-report');
    expect(banner).toHaveTextContent('A核心目标通过，可选工作未完成');
  });

  it('removes all previous explanation when switching to a terminal plan without details', () => {
    const { updatePlan } = renderPlan();
    expect(screen.getByTestId('plan-outcome-details')).toHaveTextContent('A核心实验已验收');
    updatePlan(makePlan({ id: 'plan-b', outcome_reason: 'B核心已通过，旧计划没有结构化详情', outcome_details: undefined }));
    expectNoADetails();
    expect(screen.getByTestId('plan-outcome-details')).toHaveTextContent('B核心已通过，旧计划没有结构化详情');
    expect(screen.getByTestId('plan-outcome-details')).toHaveTextContent('该计划未提供结构化验收详情');
  });

  it('hides the previous terminal explanation after switching to a running plan', () => {
    const { updatePlan } = renderPlan();
    updatePlan(makePlan({ id: 'plan-b', status: 'in_progress', outcome: undefined, outcome_reason: undefined, outcome_details: undefined }));
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
    expectNoADetails();
  });

  it('replaces details when the same plan ID, status and outcome stay unchanged', () => {
    const plan = makePlan();
    const { updatePlan } = renderPlan(plan);
    updatePlan({ ...plan, outcome_details: makeDetails('更新') });
    expectNoADetails();
    const banner = screen.getByTestId('plan-outcome-details');
    expect(banner).toHaveTextContent('更新核心实验已验收');
    expect(banner).toHaveTextContent('更新-report');
    expect(banner).toHaveTextContent('更新指标数据集缺失');
    expect(banner).toHaveTextContent('更新可选指标相关结论不能宣称完整');
  });

  it.each([null, undefined])('clears the same plan details when the next snapshot contains %s', (details) => {
    const plan = makePlan();
    const { updatePlan } = renderPlan(plan);
    // null is accepted from older or mixed-version API snapshots at runtime.
    updatePlan({ ...plan, outcome_details: details } as PlanGraph);
    expectNoADetails();
    const banner = screen.getByTestId('plan-outcome-details');
    expect(banner).toHaveTextContent(plan.outcome_reason!);
    expect(banner).toHaveTextContent('该计划未提供结构化验收详情');
  });

  it('updates the reason when the same plan keeps its details object', () => {
    const plan = makePlan();
    const { updatePlan } = renderPlan(plan);
    updatePlan({ ...plan, outcome_reason: '同计划的新结果原因' });
    const banner = screen.getByTestId('plan-outcome-details');
    expect(banner).toHaveTextContent('同计划的新结果原因');
    expect(banner).not.toHaveTextContent('A核心目标通过，可选工作未完成');
    expect(banner).toHaveTextContent('A指标数据集缺失');
  });

  it('shows a partial explanation after a running plan receives a terminal snapshot', () => {
    const terminal = makePlan();
    const { updatePlan } = renderPlan({ ...terminal, status: 'in_progress', outcome: undefined, outcome_reason: undefined, outcome_details: undefined });
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
    updatePlan(terminal);
    expect(screen.getByTestId('plan-outcome-details')).toHaveTextContent('A指标数据集缺失');
    expect(screen.getByTestId('plan-outcome-details')).toHaveTextContent('A-report');
  });
});
