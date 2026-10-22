import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useEdgesState, useNodesState } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { GitBranch, MessageSquareText } from 'lucide-react';
import '@xyflow/react/dist/style.css';
import 'katex/dist/katex.min.css';
import '@react-pdf-viewer/core/lib/styles/index.css';
import '@react-pdf-viewer/default-layout/lib/styles/index.css';

import { LeftWorkspaceChat, LeftWorkspacePdf } from './components/LeftWorkspace';
import { useResearchAssistantRuntimeContext, type ResearchAssistantRuntimeContextValue } from './context/ResearchAssistantRuntimeContext';
import { ResearchAssistantRuntimeProvider } from './context/ResearchAssistantRuntimeProvider';
import { usePdfAssistFlow } from './hooks/usePdfAssistFlow';
import { useResearchAssistantChatFlow } from './hooks/useResearchAssistantChatFlow';
import { useResearchAssistantLayoutState } from './hooks/useResearchAssistantLayoutState';
import { useResearchAssistantRuntime } from './hooks/useResearchAssistantRuntime';
import { useGraphExecutionViewModel } from './viewModels/useGraphExecutionViewModel';
import { ExecutionSidebar } from '../features/execution/ExecutionSidebar';
import { GraphPanel } from '../features/plan-graph/GraphPanel';
import { buildGraphLayout } from '../features/plan-graph/buildGraphLayout';
import type { IntentContext } from '../contracts/api';
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

  return (
    <div
      className="research-assistant-app-shell flex h-screen overflow-hidden bg-gray-100 font-sans"
      data-mobile-pane={mobilePane}
    >
      <div className="research-assistant-mobile-tabbar">
        <button
          type="button"
          onClick={() => onMobilePaneChange('chat')}
          className={mobilePane === 'chat' ? 'is-active' : ''}
        >
          <MessageSquareText className="h-4 w-4" />
          对话
        </button>
        <button
          type="button"
          onClick={() => onMobilePaneChange('graph')}
          className={mobilePane === 'graph' ? 'is-active' : ''}
        >
          <GitBranch className="h-4 w-4" />
          流程{graphNodeCount > 0 ? ` ${graphNodeCount}` : ''}
        </button>
      </div>

      {leftWorkspace}

      <div
        className={`research-assistant-left-resize-handle flex w-1.5 cursor-col-resize items-center justify-center bg-gray-200 transition-colors hover:bg-blue-400 ${layout.isResizing ? 'bg-blue-500' : ''}`}
        onMouseDown={layout.startResizingLeftPanel}
      >
        <div className="h-8 w-1 bg-gray-400 rounded-full" />
      </div>

      <div className="research-assistant-graph-workspace relative flex min-w-0 flex-1 overflow-hidden">
        <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

        {graphExecutionViewModel.showExecutionResizeHandle && (
          <div
            className={`w-1 bg-gray-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${layout.isResizingSidebar ? 'bg-blue-500' : ''}`}
            onMouseDown={layout.startResizingSidebar}
          >
            <div className="h-8 w-0.5 bg-gray-400 rounded-full" />
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
    layout,
    logsEndRef,
  });

  const leftWorkspace = pdfFlow.pdfUrl ? (
    <LeftWorkspacePdf
      widthPercent={layout.leftPanelWidth}
      pdfUrl={pdfFlow.pdfUrl}
      documentInfo={pdfFlow.documentInfo}
      onClosePdf={pdfFlow.closePdf}
      defaultLayoutPluginInstance={pdfFlow.defaultLayoutPluginInstance}
      aiTranslationPluginInstance={pdfFlow.aiTranslationPluginInstance}
      translation={pdfFlow.translation}
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
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [mobilePane, setMobilePane] = useState<'chat' | 'graph'>('chat');
  const logsEndRef = useRef<HTMLDivElement>(null);
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
