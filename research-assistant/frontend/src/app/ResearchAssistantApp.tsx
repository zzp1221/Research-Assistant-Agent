import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { BookOpen, GitBranch, MessageSquareText } from 'lucide-react';

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
import { ModelSettingsPanel } from '../features/settings/ModelSettingsPanel';
import type { IntentContext, MissingRequirement, RuntimeAction, UploadedFile } from '../contracts/api';
import type { PlanExecutionTrigger } from '../services/api/researchAssistantApi';
import { isPdfAttachment } from '../features/pdf-viewer/pdfAttachment';

interface JournalMastheadProps {
  planTitle: string | null;
  planId: string | null;
  stepCount: number;
  completedCount: number;
  pendingCount: number;
  isExecuting: boolean;
  permissionMode: string | null;
  account: string;
  onOpenModelSettings?: () => void;
  onOpenHelp: () => void;
}

/**
 * The spread carries a masthead, not a navigation bar: the workbench is a
 * manuscript under review, and every run is one issue of the same journal.
 */
function JournalMasthead(props: JournalMastheadProps) {
  const { planTitle, planId, stepCount, completedCount, pendingCount, isExecuting, permissionMode, account, onOpenModelSettings, onOpenHelp } = props;
  const finished = stepCount > 0 && completedCount === stepCount && !isExecuting;
  const statusLabel = isExecuting ? '运行中' : stepCount === 0 ? '未创建计划' : finished ? '已完成' : '计划已就绪';
  const statusDot = isExecuting ? 'proof-pulse bg-proof' : finished ? 'bg-ok' : stepCount > 0 ? 'bg-ink-soft' : 'bg-line-strong';
  return (
    <>
      <header className="research-assistant-masthead masthead-rule flex h-14 shrink-0 items-center gap-4 bg-surface px-5">
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center border border-ink bg-ink font-serif text-[15px] font-semibold text-paper">R</div>
          <div className="min-w-0 leading-4">
            <div className="truncate font-serif text-[15px] font-semibold text-ink">Research Assistant</div>
            <div className="journal-caps text-[9px]">研究工作台</div>
          </div>
        </div>
        <div className="flex min-w-0 flex-1 items-baseline justify-center gap-3">
          <h1 className="truncate font-serif text-[20px] font-semibold leading-6 tracking-[-0.01em] text-ink">研究执行台</h1>
          <span className="journal-caps hidden text-[9px] text-muted sm:inline">文献 · 阅读 · 计划 · 执行</span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className="hidden max-w-[13rem] truncate journal-num text-[10px] text-muted lg:inline">
            {planId ? `计划 ${planId.slice(0, 8).toUpperCase()}` : '尚无计划'} · {account}
          </span>
          <button
            type="button"
            onClick={onOpenHelp}
            aria-label="打开使用手册"
            className="inline-flex h-7 items-center gap-1 border border-line-strong bg-surface px-2 text-[11px] text-ink-soft transition-colors hover:border-ink hover:text-ink"
          >
            <BookOpen className="h-3 w-3" /> 使用手册
          </button>
          {onOpenModelSettings && (
            <button
              type="button"
              onClick={onOpenModelSettings}
              aria-label="模型设置"
              title="工作台设置 · 模型"
              className="inline-flex h-7 items-center gap-1 border border-line-strong bg-surface px-2 text-[11px] text-ink-soft transition-colors hover:border-ink hover:text-ink"
            >
              模型设置
            </button>
          )}
        </div>
      </header>
      <div className="flex h-9 shrink-0 items-center gap-5 border-b border-line bg-surface px-5">
        <span className="journal-caps shrink-0">执行状态</span>
        <div className="flex min-w-0 flex-1 items-center justify-center gap-4 journal-num text-[10.5px] text-ink-soft">
          <span>{stepCount} 个节点</span>
          <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
          <span>{completedCount} 已完成</span>
          {pendingCount > 0 && (
            <>
              <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
              <span className="text-proof">{pendingCount} 项待批准</span>
            </>
          )}
          {planTitle && <span className="max-w-[20rem] truncate text-muted" title={planTitle}>{planTitle}</span>}
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <span className="flex items-center gap-1.5 journal-caps">
            <span className={`h-1.5 w-1.5 rounded-full ${statusDot}`} aria-hidden="true" />
            {statusLabel}
          </span>
          <span className="journal-caps border-l border-line pl-3">{permissionMode === 'full_access' ? '完全权限' : '逐项确认'}</span>
        </div>
      </div>
    </>
  );
}

interface ResearchAssistantAppShellProps {
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  leftWorkspace: ReactNode;
  graphExecutionViewModel: ReturnType<typeof useGraphExecutionViewModel>;
  mobilePane: 'chat' | 'graph';
  onMobilePaneChange: (pane: 'chat' | 'graph') => void;
  accountLabel: string;
  showModelSettings?: boolean;
  onOpenModelSettings?: () => void;
  onOpenHelp: () => void;
}

