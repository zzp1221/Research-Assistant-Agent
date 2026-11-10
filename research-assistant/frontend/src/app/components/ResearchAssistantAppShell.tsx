/* eslint-disable jsx-a11y/no-interactive-element-to-noninteractive-role -- ARIA window splitters are focusable separators with arrow-key controls. */
import { type CSSProperties, type ReactNode, useEffect } from 'react';
import { GitBranch, MessageSquareText, PanelLeftClose, PanelLeftOpen, BookOpen } from 'lucide-react';
import type { useResearchAssistantLayoutState } from '../hooks/useResearchAssistantLayoutState';
import type { useGraphExecutionViewModel } from '../viewModels/useGraphExecutionViewModel';
import type { WorkbenchBudgetState } from '../hooks/useWorkbenchBudget';
import { ExecutionSidebar } from '../../features/execution/ExecutionSidebar';
import { GraphPanel } from '../../features/plan-graph/GraphPanel';
import { DesktopStatusBanner } from '../../components/DesktopStatusBanner';
import { NetworkIsolationBanner } from '../../components/NetworkIsolationBanner';
import { UpdateAvailableBanner } from '../../components/UpdateAvailableBanner';
import { WorkbenchRail } from '../../features/workbench/WorkbenchRail';
import { WorkbenchStatusBar } from '../../features/workbench/WorkbenchStatusBar';
import { WorkbenchDialog } from './WorkbenchDialog';

interface ResearchAssistantAppShellProps {
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  leftWorkspace: ReactNode;
  readerWorkspace?: ReactNode;
  mainContentView: 'graph' | 'pdf';
  onMainContentViewChange: (view: 'graph' | 'pdf') => void;
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

export function ResearchAssistantAppShell(props: ResearchAssistantAppShellProps) {
  const { layout, leftWorkspace, readerWorkspace, mainContentView, onMainContentViewChange, graphExecutionViewModel, mobilePane, onMobilePaneChange, workspaceView, onWorkspaceViewChange, accountLabel, budget, pendingWorkCount, pendingApprovalCount, pendingQuestionCount, activePlanStatus, reviewRequired, reviewStatus, onOpenModelSettings, onOpenPlanLibrary, onOpenCostConsole, onOpenReview, onOpenInbox, onOpenHelp } = props;
  const { containerRef: attachContainer, ...sizing } = layout;
  const graphNodeCount = graphExecutionViewModel.graphPanelProps.nodes.length;
  const isResizing = sizing.isResizing || sizing.isResizingSidebar;
  const singlePane = sizing.layoutMode === 'single' || sizing.layoutMode === 'mobile';
  const showChat = singlePane ? mobilePane === 'chat' : !sizing.chatCollapsed;
  const showMain = !singlePane || mobilePane === 'graph';
  const sidebar = graphExecutionViewModel.executionSidebarProps;
  const detailsOpen = Boolean(sidebar) && mainContentView === 'graph';
  const setDetailsOpen = sizing.setDetailsOpen;

  useEffect(() => { setDetailsOpen(detailsOpen); }, [detailsOpen, setDetailsOpen]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (document.querySelector('dialog[open], [role="dialog"][aria-modal="true"]')) return;
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
      data-layout-mode={sizing.layoutMode}
      data-chat-collapsed={sizing.chatCollapsed ? 'true' : 'false'}
      data-details-mode={sizing.detailsMode}
    >
      <WorkbenchRail
        activeView={workspaceView}
        planId={graphProps.planId ?? null}
        pendingWorkCount={pendingWorkCount}
        reviewPending={reviewRequired && reviewStatus !== 'approved'}
        accountLabel={accountLabel}
        onSelectView={(view) => {
          onWorkspaceViewChange(view);
          onMobilePaneChange('chat');
          if (sizing.chatCollapsed) sizing.toggleChat();
        }}
        onOpenPlanLibrary={onOpenPlanLibrary ?? (() => undefined)}
        onOpenCostConsole={onOpenCostConsole ?? (() => undefined)}
        onOpenInbox={onOpenInbox ?? (() => undefined)}
        onOpenReview={onOpenReview ?? (() => undefined)}
        onOpenHelp={onOpenHelp}
        onOpenModelSettings={onOpenModelSettings}
      />
      <div className="research-assistant-workbench-stack">
        <DesktopStatusBanner />
        <UpdateAvailableBanner />
        <NetworkIsolationBanner />
        <div className="research-assistant-workbench-row" ref={attachContainer} style={{ '--chat-width': `${sizing.leftPanelPixels}px` } as CSSProperties}>
          <div className="research-assistant-pane-switcher research-assistant-mobile-tabbar" hidden={!singlePane}>
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
              {mainContentView === 'pdf' ? <BookOpen className="h-4 w-4" /> : <GitBranch className="h-4 w-4" />}
              {mainContentView === 'pdf' ? '论文' : `执行图${graphNodeCount > 0 ? ` ${graphNodeCount}` : ''}`}
            </button>
          </div>

          <div className="research-assistant-chat-pane" hidden={!showChat} inert={!showChat}>
            {leftWorkspace}
          </div>

          <button
            type="button"
            className="research-assistant-left-resize-handle group relative flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-line"
            onPointerDown={(event) => {
              event.preventDefault();
              sizing.startResizingLeftPanel();
            }}
            onDoubleClick={sizing.resetLeftPanel}
            title="拖动调整对话栏宽度 · 双击恢复默认"
            role="separator"
            tabIndex={0}
            hidden={!showChat || singlePane}
            aria-orientation="vertical"
            aria-label="调整对话栏宽度"
            aria-valuemin={320}
            aria-valuemax={sizing.leftPanelMax}
            aria-valuenow={Math.round(sizing.leftPanelPixels)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                event.preventDefault();
                sizing.adjustLeftPanel(event.key === 'ArrowRight' ? 24 : -24);
              } else if (event.key === 'Home') { event.preventDefault(); sizing.resetLeftPanel(); }
            }}
          >
            <div className={`h-12 w-px rounded-full transition-colors ${sizing.isResizing ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent'}`} />
          </button>

