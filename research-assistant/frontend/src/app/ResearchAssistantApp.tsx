import { type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';

import { LeftWorkspaceChat, LeftWorkspacePdf } from './components/LeftWorkspace';
import { ResearchAssistantAppShell } from './components/ResearchAssistantAppShell';
import { WorkbenchDialog } from './components/WorkbenchDialog';
import { useResearchAssistantRuntimeContext, type ResearchAssistantRuntimeContextValue } from './context/ResearchAssistantRuntimeContext';
import { ResearchAssistantRuntimeProvider } from './context/ResearchAssistantRuntimeProvider';
import { usePdfAssistFlow } from './hooks/usePdfAssistFlow';
import { AutoRunOnCreateContext, useResearchAssistantChatFlow } from './hooks/useResearchAssistantChatFlow';
import { useGraphState } from './hooks/useGraphState';
import { useResearchAssistantLayoutState } from './hooks/useResearchAssistantLayoutState';
import { useResearchAssistantRuntime } from './hooks/useResearchAssistantRuntime';
import { readPlanRunTrigger, useGraphExecutionViewModel, type PlanRunTrigger } from './viewModels/useGraphExecutionViewModel';
import { HelpDrawer, type HelpSection } from '../features/help/HelpDrawer';
import { Onboarding } from '../features/onboarding/Onboarding';
import { buildGraphLayout } from '../features/plan-graph/buildGraphLayout';
import { SettingsPanel } from '../features/settings/SettingsPanel';
import { PlanLibraryPanel } from '../features/plan-library/PlanLibraryPanel';
import { CostConsolePanel } from '../features/cost/CostConsolePanel';
import { PlanReviewPanel } from '../features/review/PlanReviewPanel';
import { ApprovalInboxPanel } from '../features/review/ApprovalInboxPanel';
import { useWorkbenchBudget, type WorkbenchBudgetState } from './hooks/useWorkbenchBudget';
import { answerQuestion, listApprovals } from '../services/api/researchAssistantApi';
import { getDesktopBridge } from '../services/desktopBridge';
import type { IntentContext, MissingRequirement, PlanQuestion, RuntimeAction, UploadedFile } from '../contracts/api';
import type { PlanExecutionTrigger } from '../services/api/researchAssistantApi';
import { isPdfAttachment } from '../features/pdf-viewer/pdfAttachment';

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
    documentReferences: ReturnType<typeof useResearchAssistantChatFlow>['documentReferences'];
    removeDocumentReference: ReturnType<typeof useResearchAssistantChatFlow>['removeDocumentReference'];
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
  executionViewRequest: number;
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
  const [readerView, setReaderView] = useState<{ url: string | null; error: string; executionViewRequest: number; view: 'graph' | 'pdf' }>(() => ({
    url: pdfFlow.pdfUrl, error: pdfFlow.documentError, executionViewRequest: props.executionViewRequest, view: pdfFlow.pdfUrl || pdfFlow.documentError ? 'pdf' : 'graph',
  }));
  if (readerView.url !== pdfFlow.pdfUrl || readerView.error !== pdfFlow.documentError || readerView.executionViewRequest !== props.executionViewRequest) {
    setReaderView({ url: pdfFlow.pdfUrl, error: pdfFlow.documentError, executionViewRequest: props.executionViewRequest,
      view: readerView.executionViewRequest !== props.executionViewRequest ? 'graph' : pdfFlow.pdfUrl || pdfFlow.documentError ? 'pdf' : 'graph' });
  }
  const mainContentView = readerView.view;
  const setMainContentView = (view: 'graph' | 'pdf') => setReaderView({ url: pdfFlow.pdfUrl, error: pdfFlow.documentError, executionViewRequest: props.executionViewRequest, view });
  const showChat = () => {
    onWorkspaceViewChange('chat');
    onMobilePaneChange('chat');
    if (layout.chatCollapsed) layout.toggleChat();
  };
  const openPdf = async (upload: UploadedFile) => {
    setMainContentView('pdf');
    onMobilePaneChange('graph');
    await pdfFlow.openPdfFromUpload(upload);
  };
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

  const readerWorkspace = pdfFlow.pdfUrl || pdfFlow.documentError ? (
    <>
    {pdfFlow.documentError && <div role="alert" className="border-b border-line bg-proof-soft p-4 text-sm text-proof">{pdfFlow.documentError}<button type="button" className="ml-3 underline" onClick={() => setMainContentView('graph')}>返回执行图</button></div>}
    {pdfFlow.pdfUrl && (
    <LeftWorkspacePdf
      widthPercent={100}
      pdfUrl={pdfFlow.pdfUrl}
      documentName={pdfFlow.documentName}
      documentKey={pdfFlow.documentKey}
      initialPage={pdfFlow.readingState.page}
      initialScale={pdfFlow.readingState.scale}
      onPageChange={(page) => pdfFlow.setReadingState({ page })}
      onScaleChange={(scale) => pdfFlow.setReadingState({ scale })}
      documentInfo={pdfFlow.documentInfo}
      onClosePdf={pdfFlow.closePdf}
      onReturnToGraph={() => setMainContentView('graph')}
      onShowChat={showChat}
      translation={pdfFlow.translation}
      onAskAI={pdfFlow.handleAskAI}
      onAssist={pdfFlow.assistWithUpload}
      onOpenHelp={() => onOpenHelp('pdf')}
    />
    )}
    </>
  ) : null;
  const leftWorkspace = (
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
        documentReferences: chatFlow.documentReferences,
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
        onOpenAttachment: openPdf,
        onRemoveDocumentReference: chatFlow.removeDocumentReference,
        onOpenDocumentReference: async (reference) => {
          await pdfFlow.restoreReference(reference);
          setMainContentView('pdf');
          onMobilePaneChange('graph');
        },
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
          void openPdf(pdfAttachment);
        },
        onClosePdf: pdfFlow.closePdf,
      }}
      taskActions={{
        onOpenTaskView: (taskId, mode) => {
          setMainContentView('graph');
          onMobilePaneChange('graph');
          runtime.actions.handleOpenTaskView(taskId, mode);
        },
      }}
      literatureActions={{
        activeSessionId: chatFlow.activeSessionId,
        onAttachToSession: chatFlow.attachUploadToSession,
        onDetachFromSession: chatFlow.handleRemoveAttachment,
        onOpenPdf: openPdf,
        onOpenHelp: () => onOpenHelp('library'),
      }}
    />
  );

  return (
    <>
      <ResearchAssistantAppShell
        layout={layout}
        leftWorkspace={leftWorkspace}
        readerWorkspace={readerWorkspace}
        mainContentView={mainContentView}
        onMainContentViewChange={setMainContentView}
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
  const [executionViewRequest, setExecutionViewRequest] = useState(0);
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
  const [showOnboarding, setShowOnboarding] = useState(false);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const runtimeActionsRef = useRef<((planId: string, trigger?: PlanExecutionTrigger) => void) | null>(null);
  const reviewLeaveGuardRef = useRef<((proceed: () => void) => Promise<boolean>) | null>(null);
  const layout = useResearchAssistantLayoutState();
  const registerReviewLeaveGuard = useCallback((guard: ((proceed: () => void) => Promise<boolean>) | null) => {
    reviewLeaveGuardRef.current = guard;
  }, []);
  const leaveReview = useCallback((proceed: () => void) => {
    const continueAfterReview = () => { setShowReview(false); proceed(); };
    if (showReview && reviewLeaveGuardRef.current) {
      void reviewLeaveGuardRef.current(continueAfterReview);
    } else continueAfterReview();
  }, [showReview]);

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

  useEffect(() => {
    let cancelled = false;
    let attempts = 0;
    let timer: number | undefined;
    const check = () => {
      if (cancelled) return;
      const bridge = getDesktopBridge();
      if (!bridge) {
        // Wails injects window.go asynchronously; retry briefly before giving up.
        if (attempts++ < 20) timer = window.setTimeout(check, 100);
        return;
      }
      Promise.resolve(bridge.needsOnboarding())
        .then((required) => {
          if (!cancelled) setShowOnboarding(Boolean(required));
        })
        .catch(() => {
          // A broken bridge must not block the workbench.
        });
    };
    check();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
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

  const displayedPlanRef = useRef<string | null>(null);
  const handlePlanGraphChanged = useCallback(
    (planGraph: ReturnType<typeof useResearchAssistantChatFlow>['intentContext'] extends never ? never : Parameters<Parameters<typeof useResearchAssistantChatFlow>[0]['onPlanGraphChanged']>[0]) => {
      if (!planGraph) {
        displayedPlanRef.current = null;
        setNodes([]);
        setEdges([]);
        setMobilePane('chat');
        return;
      }

      try {
        const graphLayout = buildGraphLayout(planGraph);
        setNodes(graphLayout.nodes);
        setEdges(graphLayout.edges);
        if (displayedPlanRef.current !== planGraph.id) setMobilePane('graph');
        displayedPlanRef.current = planGraph.id;
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
    activeSessionId: chatFlow.activeSessionId,
    onAskReference: (reference) => {
      chatFlow.addDocumentReference(reference);
      setWorkspaceView('chat');
      setMobilePane('chat');
      if (layout.chatCollapsed) layout.toggleChat();
    },
  });

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [runtime.selectedTaskState.logs]);

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
        handleResumePlan: runtime.handleResumePlan,
        handleRetryFailedPlan: runtime.handleRetryFailedPlan,
        setDisplayMode: runtime.setDisplayMode,
        closeTaskPanel: runtime.closeTaskPanel,
        resetRuntimeState: runtime.resetRuntimeState,
        observePlan: runtime.observePlan,
        handleReconnect: runtime.handleReconnect,
      },
      meta: {
        appendSelectedTaskLog: runtime.appendSelectedTaskLog,
      },
    }),
    [runtime],
  );

  if (showOnboarding) {
    return <Onboarding onComplete={() => setShowOnboarding(false)} />;
  }

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
            documentReferences: chatFlow.documentReferences,
            removeDocumentReference: chatFlow.removeDocumentReference,
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
            handleLogin: () => leaveReview(chatFlow.handleLogin),
            handleCreateSession: () => leaveReview(chatFlow.handleCreateSession),
            handleSwitchSession: (sessionId) => leaveReview(() => chatFlow.handleSwitchSession(sessionId)),
            handleOpenServerPlan: async (planId) => leaveReview(() => { void chatFlow.handleOpenServerPlan(planId); }),
          }}
            pdfFlow={pdfFlow}
            mobilePane={mobilePane}
            executionViewRequest={executionViewRequest}
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
                  leaveReview(() => { void chatFlow.handleOpenServerPlan(planId); });
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
          <WorkbenchDialog label="计划审阅" onRequestClose={() => leaveReview(() => undefined)} className="workbench-review-dialog">
            <div className="h-full w-full max-w-[100rem] overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <PlanReviewPanel
                planId={chatFlow.activePlanId}
                identity={chatFlow.requestIdentity}
                onClose={() => setShowReview(false)}
                onRegisterLeaveGuard={registerReviewLeaveGuard}
                onSaved={() => { void chatFlow.refreshActivePlan(); }}
                onExecutionStarted={() => {
                  void chatFlow.refreshActivePlan();
                  setShowReview(false);
                  setMobilePane('graph');
                  setExecutionViewRequest((request) => request + 1);
                  void runtime.observePlan(chatFlow.activePlanId!, { startAccepted: true });
                }}
              />
            </div>
          </WorkbenchDialog>
        )}
        {showInbox && (
          <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/20 p-3 backdrop-blur-[2px] md:p-6" role="dialog" aria-modal="true" aria-label="待办中心">
            <div className="h-full w-full max-w-4xl overflow-hidden rounded-3xl border border-line bg-paper shadow-float">
              <ApprovalInboxPanel
                onClose={() => setShowInbox(false)}
                onOpenPlan={(planId) => {
                  setShowInbox(false);
                  leaveReview(() => { void chatFlow.handleOpenServerPlan(planId); });
                }}
              />
            </div>
          </div>
        )}
      </ResearchAssistantRuntimeProvider>
    </AutoRunOnCreateContext.Provider>
  );
}
