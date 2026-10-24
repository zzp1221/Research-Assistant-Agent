import { useMemo } from 'react';
import type { RefObject } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import type { IntentContext, MissingRequirement, RuntimeAction } from '../../contracts/api';
import { useResearchAssistantRuntimeContext } from '../context/ResearchAssistantRuntimeContext';
import { uiText } from '../constants/uiText';
import type { useResearchAssistantLayoutState } from '../hooks/useResearchAssistantLayoutState';

interface UseGraphExecutionViewModelOptions {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  intentContext: IntentContext | null;
  activePlanId: string | null;
	activePlanStatus: string | null;
  activePlanOutcome?: string | null;
  activePlanMissingRequirements?: MissingRequirement[];
  activePlanRuntimeActions?: RuntimeAction[];
  activePlanRuntimePolicyMode?: string | null;
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  logsEndRef: RefObject<HTMLDivElement | null>;
}

export function useGraphExecutionViewModel(options: UseGraphExecutionViewModelOptions) {
	const { nodes, edges, onNodesChange, onEdgesChange, intentContext, activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanRuntimePolicyMode, layout, logsEndRef } = options;
  const runtime = useResearchAssistantRuntimeContext();

  return useMemo(() => {
    const graphPanelProps = {
      nodes,
      edges,
      onNodesChange,
      onEdgesChange,
      onNodeClick: runtime.actions.onNodeClick,
      intentContext,
      outcome: activePlanOutcome ?? null,
      missingRequirements: activePlanMissingRequirements ?? [],
      runtimeActions: activePlanRuntimeActions ?? [],
      runtimePermissionMode: activePlanRuntimePolicyMode ?? null,
      onRuntimeActionDecision: (actionId: string, decision: 'approve' | 'reject') =>
        void runtime.actions.handleRuntimeActionDecision(activePlanId, actionId, decision),
      runAllText: uiText.runAll,
      graphTitle: uiText.graphTitle,
      graphHint: uiText.graphHint,
      isExecuting: runtime.state.executionState.isExecuting,
	  requiresApproval: activePlanStatus === 'awaiting_approval' && !runtime.state.executionState.approvalResolved,
      onRunAll: () => void runtime.actions.handleRunAllTasks(activePlanId),
	  onApproveAndRun: () => void runtime.actions.handleApproveAndRun(activePlanId),
	  onCancel: () => void runtime.actions.handleCancelPlan(activePlanId),
	  onRetryFailed: () => void runtime.actions.handleRetryFailedPlan(activePlanId),
    };

    const isExpandedMode = runtime.state.executionState.displayMode.endsWith('-expanded');
    const showExecutionResizeHandle =
      Boolean(runtime.state.executionState.selectedTask) && !isExpandedMode;

    const executionSidebarProps = runtime.state.executionState.selectedTask
      ? {
          selectedTask: runtime.state.executionState.selectedTask,
          width: isExpandedMode ? '100%' : `${layout.sidebarWidth}px`,
          isExecuting: runtime.state.executionState.isExecuting,
          requiresApproval: activePlanStatus === 'awaiting_approval' && !runtime.state.executionState.approvalResolved,
          displayMode: runtime.state.executionState.displayMode,
          executionLogs: runtime.state.selectedTaskState.logs,
          executionResult: runtime.state.selectedTaskState.result,
          executionCode: runtime.state.selectedTaskState.code,
          executionStructuredData: runtime.state.selectedTaskState.structuredData,
          executionImage: runtime.state.selectedTaskState.imageBase64 || '',
          logsEndRef,
          onClose: runtime.actions.closeTaskPanel,
          onExecute: () =>
            void runtime.actions.handleExecuteTask(
              runtime.state.executionState.selectedTask as NonNullable<typeof runtime.state.executionState.selectedTask>,
              activePlanId,
            ),
          onChangeDisplayMode: runtime.actions.setDisplayMode,
        }
      : null;

    return {
      graphPanelProps,
      showExecutionResizeHandle,
      executionSidebarProps,
    };
	}, [activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanRuntimePolicyMode, edges, intentContext, layout.sidebarWidth, logsEndRef, nodes, onEdgesChange, onNodesChange, runtime]);
}