          <div className="research-assistant-main-content" hidden={!showMain} inert={!showMain}>
            <div className="research-assistant-view-toolbar">
              {!singlePane && (
                <button type="button" onClick={sizing.toggleChat} aria-expanded={!sizing.chatCollapsed}>
                  {sizing.chatCollapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
                  {sizing.chatCollapsed ? '展开对话' : '收起对话'}
                </button>
              )}
              {readerWorkspace && <>
                <button type="button" aria-pressed={mainContentView === 'graph'} onClick={() => onMainContentViewChange('graph')}><GitBranch size={16} />执行图</button>
                <button type="button" aria-pressed={mainContentView === 'pdf'} onClick={() => onMainContentViewChange('pdf')}><BookOpen size={16} />论文</button>
                {mainContentView === 'pdf' && <span className="research-assistant-reading-status" role="status">{activePlanStatus === 'in_progress' ? '计划运行中' : '研究阅读'}{pendingWorkCount > 0 ? ` · ${pendingWorkCount} 项待办` : ''}</span>}
              </>}
            </div>
            <div className="research-assistant-graph-workspace relative flex min-w-0 flex-1 overflow-hidden" hidden={mainContentView !== 'graph'} inert={mainContentView !== 'graph'}>
              <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

            {graphExecutionViewModel.showExecutionResizeHandle && sizing.detailsMode === 'docked' && (
              <button
                type="button"
                className="group relative z-20 flex w-1.5 shrink-0 cursor-col-resize items-center justify-center bg-surface transition-colors hover:bg-surface-muted"
                onPointerDown={(event) => {
                  event.preventDefault();
                  sizing.startResizingSidebar();
                }}
                onDoubleClick={sizing.resetSidebar}
                title="拖动调整详情栏宽度 · 双击恢复默认"
                role="separator"
                tabIndex={0}
                aria-orientation="vertical"
                aria-label="调整详情栏宽度"
                aria-valuemin={360}
                aria-valuemax={sizing.sidebarMax}
                aria-valuenow={Math.round(sizing.sidebarWidth)}
                onKeyDown={(event) => {
                  if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                    event.preventDefault();
                    sizing.adjustSidebar(event.key === 'ArrowLeft' ? 24 : -24);
                  } else if (event.key === 'Home') { event.preventDefault(); sizing.resetSidebar(); }
                }}
              >
                <div className={`h-12 w-px rounded-full transition-colors ${sizing.isResizingSidebar ? 'bg-accent' : 'bg-line-strong group-hover:bg-accent'}`} />
              </button>
            )}

              {sidebar && sizing.detailsMode === 'docked' && <ExecutionSidebar {...sidebar} />}
            </div>
            <div className="research-assistant-reader-workspace" hidden={mainContentView !== 'pdf'} inert={mainContentView !== 'pdf'}>{readerWorkspace}</div>
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
      {sidebar && detailsOpen && sizing.detailsMode !== 'docked' && (
        <WorkbenchDialog label="节点详情" onRequestClose={sidebar.onClose} className={`execution-details-overlay is-${sizing.detailsMode}`}>
          <ExecutionSidebar {...sidebar} width="100%" />
        </WorkbenchDialog>
      )}
    </div>
  );
}
