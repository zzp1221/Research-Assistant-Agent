import { useMemo } from 'react';
import type { RefObject } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import type { IntentContext, MissingRequirement, PlanGraph, RuntimeAction } from '../../contracts/api';
import { useResearchAssistantRuntimeContext } from '../context/ResearchAssistantRuntimeContext';
import { uiText } from '../constants/uiText';
import type { useResearchAssistantLayoutState } from '../hooks/useResearchAssistantLayoutState';

export type PlanRunTrigger = 'manual' | 'auto' | '';

// PlanGraph predates run_trigger in the shared contract; narrow the optional
// backend field here so the view model stays the single reader for the colophon.
export const readPlanRunTrigger = (planGraph: PlanGraph | null | undefined): PlanRunTrigger => {
  const raw = planGraph?.run_trigger;
  return raw === 'manual' || raw === 'auto' ? raw : '';
};

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
  activePlanPlannerSource?: string | null;
  activePlanPlannerFallbackReason?: string | null;
  activePlanRunTrigger?: PlanRunTrigger | null;
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  logsEndRef: RefObject<HTMLDivElement | null>;
}

export function useGraphExecutionViewModel(options: UseGraphExecutionViewModelOptions) {
	const { nodes, edges, onNodesChange, onEdgesChange, intentContext, activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanRuntimePolicyMode, activePlanPlannerSource, activePlanPlannerFallbackReason, activePlanRunTrigger, layout, logsEndRef } = options;
  const runtime = useResearchAssistantRuntimeContext();

  return useMemo(() => {
    // A pending runtime action must be decided on its own card. Showing the
    // plan-level "审批并运行" button at the same time caused a guaranteed 409.
    const hasPendingRuntimeAction = (activePlanRuntimeActions ?? []).some((action) => action.status === 'pending');
    const requiresPlanApproval =
      activePlanStatus === 'awaiting_approval' &&
      !hasPendingRuntimeAction &&
      !runtime.state.executionState.approvalResolved;
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
      plannerSource: activePlanPlannerSource ?? null,
      plannerFallbackReason: activePlanPlannerFallbackReason ?? null,
      runTrigger: activePlanRunTrigger ?? '',
      onRuntimeActionDecision: (actionId: string, decision: 'approve' | 'reject') =>
        runtime.actions.handleRuntimeActionDecision(activePlanId, actionId, decision),
      runAllText: uiText.runAll,
      graphTitle: uiText.graphTitle,
      graphHint: uiText.graphHint,
      isExecuting: runtime.state.executionState.isExecuting,
	  requiresApproval: requiresPlanApproval,
      onRunAll: () => void runtime.actions.handleRunAllTasks(activePlanId),
	  onApproveAndRun: () => void runtime.actions.handleApproveAndRun(activePlanId),
	  onCancel: () => void runtime.actions.handleCancelPlan(activePlanId),
	  onRetryFailed: () => void runtime.actions.handleRetryFailedPlan(activePlanId),
      planId: activePlanId,
      onOpenTask: (taskId: string) => {
        const node = nodes.find((candidate) => candidate.id === taskId);
        if (node) runtime.actions.onNodeClick(null, node);
      },
    };

    const isExpandedMode = runtime.state.executionState.displayMode.endsWith('-expanded');
    const showExecutionResizeHandle =
      Boolean(runtime.state.executionState.selectedTask) && !isExpandedMode;

    const executionSidebarProps = runtime.state.executionState.selectedTask
      ? {
          selectedTask: runtime.state.executionState.selectedTask,
          width: isExpandedMode ? '100%' : `${layout.sidebarWidth}px`,
          isExecuting: runtime.state.executionState.isExecuting,
          requiresApproval: requiresPlanApproval,
          displayMode: runtime.state.executionState.displayMode,
          executionLogs: runtime.state.selectedTaskState.logs,
          executionResult: runtime.state.selectedTaskState.result,
          executionCode: runtime.state.selectedTaskState.code,
          executionStructuredData: runtime.state.selectedTaskState.structuredData,
          executionImage: runtime.state.selectedTaskState.imageBase64 || '',
          logsEndRef,
          onClose: runtime.actions.closeTaskPanel,
          onExecute: () =>
            runtime.actions.handleExecuteTask(
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
	}, [activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanRuntimePolicyMode, activePlanPlannerSource, activePlanPlannerFallbackReason, activePlanRunTrigger, edges, intentContext, layout.sidebarWidth, logsEndRef, nodes, onEdgesChange, onNodesChange, runtime]);
}