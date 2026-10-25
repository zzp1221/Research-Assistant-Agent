import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { GitBranch, MessageSquareText } from 'lucide-react';

import { LeftWorkspaceChat, LeftWorkspacePdf } from './components/LeftWorkspace';
import { useResearchAssistantRuntimeContext, type ResearchAssistantRuntimeContextValue } from './context/ResearchAssistantRuntimeContext';
import { ResearchAssistantRuntimeProvider } from './context/ResearchAssistantRuntimeProvider';
import { usePdfAssistFlow } from './hooks/usePdfAssistFlow';
import { useResearchAssistantChatFlow } from './hooks/useResearchAssistantChatFlow';
import { useGraphState } from './hooks/useGraphState';
import { useResearchAssistantLayoutState } from './hooks/useResearchAssistantLayoutState';
import { useResearchAssistantRuntime } from './hooks/useResearchAssistantRuntime';
import { useGraphExecutionViewModel } from './viewModels/useGraphExecutionViewModel';
import { ExecutionSidebar } from '../features/execution/ExecutionSidebar';
import { GraphPanel } from '../features/plan-graph/GraphPanel';
import { buildGraphLayout } from '../features/plan-graph/buildGraphLayout';
import type { IntentContext, MissingRequirement, RuntimeAction } from '../contracts/api';
import { isPdfAttachment } from '../features/pdf-viewer/pdfAttachment';

interface ResearchAssistantAppShellProps {
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  leftWorkspace: ReactNode;
  graphExecutionViewModel: ReturnType<typeof useGraphExecutionViewModel>;
  mobilePane: 'chat' | 'graph';
  onMobilePaneChange: (pane: 'chat' | 'graph') => void;
}

function ResearchAssistantAppShell(props: ResearchAssistantAppShellProps) {
  const { layout, leftWorkspace, graphExecutionViewModel, mobilePane, onMobilePaneChange } = props;
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

  return (
    <div
      className="research-assistant-app-shell flex h-[100dvh] overflow-hidden bg-paper font-sans text-ink"
      data-mobile-pane={mobilePane}
      data-resizing={isResizing ? 'true' : 'false'}
    >
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
          流程{graphNodeCount > 0 ? ` ${graphNodeCount}` : ''}
        </button>
      </div>

      {leftWorkspace}

      <div
        className="research-assistant-left-resize-handle group relative flex w-2 shrink-0 cursor-col-resize items-center justify-center bg-paper transition-colors hover:bg-accent/10"
        onPointerDown={(event) => {
          event.preventDefault();
          layout.startResizingLeftPanel();
        }}
        onDoubleClick={layout.resetLeftPanel}
        title="拖动调整对话区宽度 · 双击恢复默认"
        role="separator"
        aria-orientation="vertical"
        aria-label="调整对话区宽度"
      >
        <div className={`h-10 w-px rounded-full transition-colors ${layout.isResizing ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent/70'}`} />
      </div>

      <div className="research-assistant-graph-workspace relative flex min-w-0 flex-1 overflow-hidden bg-paper">
        <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

        {graphExecutionViewModel.showExecutionResizeHandle && (
          <div
            className="group relative z-20 flex w-2 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-accent/10"
            onPointerDown={(event) => {
              event.preventDefault();
              layout.startResizingSidebar();
            }}
            onDoubleClick={layout.resetSidebar}
            title="拖动调整详情区宽度 · 双击恢复默认"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整节点详情宽度"
          >
            <div className={`h-10 w-px rounded-full transition-colors ${layout.isResizingSidebar ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent/70'}`} />
          </div>
        )}

        {graphExecutionViewModel.executionSidebarProps && <ExecutionSidebar {...graphExecutionViewModel.executionSidebarProps} />}
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
    intentContext: IntentContext | null;
    activePlanId: string | null;
    activePlanStatus: string | null;
    activePlanOutcome: string | null;
    activePlanMissingRequirements: MissingRequirement[];
    activePlanRuntimeActions: RuntimeAction[];
    activePlanRuntimePolicyMode: string | null;
    activePlanPlannerSource: string | null;
    activePlanPlannerFallbackReason: string | null;
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
}

function ResearchAssistantWorkspaceContent(props: ResearchAssistantWorkspaceContentProps) {
  const { nodes, edges, onNodesChange, onEdgesChange, logsEndRef, layout, chatFlow, pdfFlow, mobilePane, onMobilePaneChange } = props;
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
    />
  );

  return (
    <ResearchAssistantAppShell
      layout={layout}
      leftWorkspace={leftWorkspace}
      graphExecutionViewModel={graphExecutionViewModel}
      mobilePane={mobilePane}
      onMobilePaneChange={onMobilePaneChange}
    />
  );
}

export default function ResearchAssistantApp() {
  const { nodes, setNodes, onNodesChange, edges, setEdges, onEdgesChange } = useGraphState();
  const [mobilePane, setMobilePane] = useState<'chat' | 'graph'>('chat');
  const logsEndRef = useRef<HTMLDivElement>(null);
  const runtimeActionsRef = useRef<((planId: string) => void) | null>(null);
  const layout = useResearchAssistantLayoutState();

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
    onPlanReady: (planGraph) => {
      const hasPlotNode = planGraph.nodes.some((node) => node.type === 'render_plot');
      if (planGraph.intent_type === 'Code_Execution' && !planGraph.approval?.required && hasPlotNode) {
        runtimeActionsRef.current?.(planGraph.id);
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
    runtimeActionsRef.current = (planId) => {
      void runAllTasks(planId);
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
    <ResearchAssistantRuntimeProvider value={runtimeContextValue}>
      <ResearchAssistantWorkspaceContent
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        logsEndRef={logsEndRef}
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
          intentContext: chatFlow.intentContext,
          activePlanId: chatFlow.activePlanId,
          activePlanStatus: chatFlow.activePlanStatus,
          activePlanOutcome: chatFlow.activePlanGraph?.outcome ?? null,
          activePlanMissingRequirements: chatFlow.activePlanGraph?.missing_requirements ?? [],
          activePlanRuntimeActions: chatFlow.activePlanGraph?.runtime_actions ?? [],
          activePlanRuntimePolicyMode: chatFlow.activePlanGraph?.runtime_policy?.mode ?? null,
          activePlanPlannerSource: chatFlow.activePlanGraph?.meta?.planner_source ?? null,
          activePlanPlannerFallbackReason: chatFlow.activePlanGraph?.meta?.planner_fallback_reason ?? null,
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
      />
    </ResearchAssistantRuntimeProvider>
  );
}
