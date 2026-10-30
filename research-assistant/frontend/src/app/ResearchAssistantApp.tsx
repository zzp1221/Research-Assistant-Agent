import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { GitBranch, MessageSquareText } from 'lucide-react';

import { LeftWorkspaceChat, LeftWorkspacePdf } from './components/LeftWorkspace';
import { useResearchAssistantRuntimeContext, type ResearchAssistantRuntimeContextValue } from './context/ResearchAssistantRuntimeContext';
import { ResearchAssistantRuntimeProvider } from './context/ResearchAssistantRuntimeProvider';
import { usePdfAssistFlow } from './hooks/usePdfAssistFlow';
import { AutoRunOnCreateContext, useResearchAssistantChatFlow } from './hooks/useResearchAssistantChatFlow';
import { useGraphState } from './hooks/useGraphState';
import { useResearchAssistantLayoutState } from './hooks/useResearchAssistantLayoutState';
import { useResearchAssistantRuntime } from './hooks/useResearchAssistantRuntime';
import { readPlanRunTrigger, useGraphExecutionViewModel, type PlanRunTrigger } from './viewModels/useGraphExecutionViewModel';
import { ExecutionSidebar } from '../features/execution/ExecutionSidebar';
import { HelpDrawer, type HelpSection } from '../features/help/HelpDrawer';
import { GraphPanel } from '../features/plan-graph/GraphPanel';
import { buildGraphLayout } from '../features/plan-graph/buildGraphLayout';
import { SettingsPanel } from '../features/settings/SettingsPanel';
import { PlanLibraryPanel } from '../features/plan-library/PlanLibraryPanel';
import { CostConsolePanel } from '../features/cost/CostConsolePanel';
import { PlanReviewPanel } from '../features/review/PlanReviewPanel';
import { ApprovalInboxPanel } from '../features/review/ApprovalInboxPanel';
import { WorkbenchRail } from '../features/workbench/WorkbenchRail';
import { WorkbenchStatusBar } from '../features/workbench/WorkbenchStatusBar';
import { useWorkbenchBudget, type WorkbenchBudgetState } from './hooks/useWorkbenchBudget';
import { answerQuestion, listApprovals } from '../services/api/researchAssistantApi';
import type { IntentContext, MissingRequirement, PlanQuestion, RuntimeAction, UploadedFile } from '../contracts/api';
import type { PlanExecutionTrigger } from '../services/api/researchAssistantApi';
import { isPdfAttachment } from '../features/pdf-viewer/pdfAttachment';

interface ResearchAssistantAppShellProps {
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  leftWorkspace: ReactNode;
  graphExecutionViewModel: ReturnType<typeof useGraphExecutionViewModel>;
  mobilePane: 'chat' | 'graph';
  onMobilePaneChange: (pane: 'chat' | 'graph') => void;
  workspaceView: 'chat' | 'literature';
  onWorkspaceViewChange: (view: 'chat' | 'literature') => void;
  accountLabel: string;
  budget: WorkbenchBudgetState;
  pendingWorkCount: number;
  pendingApprovalCount: number;
  pendingQuestionCount: number;
  activePlanStatus: string | null;
  reviewRequired: boolean;
  reviewStatus?: string | null;
  showModelSettings?: boolean;
  onOpenModelSettings?: () => void;
  onOpenPlanLibrary?: () => void;
  onOpenCostConsole?: () => void;
  onOpenReview?: () => void;
  onOpenInbox?: () => void;
  onOpenHelp: () => void;
}

