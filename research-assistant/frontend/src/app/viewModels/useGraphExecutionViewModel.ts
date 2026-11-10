import { useMemo } from 'react';
import type { RefObject } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import type { IntentContext, MissingRequirement, PlanGraph, PlanQuestion, RuntimeAction, TaskUsage, Verification } from '../../contracts/api';
import { canStartPlanRun, isTerminalPlanStatus, planBusinessState } from '../hooks/planObservation';
import type { PlanBusinessState } from '../hooks/planObservation';
import { useResearchAssistantRuntimeContext } from '../context/ResearchAssistantRuntimeContext';
import type { PlanConnectionState, PlanObservationExecutionFields as PlanObservationFields } from '../context/ResearchAssistantRuntimeContext';
import { uiText } from '../constants/uiText';
import type { useResearchAssistantLayoutState } from '../hooks/useResearchAssistantLayoutState';

export type PlanRunTrigger = 'manual' | 'auto' | '';

// PlanGraph predates run_trigger in the shared contract; narrow the optional
// backend field here so the view model stays the single reader for the colophon.
export const readPlanRunTrigger = (planGraph: PlanGraph | null | undefined): PlanRunTrigger => {
  const raw = planGraph?.run_trigger;
  return raw === 'manual' || raw === 'auto' ? raw : '';
};

export interface PlanObservationSnapshot {
  /** Server status of the observed plan (only trusted for the active plan). */
  status: string | null;
  businessState: PlanBusinessState;
  paused: boolean;
  startAccepted: boolean;
  connectionState?: PlanConnectionState;
  connectionDetail: string;
  /**
   * True when the runtime is observing a *different* plan than the active one.
   * Its status must never leak into the active plan's buttons.
   */
  observationMismatch: boolean;
}

/**
 * Resolve the status the canvas should believe.
 *
 * The runtime's `serverPlanStatus` wins only when it belongs to the plan the
 * workbench is showing; otherwise the snapshot from the plan API
 * (`activePlanStatus`) stays authoritative. `paused` is an independent flag and
 * is ignored while there is a mismatch.
 *
 * `executionState` is deliberately typed as `object`: the runtime hook gains
 * these optional fields in parallel, so reading them must not depend on the
 * hook's return type already declaring them.
 */
export const resolvePlanObservation = (
  activePlanId: string | null,
  activePlanStatus: string | null,
  executionState: object | null | undefined,
): PlanObservationSnapshot => {
  const observedFields = (executionState ?? {}) as PlanObservationFields;
  const observedPlanId = observedFields.observedPlanId ?? '';
  // Design 4.2/4.4: the runtime snapshot is trusted only for the plan it was
  // taken for. Anything observed for another plan (or nothing bound yet) must
  // not drive this plan's buttons, so the plan API snapshot stays in charge.
  const observedMatchesActive = Boolean(activePlanId) && observedPlanId === activePlanId;
  const serverStatus = typeof observedFields.serverPlanStatus === 'string' ? observedFields.serverPlanStatus : '';
  const trustedServerStatus = observedMatchesActive && serverStatus.trim() !== '' ? serverStatus : '';
  const status = trustedServerStatus || activePlanStatus;
  const paused = observedMatchesActive && observedFields.planPaused === true;
  const startAccepted = observedMatchesActive && observedFields.startAccepted === true;
  return {
    status,
    businessState: planBusinessState(status, paused),
    paused,
    startAccepted,
    connectionState: observedMatchesActive ? observedFields.connectionState : undefined,
    connectionDetail: observedMatchesActive ? observedFields.connectionDetail ?? '' : '',
    observationMismatch: Boolean(observedPlanId) && observedPlanId !== activePlanId,
  };
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
  activePlanQuestions?: PlanQuestion[];
  activePlanReviewStatus?: string | null;
  activePlanReviewRequired?: boolean;
  onAnswerQuestion?: (questionId: string, answer: { answer?: string; answer_option?: string }) => Promise<void> | void;
  onOpenReview?: () => void;
  activePlanRuntimePolicyMode?: string | null;
  activePlanPlannerSource?: string | null;
  activePlanPlannerFallbackReason?: string | null;
  activePlanComplexity?: string | null;
  activePlanPlanningRoute?: string | null;
  activePlanTemplateVariant?: string | null;
  activePlanRunTrigger?: PlanRunTrigger | null;
  activePlanVerification?: Verification | null;
  activePlanUsage?: TaskUsage | null;
  onOpenModelSettings?: () => void;
  layout: ReturnType<typeof useResearchAssistantLayoutState>;
  logsEndRef: RefObject<HTMLDivElement | null>;
}