function ResearchAssistantAppShell(props: ResearchAssistantAppShellProps) {
  const { layout, leftWorkspace, graphExecutionViewModel, mobilePane, onMobilePaneChange, accountLabel, onOpenModelSettings, onOpenHelp } = props;
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
  const pendingActions = (graphProps.runtimeActions ?? []).filter((action) => action.status === 'pending').length;

  return (
    <div
      className="research-assistant-app-shell flex h-[100dvh] flex-col overflow-hidden bg-paper font-sans text-ink"
      data-mobile-pane={mobilePane}
      data-resizing={isResizing ? 'true' : 'false'}
    >
      <JournalMasthead
        planTitle={graphProps.intentContext?.raw_intent ?? null}
        planId={graphProps.planId ? graphProps.planId.slice(0, 8).toUpperCase() : null}
        stepCount={stepCount}
        completedCount={completedCount}
        pendingCount={pendingActions}
        isExecuting={graphProps.isExecuting}
        permissionMode={graphProps.runtimePermissionMode ?? null}
        account={accountLabel}
        onOpenModelSettings={onOpenModelSettings}
        onOpenHelp={onOpenHelp}
      />
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
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
          className="research-assistant-left-resize-handle group relative flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-paper transition-colors hover:bg-line"
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
          <div className={`h-12 w-px transition-colors ${layout.isResizing ? 'bg-proof' : 'bg-line-strong group-hover:bg-ink'}`} />
        </div>

        <div className="research-assistant-graph-workspace relative flex min-w-0 flex-1 overflow-hidden bg-paper">
          <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

          {graphExecutionViewModel.showExecutionResizeHandle && (
            <div
              className="group relative z-20 flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-line"
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
              <div className={`h-12 w-px transition-colors ${layout.isResizingSidebar ? 'bg-proof' : 'bg-line-strong group-hover:bg-ink'}`} />
            </div>
          )}

          {graphExecutionViewModel.executionSidebarProps && <ExecutionSidebar {...graphExecutionViewModel.executionSidebarProps} />}
        </div>
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
    activePlanRuntimePolicyMode: string | null;
    activePlanPlannerSource: string | null;
    activePlanPlannerFallbackReason: string | null;
    activePlanComplexity: string | null;
    activePlanPlanningRoute: string | null;
    activePlanTemplateVariant: string | null;
    activePlanRunTrigger: PlanRunTrigger;
    activePlanVerification: import('../contracts/api').Verification | null;
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
  };
  pdfFlow: ReturnType<typeof usePdfAssistFlow>;
  mobilePane: 'chat' | 'graph';
  onMobilePaneChange: (pane: 'chat' | 'graph') => void;
  onOpenHelp: (section?: HelpSection) => void;
}

function ResearchAssistantWorkspaceContent(props: ResearchAssistantWorkspaceContentProps) {
  const { nodes, edges, onNodesChange, onEdgesChange, logsEndRef, showModelSettings, onOpenModelSettings, onCloseModelSettings, layout, chatFlow, pdfFlow, mobilePane, onMobilePaneChange, onOpenHelp } = props;
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
    activePlanRuntimePolicyMode: chatFlow.activePlanRuntimePolicyMode,
    activePlanPlannerSource: chatFlow.activePlanPlannerSource,
    activePlanPlannerFallbackReason: chatFlow.activePlanPlannerFallbackReason,
    activePlanComplexity: chatFlow.activePlanComplexity,
    activePlanPlanningRoute: chatFlow.activePlanPlanningRoute,
    activePlanTemplateVariant: chatFlow.activePlanTemplateVariant,
    activePlanRunTrigger: chatFlow.activePlanRunTrigger,
    activePlanVerification: chatFlow.activePlanVerification ?? null,
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
        accountLabel={chatFlow.userId ? `账户 ${chatFlow.userId.slice(0, 8)}` : '游客'}
        onOpenModelSettings={onOpenModelSettings}
        onOpenHelp={() => onOpenHelp('start')}
      />
      {showModelSettings && (
        <div className="fixed inset-0 z-50 flex items-stretch justify-center bg-ink/25 p-3 md:p-8">
          <div className="w-full max-w-5xl overflow-hidden border border-ink bg-paper shadow-2xl">
            <ModelSettingsPanel onClose={onCloseModelSettings} />
          </div>
        </div>
      )}
    </>
  );
}

export default function ResearchAssistantApp() {
  const { nodes, setNodes, onNodesChange, edges, setEdges, onEdgesChange } = useGraphState();
  const [mobilePane, setMobilePane] = useState<'chat' | 'graph'>('chat');
  const [showModelSettings, setShowModelSettings] = useState(() =>
    typeof window !== 'undefined' && window.location.hash === '#settings:models',
  );
  const [showHelp, setShowHelp] = useState(false);
  const [helpSection, setHelpSection] = useState<HelpSection>('start');
  const logsEndRef = useRef<HTMLDivElement>(null);
  const runtimeActionsRef = useRef<((planId: string, trigger?: PlanExecutionTrigger) => void) | null>(null);
  const layout = useResearchAssistantLayoutState();

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
        hasPlotNode;
      if (shouldAutoRun) {
        runtimeActionsRef.current?.(planGraph.id, 'auto');
      }
    },
  });

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
            activePlanRuntimePolicyMode: chatFlow.activePlanGraph?.runtime_policy?.mode ?? null,
            activePlanPlannerSource: chatFlow.activePlanGraph?.meta?.planner_source ?? null,
            activePlanPlannerFallbackReason: chatFlow.activePlanGraph?.meta?.planner_fallback_reason ?? null,
            activePlanComplexity: chatFlow.activePlanGraph?.meta?.complexity ?? null,
            activePlanPlanningRoute: chatFlow.activePlanGraph?.meta?.planning_route ?? null,
            activePlanTemplateVariant: chatFlow.activePlanGraph?.meta?.template_variant ?? null,
            activePlanRunTrigger: readPlanRunTrigger(chatFlow.activePlanGraph),
            activePlanVerification: chatFlow.activePlanGraph?.verification ?? null,
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
          }}
            pdfFlow={pdfFlow}
            mobilePane={mobilePane}
            onMobilePaneChange={setMobilePane}
            onOpenHelp={openHelp}
          />
        {showHelp ? <HelpDrawer onClose={() => setShowHelp(false)} initialSection={helpSection} /> : null}
      </ResearchAssistantRuntimeProvider>
    </AutoRunOnCreateContext.Provider>
  );
}