function ResearchAssistantAppShell(props: ResearchAssistantAppShellProps) {
  const { layout, leftWorkspace, graphExecutionViewModel, mobilePane, onMobilePaneChange, workspaceView, onWorkspaceViewChange, accountLabel, budget, pendingWorkCount, pendingApprovalCount, pendingQuestionCount, activePlanStatus, reviewRequired, reviewStatus, onOpenModelSettings, onOpenPlanLibrary, onOpenCostConsole, onOpenReview, onOpenInbox, onOpenHelp } = props;
  const graphNodeCount = graphExecutionViewModel.graphPanelProps.nodes.length;
  const isResizing = layout.isResizing || layout.isResizingSidebar;

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (document.querySelector('[role="dialog"][aria-modal="true"]')) return;
      if (!graphExecutionViewModel.executionSidebarProps) return;
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
      graphExecutionViewModel.executionSidebarProps.onClose();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [graphExecutionViewModel.executionSidebarProps]);

  const graphProps = graphExecutionViewModel.graphPanelProps;
  const stepCount = graphProps.nodes.length;
  const completedCount = graphProps.nodes.filter((node) => node.data.status === 'completed').length;
  const runningCount = graphProps.nodes.filter((node) => node.data.status === 'in_progress').length;

  return (
    <div
      className="research-assistant-app-shell font-sans text-ink"
      data-mobile-pane={mobilePane}
      data-resizing={isResizing ? 'true' : 'false'}
    >
      <WorkbenchRail
        activeView={workspaceView}
        planId={graphProps.planId ?? null}
        pendingWorkCount={pendingWorkCount}
        reviewPending={reviewRequired && reviewStatus !== 'approved'}
        accountLabel={accountLabel}
        onSelectView={onWorkspaceViewChange}
        onOpenPlanLibrary={onOpenPlanLibrary ?? (() => undefined)}
        onOpenCostConsole={onOpenCostConsole ?? (() => undefined)}
        onOpenInbox={onOpenInbox ?? (() => undefined)}
        onOpenReview={onOpenReview ?? (() => undefined)}
        onOpenHelp={onOpenHelp}
        onOpenModelSettings={onOpenModelSettings}
      />
      <div className="research-assistant-workbench-stack">
        <div className="research-assistant-workbench-row">
          <div className="research-assistant-mobile-tabbar">
            <button
              type="button"
              onClick={() => onMobilePaneChange('chat')}
              aria-pressed={mobilePane === 'chat'}
              className={mobilePane === 'chat' ? 'is-active' : ''}
            >
              <MessageSquareText className="h-4 w-4" />
              对话
            </button>
            <button
              type="button"
              onClick={() => onMobilePaneChange('graph')}
              aria-pressed={mobilePane === 'graph'}
              className={mobilePane === 'graph' ? 'is-active' : ''}
            >
              <GitBranch className="h-4 w-4" />
              执行图{graphNodeCount > 0 ? ` ${graphNodeCount}` : ''}
            </button>
          </div>

          {leftWorkspace}

          <div
            className="research-assistant-left-resize-handle group relative flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-line"
            onPointerDown={(event) => {
              event.preventDefault();
              layout.startResizingLeftPanel();
            }}
            onDoubleClick={layout.resetLeftPanel}
            title="拖动调整对话栏宽度 · 双击恢复默认"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整对话栏宽度"
          >
            <div className={`h-12 w-px rounded-full transition-colors ${layout.isResizing ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent'}`} />
          </div>

          <div className="research-assistant-graph-workspace relative flex min-w-0 flex-1 overflow-hidden">
            <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

            {graphExecutionViewModel.showExecutionResizeHandle && (
              <div
                className="group relative z-20 flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-surface-muted"
                onPointerDown={(event) => {
                  event.preventDefault();
                  layout.startResizingSidebar();
                }}
                onDoubleClick={layout.resetSidebar}
                title="拖动调整详情栏宽度 · 双击恢复默认"
                role="separator"
                aria-orientation="vertical"
                aria-label="调整详情栏宽度"
              >
                <div className={`h-12 w-px rounded-full transition-colors ${layout.isResizingSidebar ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent'}`} />
              </div>
            )}

            {graphExecutionViewModel.executionSidebarProps && <ExecutionSidebar {...graphExecutionViewModel.executionSidebarProps} />}
          </div>
        </div>

        <WorkbenchStatusBar
          planId={graphProps.planId ?? null}
          planStatus={activePlanStatus}
          reviewRequired={reviewRequired}
          reviewStatus={reviewStatus}
          stepCount={stepCount}
          completedCount={completedCount}
          runningCount={runningCount}
          budget={budget}
          pendingWorkCount={pendingWorkCount}
          pendingApprovalCount={pendingApprovalCount}
          pendingQuestionCount={pendingQuestionCount}
          permissionMode={graphProps.runtimePermissionMode ?? null}
          onOpenPlanLibrary={onOpenPlanLibrary ?? (() => undefined)}
          onOpenCostConsole={onOpenCostConsole ?? (() => undefined)}
          onOpenInbox={onOpenInbox ?? (() => undefined)}
          onOpenReview={onOpenReview ?? (() => undefined)}
        />
      </div>
    </div>
  );
}

interface ResearchAssistantWorkspaceContentProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  logsEndRef: RefObject<HTMLDivElement | null>;
  showModelSettings: boolean;
  onOpenModelSettings: () => void;
  onCloseModelSettings: () => void;
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  budget: WorkbenchBudgetState;
  pendingWorkCounts: { total: number; approvals: number; questions: number };
  chatFlow: {
    chatHistory: ReturnType<typeof useResearchAssistantChatFlow>['chatHistory'];
    appendChatMessage: ReturnType<typeof useResearchAssistantChatFlow>['appendChatMessage'];
    loading: boolean;
    prompt: string;
    setPrompt: (value: string) => void;
    handleSendMessage: () => void;
    pendingAttachments: ReturnType<typeof useResearchAssistantChatFlow>['pendingAttachments'];
    uploadingAttachments: boolean;
    attachmentError: string;
    handleAttachFiles: (files: File[]) => void;
    handleRemoveAttachment: (uploadId: string) => void;
    attachUploadToSession: (upload: UploadedFile) => 'added' | 'duplicate' | 'full';
    intentContext: IntentContext | null;
    activePlanId: string | null;
    activePlanStatus: string | null;
    activePlanOutcome: string | null;
    activePlanMissingRequirements: MissingRequirement[];
    activePlanRuntimeActions: RuntimeAction[];
    activePlanQuestions?: PlanQuestion[];
    activePlanReviewStatus?: string | null;
    activePlanReviewRequired?: boolean;
    activePlanRuntimePolicyMode: string | null;
    activePlanPlannerSource: string | null;
    activePlanPlannerFallbackReason: string | null;
    activePlanComplexity: string | null;
    activePlanPlanningRoute: string | null;
    activePlanTemplateVariant: string | null;
    activePlanRunTrigger: PlanRunTrigger;
    activePlanVerification: import('../contracts/api').Verification | null;
    activePlanUsage: import('../contracts/api').TaskUsage | null;
    runtimePermissionMode: 'confirm_important' | 'full_access';
    showFullAccessConfirm: boolean;
    handleRuntimePermissionModeChange: (mode: 'confirm_important' | 'full_access') => void;
    handleConfirmFullAccess: () => void;
    handleCancelFullAccess: () => void;
    isLoggedIn: boolean;
    userId: string | null;
    loginInput: string;
    setLoginInput: (value: string) => void;
    activeSessionId: string | null;
    sessionSummaries: ReturnType<typeof useResearchAssistantChatFlow>['sessionSummaries'];
    handleLogin: () => void;
    handleCreateSession: () => void;
    handleSwitchSession: (sessionId: string) => void;
    handleOpenServerPlan: (planId: string) => Promise<void>;
  };
  pdfFlow: ReturnType<typeof usePdfAssistFlow>;
  mobilePane: 'chat' | 'graph';
  onMobilePaneChange: (pane: 'chat' | 'graph') => void;
  workspaceView: 'chat' | 'literature';
  onWorkspaceViewChange: (view: 'chat' | 'literature') => void;
  onOpenHelp: (section?: HelpSection) => void;
  onOpenPlanLibrary: () => void;
  onOpenCostConsole: () => void;
  onOpenReview?: () => void;
  onOpenInbox?: () => void;
  onAnswerQuestion?: (questionId: string, answer: { answer?: string; answer_option?: string }) => Promise<void> | void;
}