export function useGraphExecutionViewModel(options: UseGraphExecutionViewModelOptions) {
	const { nodes, edges, onNodesChange, onEdgesChange, intentContext, activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanQuestions, activePlanReviewStatus, activePlanReviewRequired, onAnswerQuestion, onOpenReview, activePlanRuntimePolicyMode, activePlanPlannerSource, activePlanPlannerFallbackReason, activePlanComplexity, activePlanPlanningRoute, activePlanTemplateVariant, activePlanRunTrigger, activePlanVerification, activePlanUsage, onOpenModelSettings, layout, logsEndRef } = options;
  const runtime = useResearchAssistantRuntimeContext();

  return useMemo(() => {
    const executionState = runtime.state.executionState;
    // The observation fields are optional while the runtime lands in parallel.
    const observation = resolvePlanObservation(activePlanId, activePlanStatus, executionState);
    const effectivePlanStatus = observation.status;
    const planPaused = observation.paused;
    const startAccepted = observation.startAccepted;
    const businessState = observation.businessState;
    const isServerActive =
      businessState === 'observing' || businessState === 'paused' || startAccepted;

    // A pending runtime action must be decided on its own card. Showing the
    // plan-level "审批并运行" button at the same time caused a guaranteed 409.
    const hasPendingRuntimeAction = (activePlanRuntimeActions ?? []).some((action) => action.status === 'pending');
    const requiresPlanApproval =
      businessState === 'awaiting_approval' &&
      !hasPendingRuntimeAction &&
      !executionState.approvalResolved;
    // A review gate blocks execution until the user approves the latest
    // revision; the primary action becomes "go to review" instead of "run".
    const requiresPlanReview =
      businessState === 'review' &&
      (Boolean(activePlanReviewRequired) || activePlanReviewStatus === 'pending');
    // Only a runnable plan that was not accepted yet may be started. `blocked`,
    // `unknown`, `paused`, waiting gates, terminal states and an interrupted
    // connection all keep the run button away instead of re-executing.
    const canRun = canStartPlanRun(effectivePlanStatus, {
      paused: planPaused,
      startAccepted,
      connectionActive: observation.connectionState === 'live',
    });
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
      questions: activePlanQuestions ?? [],
      onAnswerQuestion,
      requiresReview: requiresPlanReview,
      onOpenReview,
      runtimePermissionMode: activePlanRuntimePolicyMode ?? null,
      plannerSource: activePlanPlannerSource ?? null,
      plannerFallbackReason: activePlanPlannerFallbackReason ?? null,
      complexity: activePlanComplexity ?? null,
      planningRoute: activePlanPlanningRoute ?? null,
      templateVariant: activePlanTemplateVariant ?? null,
      runTrigger: activePlanRunTrigger ?? '',
      verification: activePlanVerification ?? null,
      onOpenModelSettings,
      onRuntimeActionDecision: (actionId: string, decision: 'approve' | 'reject') =>
        runtime.actions.handleRuntimeActionDecision(activePlanId, actionId, decision),
      runAllText: uiText.runAll,
      graphTitle: uiText.graphTitle,
      graphHint: uiText.graphHint,
      isExecuting: executionState.isExecuting,
	  requiresApproval: requiresPlanApproval,
      onRunAll: () => void runtime.actions.handleRunAllTasks(activePlanId),
	  onApproveAndRun: () => void runtime.actions.handleApproveAndRun(activePlanId),
	  onCancel: () => void runtime.actions.handleCancelPlan(activePlanId),
	  onRetryFailed: () => void runtime.actions.handleRetryFailedPlan(activePlanId),
      // Resume exists only when the runtime exposes the backend-permitted
      // resume action, and it always targets the active plan. It never falls
      // back to execute/approve/retry: a paused plan without resume is canceled.
      onResume: activePlanId && runtime.actions.handleResumePlan
        ? () => void runtime.actions.handleResumePlan?.(activePlanId)
        : undefined,
      planId: activePlanId,
      planStatus: effectivePlanStatus,
      canRun,
      startAccepted,
      planPaused,
      connectionState: observation.connectionState,
      connectionDetail: observation.connectionDetail,
      // Reconnection only rebuilds the observation flow: never execute, retry
      // or resume from here.
      onReconnect: activePlanId
        ? () => {
            if (runtime.actions.handleReconnect) {
              void runtime.actions.handleReconnect();
              return;
            }
            // Fallback while the runtime exposes only the observation entry.
            void runtime.actions.observePlan?.(activePlanId);
          }
        : undefined,
      onOpenTask: (taskId: string) => {
        const node = nodes.find((candidate) => candidate.id === taskId);
        if (node) runtime.actions.onNodeClick(null, node);
      },
    };

    const isExpandedMode = executionState.displayMode.endsWith('-expanded');
    const showExecutionResizeHandle =
      Boolean(executionState.selectedTask) && !isExpandedMode;

    // Node-level execution is gated by the same server truth: while the plan
    // runs, waits for a decision or was accepted but not flipped yet, a single
    // task must not be started again. The sidebar has no `canExecute` prop, so
    // the gate rides on the existing `isExecuting` flag it already honors.
    const serverBusy =
      isServerActive ||
      businessState === 'awaiting_approval' ||
      businessState === 'awaiting_input' ||
      businessState === 'review' ||
      businessState === 'blocked' ||
      businessState === 'unknown';
    const sidebarIsExecuting = executionState.isExecuting || serverBusy;

    const executionSidebarProps = executionState.selectedTask
      ? {
          selectedTask: executionState.selectedTask,
          planUsage: activePlanUsage ?? null,
          width: isExpandedMode ? '100%' : `${layout.sidebarWidth}px`,
          isExecuting: sidebarIsExecuting,
          requiresApproval: requiresPlanApproval,
          displayMode: executionState.displayMode,
          executionLogs: runtime.state.selectedTaskState.logs,
          executionResult: runtime.state.selectedTaskState.result,
          executionCode: runtime.state.selectedTaskState.code,
          executionStructuredData: runtime.state.selectedTaskState.structuredData,
          executionImage: runtime.state.selectedTaskState.imageBase64 || '',
          logsEndRef,
          onClose: runtime.actions.closeTaskPanel,
          onExecute: () => {
            if (serverBusy || executionState.isExecuting) return;
            runtime.actions.handleExecuteTask(
              executionState.selectedTask as NonNullable<typeof executionState.selectedTask>,
              activePlanId,
            );
          },
          onChangeDisplayMode: runtime.actions.setDisplayMode,
          onOpenModelSettings,
        }
      : null;

    return {
      graphPanelProps,
      showExecutionResizeHandle,
      executionSidebarProps,
      planUsage: activePlanUsage ?? null,
      planObservation: observation,
      planBusinessState: businessState,
      canRunPlan: canRun,
      planIsTerminal: isTerminalPlanStatus(effectivePlanStatus),
    };
	}, [activePlanId, activePlanStatus, activePlanOutcome, activePlanMissingRequirements, activePlanRuntimeActions, activePlanQuestions, activePlanReviewStatus, activePlanReviewRequired, onAnswerQuestion, onOpenReview, activePlanRuntimePolicyMode, activePlanPlannerSource, activePlanPlannerFallbackReason, activePlanComplexity, activePlanPlanningRoute, activePlanTemplateVariant, activePlanRunTrigger, activePlanVerification, activePlanUsage, onOpenModelSettings, edges, intentContext, layout.sidebarWidth, logsEndRef, nodes, onEdgesChange, onNodesChange, runtime]);
}