function ResearchAssistantWorkspaceContent(props: ResearchAssistantWorkspaceContentProps) {
  const { nodes, edges, onNodesChange, onEdgesChange, logsEndRef, showModelSettings, onOpenModelSettings, onCloseModelSettings, layout, budget, pendingWorkCounts, chatFlow, pdfFlow, mobilePane, onMobilePaneChange, workspaceView, onWorkspaceViewChange, onOpenHelp, onOpenPlanLibrary, onOpenCostConsole, onOpenReview, onOpenInbox } = props;
  const runtime = useResearchAssistantRuntimeContext();
  const graphExecutionViewModel = useGraphExecutionViewModel({
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    intentContext: chatFlow.intentContext,
    activePlanId: chatFlow.activePlanId,
    activePlanStatus: chatFlow.activePlanStatus,
    activePlanOutcome: chatFlow.activePlanOutcome,
    activePlanMissingRequirements: chatFlow.activePlanMissingRequirements,
    activePlanRuntimeActions: chatFlow.activePlanRuntimeActions,
    activePlanQuestions: chatFlow.activePlanQuestions ?? [],
    activePlanReviewStatus: chatFlow.activePlanReviewStatus ?? null,
    activePlanReviewRequired: chatFlow.activePlanReviewRequired ?? false,
    onAnswerQuestion: props.onAnswerQuestion,
    onOpenReview: props.onOpenReview,
    activePlanRuntimePolicyMode: chatFlow.activePlanRuntimePolicyMode,
    activePlanPlannerSource: chatFlow.activePlanPlannerSource,
    activePlanPlannerFallbackReason: chatFlow.activePlanPlannerFallbackReason,
    activePlanComplexity: chatFlow.activePlanComplexity,
    activePlanPlanningRoute: chatFlow.activePlanPlanningRoute,
    activePlanTemplateVariant: chatFlow.activePlanTemplateVariant,
    activePlanRunTrigger: chatFlow.activePlanRunTrigger,
    activePlanVerification: chatFlow.activePlanVerification ?? null,
    activePlanUsage: chatFlow.activePlanUsage ?? null,
    onOpenModelSettings,
    layout,
    logsEndRef,
  });

  const leftWorkspace = pdfFlow.pdfUrl ? (
    <LeftWorkspacePdf
      widthPercent={layout.leftPanelWidth}
      pdfUrl={pdfFlow.pdfUrl}
      documentInfo={pdfFlow.documentInfo}
      onClosePdf={pdfFlow.closePdf}
      translation={pdfFlow.translation}
      onAskAI={pdfFlow.handleAskAI}
      onAssist={pdfFlow.assistWithUpload}
      onOpenHelp={() => onOpenHelp('pdf')}
    />
  ) : (
    <LeftWorkspaceChat
      widthPercent={layout.leftPanelWidth}
      activeView={workspaceView}
      onActiveViewChange={onWorkspaceViewChange}
      state={{
        chatHistory: chatFlow.chatHistory,
        loading: chatFlow.loading,
        prompt: chatFlow.prompt,
        showSuggestions: pdfFlow.showSuggestions,
        pendingAttachments: chatFlow.pendingAttachments,
        uploadingAttachments: chatFlow.uploadingAttachments,
        attachmentError: chatFlow.attachmentError,
        runtimePermissionMode: chatFlow.runtimePermissionMode,
        showFullAccessConfirm: chatFlow.showFullAccessConfirm,
        isLoggedIn: chatFlow.isLoggedIn,
        userId: chatFlow.userId,
        loginInput: chatFlow.loginInput,
        activeSessionId: chatFlow.activeSessionId,
        sessions: chatFlow.sessionSummaries,
      }}
      chatActions={{
        setPrompt: chatFlow.setPrompt,
        setShowSuggestions: pdfFlow.setShowSuggestions,
        onSendMessage: chatFlow.handleSendMessage,
        setLoginInput: chatFlow.setLoginInput,
        onLogin: chatFlow.handleLogin,
        onCreateSession: chatFlow.handleCreateSession,
        onSwitchSession: chatFlow.handleSwitchSession,
        onAttachFiles: chatFlow.handleAttachFiles,
        onRemoveAttachment: chatFlow.handleRemoveAttachment,
        onOpenAttachment: pdfFlow.openPdfFromUpload,
        onRuntimePermissionModeChange: chatFlow.handleRuntimePermissionModeChange,
        onConfirmFullAccess: chatFlow.handleConfirmFullAccess,
        onCancelFullAccess: chatFlow.handleCancelFullAccess,
      }}
      pdfActions={{
        onOpenPdf: () => {
          const pdfAttachment = chatFlow.pendingAttachments.find(isPdfAttachment);
          if (!pdfAttachment) {
            chatFlow.appendChatMessage({ role: 'system', text: '请先上传 PDF 附件，再打开阅读器。' });
            return;
          }
          void pdfFlow.openPdfFromUpload(pdfAttachment);
        },
        onClosePdf: pdfFlow.closePdf,
      }}
      taskActions={{
        onOpenTaskView: runtime.actions.handleOpenTaskView,
      }}
      literatureActions={{
        activeSessionId: chatFlow.activeSessionId,
        onAttachToSession: chatFlow.attachUploadToSession,
        onDetachFromSession: chatFlow.handleRemoveAttachment,
        onOpenPdf: pdfFlow.openPdfFromUpload,
        onOpenHelp: () => onOpenHelp('library'),
      }}
    />
  );

  return (
    <>
      <ResearchAssistantAppShell
        layout={layout}
        leftWorkspace={leftWorkspace}
        graphExecutionViewModel={graphExecutionViewModel}
        mobilePane={mobilePane}
        onMobilePaneChange={onMobilePaneChange}
        workspaceView={workspaceView}
        onWorkspaceViewChange={onWorkspaceViewChange}
        accountLabel={chatFlow.userId ? `账户 ${chatFlow.userId.slice(0, 8)}` : '游客'}
        budget={budget}
        pendingWorkCount={pendingWorkCounts.total}
        pendingApprovalCount={pendingWorkCounts.approvals}
        pendingQuestionCount={pendingWorkCounts.questions}
        activePlanStatus={chatFlow.activePlanStatus}
        reviewRequired={Boolean(chatFlow.activePlanReviewRequired)}
        reviewStatus={chatFlow.activePlanReviewStatus}
        onOpenModelSettings={onOpenModelSettings}
        onOpenPlanLibrary={onOpenPlanLibrary}
        onOpenCostConsole={onOpenCostConsole}
        onOpenReview={onOpenReview}
        onOpenInbox={onOpenInbox}
        onOpenHelp={() => onOpenHelp('start')}
      />
      {showModelSettings && (
        <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-8">
          <div className="w-full max-w-5xl overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
            <SettingsPanel
              onClose={onCloseModelSettings}
              initialTab={
                typeof window !== 'undefined' && window.location.hash === '#settings:tools'
                  ? 'tools'
                  : typeof window !== 'undefined' && window.location.hash === '#settings:agents'
                    ? 'agents'
                    : 'models'
              }
            />
          </div>
        </div>
      )}
    </>
  );
}

export default function ResearchAssistantApp() {
  const { nodes, setNodes, onNodesChange, edges, setEdges, onEdgesChange } = useGraphState();
  const [mobilePane, setMobilePane] = useState<'chat' | 'graph'>('chat');
  const [workspaceView, setWorkspaceView] = useState<'chat' | 'literature'>('chat');
  const [showModelSettings, setShowModelSettings] = useState(() =>
    typeof window !== 'undefined' && window.location.hash.startsWith('#settings:'),
  );
  const [showPlanLibrary, setShowPlanLibrary] = useState(false);
  const [showCostConsole, setShowCostConsole] = useState(false);
  const [showReview, setShowReview] = useState(false);
  const [showInbox, setShowInbox] = useState(false);
  const [pendingWorkCounts, setPendingWorkCounts] = useState({ total: 0, approvals: 0, questions: 0 });
  const [showHelp, setShowHelp] = useState(false);
  const [helpSection, setHelpSection] = useState<HelpSection>('start');
  const logsEndRef = useRef<HTMLDivElement>(null);
  const runtimeActionsRef = useRef<((planId: string, trigger?: PlanExecutionTrigger) => void) | null>(null);
  const layout = useResearchAssistantLayoutState();

  useEffect(() => {
    let cancelled = false;
    const loadPending = async () => {
      try {
        const payload = await listApprovals({ status: 'pending', kind: 'all', limit: 200 });
        if (!cancelled) {
          const counts = payload.items.reduce(
            (acc, item) => {
              acc.total += 1;
              if (item.kind === 'approval') acc.approvals += 1;
              if (item.kind === 'question') acc.questions += 1;
              return acc;
            },
            { total: 0, approvals: 0, questions: 0 },
          );
          setPendingWorkCounts(counts);
        }
      } catch {
        // The inbox badge is advisory; a transient failure must not disturb the
        // workbench or surface a scary error.
      }
    };
    void loadPending();
    const timer = window.setInterval(() => void loadPending(), 20000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  const openHelp = useCallback((section: HelpSection = 'start') => {
    setHelpSection(section);
    setShowHelp(true);
  }, []);

  const openModelSettings = useCallback(() => {
    if (typeof window !== 'undefined') {
      window.history.replaceState(null, '', '#settings:models');
    }
    setShowModelSettings(true);
  }, []);
  const closeModelSettings = useCallback(() => {
    if (typeof window !== 'undefined') {
      window.history.replaceState(null, '', window.location.pathname + window.location.search);
    }
    setShowModelSettings(false);
  }, []);

  useEffect(() => {
    const onHashChange = () => setShowModelSettings(window.location.hash === '#settings:models');
    const onOpenEvent = () => setShowModelSettings(true);
    window.addEventListener('hashchange', onHashChange);
    window.addEventListener('ra:open-model-settings', onOpenEvent);
    return () => {
      window.removeEventListener('hashchange', onHashChange);
      window.removeEventListener('ra:open-model-settings', onOpenEvent);
    };
  }, []);

  const handlePlanGraphChanged = useCallback(
    (planGraph: ReturnType<typeof useResearchAssistantChatFlow>['intentContext'] extends never ? never : Parameters<Parameters<typeof useResearchAssistantChatFlow>[0]['onPlanGraphChanged']>[0]) => {
      if (!planGraph) {
        setNodes([]);
        setEdges([]);
        setMobilePane('chat');
        return;
      }

      try {
        const graphLayout = buildGraphLayout(planGraph);
        setNodes(graphLayout.nodes);
        setEdges(graphLayout.edges);
        setMobilePane('graph');
      } catch (error) {
        // A malformed persisted plan must not take down the whole workspace.
        console.error('[plan] failed to build graph layout', error);
        setNodes([]);
        setEdges([]);
      }
    },
    [setEdges, setNodes],
  );

  const chatFlow = useResearchAssistantChatFlow({
    onPlanGraphChanged: handlePlanGraphChanged,
    onPlanReady: (planGraph, planReadyOptions) => {
      // Auto-run is opt-in and still limited to the one plan shape that has a
      // deterministic plot step; approval-gated plans always wait for a human.
      const hasPlotNode = planGraph.nodes.some((node) => node.type === 'render_plot');
      const shouldAutoRun =
        planReadyOptions?.autoRun === true &&
        planGraph.intent_type === 'Code_Execution' &&
        !planGraph.approval?.required &&
        !planGraph.review?.required &&
        hasPlotNode;
      if (shouldAutoRun) {
        runtimeActionsRef.current?.(planGraph.id, 'auto');
      }
    },
  });

  const budget = useWorkbenchBudget(chatFlow.activePlanId);

  const runtime = useResearchAssistantRuntime({
    nodes,
    setNodes,
    appendChatMessage: chatFlow.appendChatMessage,
    identity: chatFlow.requestIdentity,
    refreshPlanState: chatFlow.refreshActivePlan,
    activePlanGraph: chatFlow.activePlanGraph,
  });
  const { resetRuntimeState } = runtime;
  const runAllTasks = runtime.handleRunAllTasks;

  useEffect(() => {
    runtimeActionsRef.current = (planId, trigger = 'manual') => {
      void runAllTasks(planId, trigger);
    };
  }, [runAllTasks]);

  const pdfFlow = usePdfAssistFlow({
    setPrompt: chatFlow.setPrompt,
    appendChatMessage: chatFlow.appendChatMessage,
    appendSelectedTaskLog: runtime.appendSelectedTaskLog,
    identity: chatFlow.requestIdentity,
  });

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [runtime.selectedTaskState.logs]);

  useEffect(() => {
    resetRuntimeState();
  }, [chatFlow.activeSessionId, resetRuntimeState]);

  const runtimeContextValue = useMemo<ResearchAssistantRuntimeContextValue>(
    () => ({
      state: {
        executionState: runtime.executionState,
        selectedTaskState: runtime.selectedTaskState,
      },
      actions: {
        onNodeClick: runtime.onNodeClick,
        handleOpenTaskView: runtime.handleOpenTaskView,
        handleExecuteTask: runtime.handleExecuteTask,
        handleRunAllTasks: runtime.handleRunAllTasks,
        handleApproveAndRun: runtime.handleApproveAndRun,
        handleRuntimeActionDecision: runtime.handleRuntimeActionDecision,
        handleCancelPlan: runtime.handleCancelPlan,
        handleRetryFailedPlan: runtime.handleRetryFailedPlan,
        setDisplayMode: runtime.setDisplayMode,
        closeTaskPanel: runtime.closeTaskPanel,
        resetRuntimeState: runtime.resetRuntimeState,
      },
      meta: {
        appendSelectedTaskLog: runtime.appendSelectedTaskLog,
      },
    }),
    [runtime],
  );

  return (
    <AutoRunOnCreateContext.Provider value={chatFlow.autoRunControl}>
      <ResearchAssistantRuntimeProvider value={runtimeContextValue}>
        <ResearchAssistantWorkspaceContent
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          logsEndRef={logsEndRef}
          showModelSettings={showModelSettings}
          onOpenModelSettings={openModelSettings}
          onCloseModelSettings={closeModelSettings}
          layout={layout}
          budget={budget}
          pendingWorkCounts={pendingWorkCounts}
          chatFlow={{
            chatHistory: chatFlow.chatHistory,
            appendChatMessage: chatFlow.appendChatMessage,
            loading: chatFlow.loading,
            prompt: chatFlow.prompt,
            setPrompt: chatFlow.setPrompt,
            handleSendMessage: chatFlow.handleSendMessage,
            pendingAttachments: chatFlow.pendingAttachments,
            uploadingAttachments: chatFlow.uploadingAttachments,
            attachmentError: chatFlow.attachmentError,
            handleAttachFiles: chatFlow.handleAttachFiles,
            handleRemoveAttachment: chatFlow.handleRemoveAttachment,
            attachUploadToSession: chatFlow.attachUploadToSession,
            intentContext: chatFlow.intentContext,
            activePlanId: chatFlow.activePlanId,
            activePlanStatus: chatFlow.activePlanStatus,
            activePlanOutcome: chatFlow.activePlanGraph?.outcome ?? null,
            activePlanMissingRequirements: chatFlow.activePlanGraph?.missing_requirements ?? [],
            activePlanRuntimeActions: chatFlow.activePlanGraph?.runtime_actions ?? [],
            activePlanQuestions: chatFlow.activePlanGraph?.questions ?? [],
            activePlanReviewStatus: chatFlow.activePlanGraph?.review?.status ?? null,
            activePlanReviewRequired: chatFlow.activePlanGraph?.review?.required ?? false,
            activePlanRuntimePolicyMode: chatFlow.activePlanGraph?.runtime_policy?.mode ?? null,
            activePlanPlannerSource: chatFlow.activePlanGraph?.meta?.planner_source ?? null,
            activePlanPlannerFallbackReason: chatFlow.activePlanGraph?.meta?.planner_fallback_reason ?? null,
            activePlanComplexity: chatFlow.activePlanGraph?.meta?.complexity ?? null,
            activePlanPlanningRoute: chatFlow.activePlanGraph?.meta?.planning_route ?? null,
            activePlanTemplateVariant: chatFlow.activePlanGraph?.meta?.template_variant ?? null,
            activePlanRunTrigger: readPlanRunTrigger(chatFlow.activePlanGraph),
            activePlanVerification: chatFlow.activePlanGraph?.verification ?? null,
            activePlanUsage: chatFlow.activePlanGraph?.usage ?? null,
            runtimePermissionMode: chatFlow.runtimePermissionMode,
            showFullAccessConfirm: chatFlow.showFullAccessConfirm,
            handleRuntimePermissionModeChange: chatFlow.handleRuntimePermissionModeChange,
            handleConfirmFullAccess: chatFlow.handleConfirmFullAccess,
            handleCancelFullAccess: chatFlow.handleCancelFullAccess,
            isLoggedIn: chatFlow.isLoggedIn,
            userId: chatFlow.userId,
            loginInput: chatFlow.loginInput,
            setLoginInput: chatFlow.setLoginInput,
            activeSessionId: chatFlow.activeSessionId,
            sessionSummaries: chatFlow.sessionSummaries,
            handleLogin: chatFlow.handleLogin,
            handleCreateSession: chatFlow.handleCreateSession,
            handleSwitchSession: chatFlow.handleSwitchSession,
            handleOpenServerPlan: chatFlow.handleOpenServerPlan,
          }}
            pdfFlow={pdfFlow}
            mobilePane={mobilePane}
            onMobilePaneChange={setMobilePane}
            workspaceView={workspaceView}
            onWorkspaceViewChange={setWorkspaceView}
            onOpenHelp={openHelp}
            onOpenPlanLibrary={() => setShowPlanLibrary(true)}
            onOpenCostConsole={() => setShowCostConsole(true)}
            onOpenReview={() => setShowReview(true)}
            onOpenInbox={() => setShowInbox(true)}
            onAnswerQuestion={async (questionId, answer) => {
              const planId = chatFlow.activePlanId;
              if (!planId) return;
              await answerQuestion(planId, questionId, answer);
              await chatFlow.refreshActivePlan();
            }}
          />
        {showHelp ? <HelpDrawer onClose={() => setShowHelp(false)} initialSection={helpSection} /> : null}
        {showPlanLibrary && (
          <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-6" role="dialog" aria-modal="true" aria-label="计划库">
            <div className="w-full max-w-[100rem] overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <PlanLibraryPanel
                onClose={() => setShowPlanLibrary(false)}
                onOpenPlan={(planId) => {
                  setShowPlanLibrary(false);
                  void chatFlow.handleOpenServerPlan(planId);
                }}
              />
            </div>
          </div>
        )}
        {showCostConsole && (
          <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-8" role="dialog" aria-modal="true" aria-label="成本控制台">
            <div className="w-full max-w-6xl overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <CostConsolePanel onClose={() => setShowCostConsole(false)} />
            </div>
          </div>
        )}
        {showReview && chatFlow.activePlanId && (
          <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-6" role="dialog" aria-modal="true" aria-label="计划审阅">
            <div className="h-full w-full max-w-[100rem] overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <PlanReviewPanel
                planId={chatFlow.activePlanId}
                onClose={() => setShowReview(false)}
                onApplied={() => {
                  void chatFlow.refreshActivePlan();
                  setShowReview(false);
                }}
              />
            </div>
          </div>
        )}
        {showInbox && (
          <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-6" role="dialog" aria-modal="true" aria-label="待办中心">
            <div className="h-full w-full max-w-4xl overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <ApprovalInboxPanel
                onClose={() => setShowInbox(false)}
                onOpenPlan={(planId) => {
                  setShowInbox(false);
                  void chatFlow.handleOpenServerPlan(planId);
                }}
              />
            </div>
          </div>
        )}
      </ResearchAssistantRuntimeProvider>
    </AutoRunOnCreateContext.Provider>
  );
}