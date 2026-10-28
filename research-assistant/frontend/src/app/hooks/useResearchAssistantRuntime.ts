import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Node } from '@xyflow/react';
import type { ChatMessage, ExecuteTaskResultEvent, NodeExecutionState, PlanGraph, Task } from '../../contracts/api';
import { PLAN_EVENTS, isPlanTerminalEvent, pickImageBase64 } from '../../contracts/events';
import { approvePlan, cancelPlan, createPlanEventSource, decideRuntimeAction, executePlan, executePlanTaskStream, retryTask } from '../../services/api/researchAssistantApi';
import { usePlanHistoryHydrator } from './usePlanHistoryHydrator';
import { describeReconnect, eventSeq, isTerminalPlanEventType, nextBackoffMs, shouldReconnect } from './planStreamReconnect';
import type { PlanExecutionTrigger, RequestIdentity } from '../../services/api/researchAssistantApi';
import { getTaskStyleByStatus } from '../../features/shared/agentVisuals';
import { createTaskNodeLabel } from '../../features/plan-graph/nodeLabelFactory';
import { uiText } from '../constants/uiText';

export type ExecutionDisplayMode =
  | 'logs'
  | 'report'
  | 'code'
  | 'plot'
  | 'evidence'
  | 'trials'
  | 'ablation'
  | 'report-expanded'
  | 'plot-expanded'
  | 'evidence-expanded'
  | 'trials-expanded'
  | 'ablation-expanded';

interface ExecutionState {
  selectedTask: Task | null;
  nodeStates: Record<string, NodeExecutionState>;
  displayMode: ExecutionDisplayMode;
  isExecuting: boolean;
	approvalResolved: boolean;
}

type ExecutionAction =
  | { type: 'select-task'; task: Task; state?: NodeExecutionState }
  | { type: 'close-task' }
  | { type: 'patch-task-state'; taskId: string; updater: (prev: NodeExecutionState) => NodeExecutionState }
  | { type: 'set-display-mode'; mode: ExecutionDisplayMode }
  | { type: 'set-executing'; value: boolean }
	| { type: 'set-approval-resolved'; value: boolean }
  | { type: 'reset' };

const initialNodeExecutionState: NodeExecutionState = { logs: '', result: '', code: '', structuredData: '', imageBase64: '' };
const reportAgents = new Set(['librarian_agent', 'data_agent', 'benchmark_agent', 'research_coding_agent']);
const isReportAgent = (assignedTo: string) => reportAgents.has(assignedTo);

const terminalNodeStatuses = new Set(['completed', 'failed', 'skipped', 'canceled']);

const updateNodeStatus = (node: Node, status: string, taskPatch: Partial<Task> = {}): Node => {
	const task = node.data.task as Task;
	if (!task) return node;
	const updatedTask = { ...task, ...taskPatch, Status: status };
	return {
		...node,
		data: {
			...node.data,
			status,
			task: updatedTask,
			label: createTaskNodeLabel({
				assignedTo: updatedTask.AssignedTo,
				taskName: updatedTask.Name,
				status,
				step: typeof node.data.step === 'number' ? node.data.step : undefined,
				task: updatedTask,
			}),
		},
		style: {
			...(node.style || {}),
			...getTaskStyleByStatus(status),
		},
	};
};

const updateNodeStatusIfActive = (node: Node, status: string): Node => {
	if (terminalNodeStatuses.has(String(node.data.status))) return node;
	return updateNodeStatus(node, status);
};

const detectBestDisplayMode = (task: Task, state: NodeExecutionState): ExecutionDisplayMode => {
  if (task.Type === 'claim_evidence_build' && state.structuredData) return 'evidence';
  if ((task.Type === 'autoresearch_run' || task.Type === 'autoresearch_validate' || task.Type === 'experiment_run' || task.Type === 'experiment_validate') && (state.structuredData || state.result)) return 'trials';
  if (task.Type === 'ablation_design' && (state.structuredData || state.result)) return 'ablation';
  if (state.imageBase64) return 'plot';
  if (state.code && !state.result) return 'code';
  if (state.result && isReportAgent(task.AssignedTo)) return 'report';
  return 'logs';
};

const executionStateFromTask = (task: Task): NodeExecutionState => ({
  logs: '',
  result: task.Result || '',
  code: task.Code || '',
  structuredData: task.StructuredData || '',
  imageBase64: task.ImageBase64 || '',
});

const PARAM_PREVIEW_LIMIT = 1200;

const previewParameterValue = (key: string, value: unknown): string => {
  if (value === undefined || value === null) return '';
  const keyLower = key.toLowerCase();
  if (typeof value === 'string' && (keyLower.includes('image') || keyLower.includes('base64'))) {
    return `<base64 image, ${value.length} chars>`;
  }

  const raw = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  if (!raw) return '';
  return raw.length > PARAM_PREVIEW_LIMIT ? `${raw.slice(0, PARAM_PREVIEW_LIMIT)}...[truncated]` : raw;
};

const formatParameterObject = (title: string, values: Record<string, unknown>): string => {
  const entries = Object.entries(values).filter(([, value]) => value !== undefined && value !== null && String(value).trim() !== '');
  if (entries.length === 0) return `${title}: 无`;
  return [
    `${title}:`,
    ...entries.map(([key, value]) => `- ${key}: ${previewParameterValue(key, value)}`),
  ].join('\n');
};

const formatArtifactSummaries = (title: string, rawArtifacts: unknown): string => {
  if (!Array.isArray(rawArtifacts) || rawArtifacts.length === 0) return `${title}: 无`;
  const lines = rawArtifacts.map((item) => {
    const artifact = item as Record<string, unknown>;
    const key = String(artifact.key || 'unknown');
    const type = artifact.type ? ` (${String(artifact.type)})` : '';
    const producer = artifact.producer_task_id ? ` <- ${String(artifact.producer_task_id)}` : '';
    return `- ${key}${type}${producer}: ${previewParameterValue(key, artifact.value_preview || '')}`;
  });
  return [`${title}:`, ...lines].join('\n');
};

const buildDirectExecutionOutputs = (result: string, code: string, structuredData: string, imageBase64: string): Record<string, unknown> => {
  const outputs: Record<string, unknown> = {};
  if (code) outputs.generated_code = code;
  if (result) outputs.result = result;
  if (structuredData) outputs.structured_data = structuredData;
  if (imageBase64) outputs.image_base64 = imageBase64;
  return outputs;
};

const executionReducer = (state: ExecutionState, action: ExecutionAction): ExecutionState => {
  switch (action.type) {
    case 'select-task': {
      const taskState = action.state ?? executionStateFromTask(action.task);
      return {
        ...state,
        selectedTask: action.task,
        displayMode: detectBestDisplayMode(action.task, taskState),
      };
    }
    case 'close-task':
      return {
        ...state,
        selectedTask: null,
        displayMode: 'logs',
      };
    case 'patch-task-state': {
      const prevTaskState = state.nodeStates[action.taskId] ?? initialNodeExecutionState;
      return {
        ...state,
        nodeStates: {
          ...state.nodeStates,
          [action.taskId]: action.updater(prevTaskState),
        },
      };
    }
    case 'set-display-mode':
      return { ...state, displayMode: action.mode };
    case 'set-executing':
      return { ...state, isExecuting: action.value };
	case 'set-approval-resolved':
	  return { ...state, approvalResolved: action.value };
    case 'reset':
      return {
        selectedTask: null,
        nodeStates: {},
        displayMode: 'logs',
        isExecuting: false,
		approvalResolved: false,
      };
    default:
      return state;
  }
};

interface UseResearchAssistantRuntimeOptions {
  nodes: Node[];
  setNodes: Dispatch<SetStateAction<Node[]>>;
  appendChatMessage: (message: ChatMessage) => void;
	identity: RequestIdentity;
	refreshPlanState?: () => void | Promise<void>;
	activePlanGraph?: PlanGraph | null;
}

export function useResearchAssistantRuntime(options: UseResearchAssistantRuntimeOptions) {
	const { nodes, setNodes, appendChatMessage, identity, refreshPlanState, activePlanGraph } = options;
  const planEventSourceRef = useRef<EventSource | null>(null);
  const planReconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const planStreamGenerationRef = useRef(0);
  const planStreamSeqRef = useRef(0);
  const planStreamAttemptsRef = useRef(0);
  const lastPlanEventTypeRef = useRef('');
  const activePlanStreamIdRef = useRef<string | null>(null);

  // Cancel the pending retry, invalidate in-flight connections and close the
  // current EventSource. Generation is bumped so stale async results are ignored.
  const closePlanStream = useCallback(() => {
    planStreamGenerationRef.current += 1;
    if (planReconnectTimerRef.current !== null) {
      clearTimeout(planReconnectTimerRef.current);
      planReconnectTimerRef.current = null;
    }
    planEventSourceRef.current?.close();
    planEventSourceRef.current = null;
    planStreamAttemptsRef.current = 0;
  }, []);

  const [executionState, dispatchExecution] = useReducer(executionReducer, {
    selectedTask: null,
    nodeStates: {},
    displayMode: 'logs',
    isExecuting: false,
	approvalResolved: false,
  });

  const selectedTaskState = useMemo(() => {
    if (!executionState.selectedTask) return initialNodeExecutionState;
    return executionState.nodeStates[executionState.selectedTask.ID] ?? executionStateFromTask(executionState.selectedTask);
  }, [executionState.selectedTask, executionState.nodeStates]);

  const patchNodeState = useCallback((taskId: string, updater: (prev: NodeExecutionState) => NodeExecutionState) => {
    dispatchExecution({ type: 'patch-task-state', taskId, updater });
  }, []);

  const appendNodeLog = useCallback(
    (taskId: string, line: string) => {
      patchNodeState(taskId, (prev) => ({
        ...prev,
        logs: prev.logs ? `${prev.logs}\n${line}` : line,
      }));
    },
    [patchNodeState],
  );

  const updateNodeVisualState = useCallback(
    (taskId: string, status: string) => {
      setNodes((nds) =>
		nds.map((node) => (node.id === taskId ? updateNodeStatus(node, status) : node)),
      );
    },
    [setNodes],
  );

  const { applyHistory } = usePlanHistoryHydrator(patchNodeState, identity);

  // On refresh/session switch, hydrate node logs and results from the durable
  // plan event log so the execution panel is not blank for finished plans.
  const activePlanGraphId = activePlanGraph?.id;
  const activePlanGraphStatus = activePlanGraph?.status;
  useEffect(() => {
    if (!activePlanGraphId || !activePlanGraphStatus) return;
    void applyHistory({ id: activePlanGraphId, status: activePlanGraphStatus });
  }, [activePlanGraphId, activePlanGraphStatus, applyHistory]);

  const connectPlanStream = useCallback(
    async (planId: string) => {
      closePlanStream();

      if (activePlanStreamIdRef.current !== planId) {
        activePlanStreamIdRef.current = planId;
        planStreamSeqRef.current = 0;
      }
      lastPlanEventTypeRef.current = '';
      const generation = planStreamGenerationRef.current;

      let source: EventSource | null = null;
      let connectionToken = 0;

      const disposeStream = () => {
        if (source) {
          source.close();
          source = null;
        }
        if (planEventSourceRef.current) {
          planEventSourceRef.current.close();
          planEventSourceRef.current = null;
        }
      };

      const scheduleReconnect = () => {
        if (generation !== planStreamGenerationRef.current) return;
        if (planReconnectTimerRef.current !== null) return;
        const attempt = planStreamAttemptsRef.current;
        if (!shouldReconnect(lastPlanEventTypeRef.current, attempt)) {
          dispatchExecution({ type: 'set-executing', value: false });
          // A terminal event already produced its own message; only a genuine
          // retry exhaustion needs the "stream interrupted" notice.
          if (!isTerminalPlanEventType(lastPlanEventTypeRef.current)) {
            appendChatMessage({ role: 'system', text: '计划事件流已中断；请刷新计划状态后重试。' });
          }
          return;
        }
        const nextAttempt = attempt + 1;
        planStreamAttemptsRef.current = nextAttempt;
        appendChatMessage({ role: 'system', text: describeReconnect(nextAttempt) });
        planReconnectTimerRef.current = setTimeout(() => {
          planReconnectTimerRef.current = null;
          if (generation !== planStreamGenerationRef.current) return;
          const cursor = planStreamSeqRef.current > 0 ? planStreamSeqRef.current : undefined;
          void openStream(cursor, true).catch((error: unknown) => {
            console.error(error);
            scheduleReconnect();
          });
        }, nextBackoffMs(attempt));
      };

      const openStream = async (afterSeq: number | undefined, isReconnect: boolean): Promise<void> => {
        const token = connectionToken + 1;
        connectionToken = token;
        const created = await createPlanEventSource(planId, identity, {
          onPlanEvent: (event) => {
            // Ignore queued callbacks from a stream superseded by a plan switch,
            // cancel, or a newer connection in the same reconnect loop.
            if (generation !== planStreamGenerationRef.current) return;
            if (token !== connectionToken) return;
            const seq = eventSeq(event);

            // Any event proves the new connection is alive, including a replay
            // of something already applied. Announce recovery once, then reset
            // the backoff budget so a later outage starts from 1s again.
            if (planStreamAttemptsRef.current > 0) {
              planStreamAttemptsRef.current = 0;
              const resumeSeq = Math.max(seq, planStreamSeqRef.current);
              appendChatMessage({ role: 'system', text: '计划事件流已恢复（从序号 ' + resumeSeq + ' 继续）' });
            }

            if (seq > 0) {
              // A reconnect replays events the UI already applied; drop them.
              if (seq <= planStreamSeqRef.current) return;
              planStreamSeqRef.current = seq;
            }
            lastPlanEventTypeRef.current = event.event_type;

            if (event.task_id && event.task_status) {
              updateNodeVisualState(event.task_id, event.task_status);
            }

            if (event.event_type === PLAN_EVENTS.TASK_READY && event.task_id) {
              appendNodeLog(event.task_id, `[Plan] ready\n${formatArtifactSummaries('[Plan Params] 本次上游传入参数', event.payload?.inputs)}`);
            }
            if (event.event_type === PLAN_EVENTS.TASK_STARTED && event.task_id) {
              appendNodeLog(event.task_id, '[Plan] started');
            }
            if (event.event_type === PLAN_EVENTS.TASK_LOG && event.task_id) {
              appendNodeLog(event.task_id, String(event.payload?.message || ''));
            }
            if (event.event_type === PLAN_EVENTS.ARTIFACT_CREATED && event.task_id) {
              const keys = Array.isArray(event.payload?.artifact_keys) ? event.payload?.artifact_keys?.join(', ') : '';
              appendNodeLog(
                event.task_id,
                `[Plan] artifacts created${keys ? `: ${keys}` : ''}\n${formatArtifactSummaries('[Plan Params] 将传递给下游的参数', event.payload?.artifacts)}`,
              );
            }
            if (event.event_type === PLAN_EVENTS.TASK_BLOCKED && event.task_id) {
              const upstream = String(event.payload?.upstream_task_id || '');
              appendNodeLog(event.task_id, `[Plan] blocked${upstream ? ` by ${upstream}` : ''}`);
            }
            if (event.event_type === PLAN_EVENTS.TASK_COMPLETED && event.task_id) {
              const result = String(event.payload?.result || event.payload?.result_summary || '');
              const code = String(event.payload?.code || '');
              const structuredData = String(event.payload?.structured_data || '');
              patchNodeState(event.task_id, (prev) => ({
                ...prev,
                logs: prev.logs ? `${prev.logs}\n[Plan] task_completed` : '[Plan] task_completed',
                result: result || prev.result || '',
                code: code || prev.code || '',
                structuredData: structuredData || prev.structuredData || '',
                imageBase64: pickImageBase64(event.payload) || prev.imageBase64 || '',
              }));
              setNodes((current) => current.map((node) => node.id === event.task_id
                ? updateNodeStatus(node, 'completed', {
                    Result: result || (node.data.task as Task | undefined)?.Result,
                    Code: code || (node.data.task as Task | undefined)?.Code,
                    StructuredData: structuredData || (node.data.task as Task | undefined)?.StructuredData,
                  })
                : node));
            }
            if (event.event_type === PLAN_EVENTS.RUNTIME_ACTION_REQUESTED) {
              const action = event.payload?.action as { title?: string; kind?: string } | undefined;
              appendChatMessage({ role: 'system', text: `需要你的运行权限确认：${action?.title || action?.kind || '重要操作'}。请在右侧计划面板允许或拒绝。` });
              void refreshPlanState?.();
            }
            if (event.event_type === PLAN_EVENTS.RUNTIME_ACTION_RESOLVED) {
              void refreshPlanState?.();
            }
            if ((event.event_type === PLAN_EVENTS.VERIFICATION_COMPLETED || event.event_type === PLAN_EVENTS.VERIFICATION_FAILED) && event.task_id) {
              const verification = event.payload?.verification as Task['Verification'] | undefined;
              if (verification) {
                patchNodeState(event.task_id, (prev) => ({
                  ...prev,
                  logs: prev.logs ? `${prev.logs}\n[Plan] verification_${verification.status}` : `[Plan] verification_${verification.status}`,
                }));
                setNodes((current) => current.map((node) => node.id === event.task_id
                  ? updateNodeStatus(node, String(node.data.status || 'completed'), {
                      Verification: verification,
                      Result: (node.data.task as Task | undefined)?.Result,
                      Code: (node.data.task as Task | undefined)?.Code,
                      StructuredData: (node.data.task as Task | undefined)?.StructuredData,
                    })
                  : node));
                void refreshPlanState?.();
              }
            }
            if (event.event_type === PLAN_EVENTS.TASK_FAILED && event.task_id) {
              const errorText = String(event.payload?.error || 'Task failed');
              patchNodeState(event.task_id, (prev) => ({
                ...prev,
                logs: prev.logs ? `${prev.logs}\n[Plan Error] ${errorText}` : `[Plan Error] ${errorText}`,
              }));
            }

            if (isPlanTerminalEvent(event)) {
              if (event.event_type === PLAN_EVENTS.PLAN_CANCELED) {
                setNodes((current) => current.map((node) => updateNodeStatusIfActive(node, 'canceled')));
              }
              disposeStream();
              if (planReconnectTimerRef.current !== null) {
                clearTimeout(planReconnectTimerRef.current);
                planReconnectTimerRef.current = null;
              }
              planStreamAttemptsRef.current = 0;
              dispatchExecution({ type: 'set-executing', value: false });
              appendChatMessage({
                role: 'system',
                text:
                  event.event_type === PLAN_EVENTS.PLAN_COMPLETED
                    ? '整张拓扑图已经执行完成。'
                    : `计划执行失败：${String(event.payload?.error || event.payload?.reason || '未知错误')}`,
              });
              void refreshPlanState?.();
            }
          },
          onError: () => {
            if (generation !== planStreamGenerationRef.current) return;
            if (token !== connectionToken) return;
            disposeStream();
            scheduleReconnect();
          },
        }, { afterSeq, forceTicket: isReconnect });

        if (generation !== planStreamGenerationRef.current) {
          created.close();
          return;
        }
        source = created;
        planEventSourceRef.current = created;
      };

      await openStream(planStreamSeqRef.current > 0 ? planStreamSeqRef.current : undefined, false);
    },
	[appendChatMessage, appendNodeLog, closePlanStream, identity, patchNodeState, refreshPlanState, setNodes, updateNodeVisualState],
  );

  const handleExecuteTask = useCallback(
    async (task: Task, planId: string | null) => {
      if (!planId) {
        appendChatMessage({ role: 'system', text: '无法执行节点：当前会话没有关联的计划。' });
        return;
      }
      dispatchExecution({ type: 'set-executing', value: true });
      dispatchExecution({
        type: 'select-task',
        task,
        state: executionState.nodeStates[task.ID],
      });
      dispatchExecution({ type: 'set-display-mode', mode: 'logs' });

      const nodeInputs = task.Inputs ?? {};
      const initLog = `[System] 正在唤醒 ${task.AssignedTo}...\n[System] 正在通过 Eino 框架调用 DeepSeek 模型${
        isReportAgent(task.AssignedTo) ? '生成结构化结果' : '生成代码'
      }...\n\n${formatParameterObject('[Params] 计划内任务输入', nodeInputs)}\n`;
      patchNodeState(task.ID, () => ({ logs: initLog, result: '', code: '', structuredData: '', imageBase64: '' }));

      try {
        await executePlanTaskStream(
          planId,
          task.ID,
          identity,
          {
            onLog: (line) => {
              patchNodeState(task.ID, (prev) => ({
                ...prev,
                logs: prev.logs ? `${prev.logs}\n${line}` : line,
              }));
            },
            onResult: (rawData) => {
              let finalResult = rawData;
              let generatedCode = '';
              let structuredData = '';
              let imageBase64 = '';

              try {
                const parsed = JSON.parse(rawData) as ExecuteTaskResultEvent;
                if (parsed?.result) finalResult = parsed.result;
                if (parsed?.code) generatedCode = parsed.code;
                if (parsed?.structured_data) structuredData = parsed.structured_data;
                imageBase64 = pickImageBase64(parsed);
              } catch {
                // keep raw fallback
              }

              const directOutputs = buildDirectExecutionOutputs(finalResult, generatedCode, structuredData, imageBase64);

              patchNodeState(task.ID, (prev) => ({
                logs: `${prev.logs || ''}\n\n${formatParameterObject('[Params] 将传递给下游的参数', directOutputs)}\n\n[exec] agent finished`,
                result: finalResult,
                code: generatedCode,
                structuredData,
                imageBase64,
              }));
              setNodes((current) => current.map((node) => node.id === task.ID
                ? updateNodeStatus(node, 'completed', {
                    Result: finalResult,
                    Code: generatedCode,
                    StructuredData: structuredData,
                    ImageBase64: imageBase64,
                  })
                : node));

              const taskActions: ('view_plot' | 'view_report')[] = [];
              if (imageBase64) taskActions.push('view_plot');
              if (finalResult && isReportAgent(task.AssignedTo)) {
                taskActions.push('view_report');
              }

              appendChatMessage({
                role: 'system',
                taskId: task.ID,
                text:
                  `节点 **[${task.Name}]** 已完成。完整结果可在右侧检查器的日志、报告与图表中查看。` +
                  (generatedCode
                    ? `\n\n**生成的代码片段:**\n\`\`\`python\n${generatedCode.substring(0, 300)}${
                        generatedCode.length > 300 ? '\n... (代码较长，请在右侧面板查看完整代码)' : ''
                      }\n\`\`\``
                    : ''),
                actions: taskActions.length > 0 ? taskActions : undefined,
              });

              if (task.Type === 'claim_evidence_build' && structuredData) {
                dispatchExecution({ type: 'set-display-mode', mode: 'evidence' });
              } else if ((task.Type === 'autoresearch_run' || task.Type === 'autoresearch_validate' || task.Type === 'experiment_run' || task.Type === 'experiment_validate') && (structuredData || finalResult)) {
                dispatchExecution({ type: 'set-display-mode', mode: 'trials' });
              } else if (imageBase64) dispatchExecution({ type: 'set-display-mode', mode: 'plot' });
              else if (isReportAgent(task.AssignedTo)) {
                dispatchExecution({ type: 'set-display-mode', mode: 'report' });
              }

              updateNodeVisualState(task.ID, 'completed');
            },
            onError: (errorMessage) => {
              throw new Error(errorMessage);
            },
          },
        );
      } catch (error: unknown) {
        console.error(error);
        const message = error instanceof Error ? error.message : String(error);
        const errorMsg =
          message === 'Failed to fetch'
            ? '与后端连接中断：可能是模型响应超时或本地端口被占用。请稍后重试，或检查后端服务状态。'
            : message;

        patchNodeState(task.ID, (prev) => ({
          ...prev,
          logs: `${prev.logs}\n\n[error] 执行中断 ${errorMsg}`,
        }));
        appendChatMessage({
          role: 'system',
          text: `节点 **[${task.Name}]** 执行失败。\n\n**错误信息**\n\n${errorMsg}`,
        });
        updateNodeVisualState(task.ID, 'failed');
        throw error;
      } finally {
        dispatchExecution({ type: 'set-executing', value: false });
      }
    },
    [appendChatMessage, executionState.nodeStates, identity, patchNodeState, setNodes, updateNodeVisualState],
  );

  const handleRunAllTasks = useCallback(
    async (activePlanId: string | null, trigger: PlanExecutionTrigger = 'manual') => {
      if (executionState.isExecuting) return;
      if (!activePlanId) {
        appendChatMessage({ role: 'system', text: uiText.noPlanMessage });
        return;
      }

      dispatchExecution({ type: 'set-executing', value: true });
      appendChatMessage({ role: 'system', text: uiText.planStartMessage });

      try {
		await executePlan(activePlanId, identity, trigger);
        await connectPlanStream(activePlanId);
      } catch (error) {
        console.error(error);
        dispatchExecution({ type: 'set-executing', value: false });
        appendChatMessage({ role: 'system', text: uiText.planStartFailedMessage });
      }
    },
	[appendChatMessage, connectPlanStream, executionState.isExecuting, identity],
  );

	const handleApproveAndRun = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId || executionState.isExecuting) return;
			dispatchExecution({ type: 'set-executing', value: true });
			try {
				await approvePlan(activePlanId, identity);
				dispatchExecution({ type: 'set-approval-resolved', value: true });
				await executePlan(activePlanId, identity);
				appendChatMessage({ role: 'system', text: '计划已审批，开始执行。' });
				await connectPlanStream(activePlanId);
			} catch (error) {
				console.error(error);
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '计划审批或启动失败，请检查运行状态。' });
			}
		},
		[appendChatMessage, connectPlanStream, executionState.isExecuting, identity],
	);

	const runtimeDecisionInFlightRef = useRef(false);
	const handleRuntimeActionDecision = useCallback(
		async (planId: string | null, actionId: string, decision: 'approve' | 'reject'): Promise<boolean> => {
			if (!planId || runtimeDecisionInFlightRef.current) return false;
			runtimeDecisionInFlightRef.current = true;
			try {
				const action = await decideRuntimeAction(planId, actionId, decision, identity);
				appendChatMessage({
					role: 'system',
					text: decision === 'approve' ? `已允许运行操作：${action.title}。` : `已拒绝运行操作：${action.title}，计划已停止。`,
				});
				await refreshPlanState?.();
				if (decision === 'approve') {
					await connectPlanStream(planId);
				}
				return true;
			} catch (error) {
				console.error(error);
				appendChatMessage({ role: 'system', text: '运行权限审批失败，请刷新计划状态后重试。' });
				return false;
			} finally {
				runtimeDecisionInFlightRef.current = false;
			}
		},
		[appendChatMessage, connectPlanStream, identity, refreshPlanState],
	);

	const handleCancelPlan = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId) return;
			try {
				await cancelPlan(activePlanId, identity);
				closePlanStream();
				setNodes((current) => current.map((node) => updateNodeStatusIfActive(node, 'canceled')));
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '计划已取消，未完成任务已停止。' });
			} catch (error) {
				console.error(error);
				appendChatMessage({ role: 'system', text: '取消计划失败，请刷新计划状态后重试。' });
			}
		},
		[appendChatMessage, closePlanStream, identity, setNodes],
	);

	const handleRetryFailedPlan = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId || executionState.isExecuting) return;
			const failedNodes = nodes.filter((node) => node.data.status === 'failed');
			if (failedNodes.length === 0) return;
			dispatchExecution({ type: 'set-executing', value: true });
			try {
				for (const failedNode of failedNodes) {
					await retryTask(activePlanId, failedNode.id, identity);
				}
				setNodes((current) =>
					current.map((node) =>
						['failed', 'blocked', 'canceled'].includes(String(node.data.status))
							? updateNodeStatus(node, 'pending')
							: node,
					),
				);
				await executePlan(activePlanId, identity);
				appendChatMessage({ role: 'system', text: '失败节点已重置，计划开始重试。' });
				await connectPlanStream(activePlanId);
			} catch (error) {
				console.error(error);
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '重试计划失败，请检查节点状态。' });
			}
		},
		[appendChatMessage, connectPlanStream, executionState.isExecuting, identity, nodes, setNodes],
	);

  const onNodeClick = useCallback(
    (_: unknown, node: Node) => {
      const taskData = node.data.task as Task;
      if (!taskData) return;
      dispatchExecution({
        type: 'select-task',
        task: taskData,
        state: executionState.nodeStates[taskData.ID],
      });
    },
    [executionState.nodeStates],
  );

  const handleOpenTaskView = useCallback(
    (taskId: string, mode: 'plot' | 'report') => {
      const targetNode = nodes.find((n) => n.id === taskId);
      if (!targetNode) return;
      onNodeClick(null, targetNode);
      dispatchExecution({ type: 'set-display-mode', mode });
    },
    [nodes, onNodeClick],
  );

  const appendSelectedTaskLog = useCallback(
    (line: string) => {
      if (!executionState.selectedTask) return;
      appendNodeLog(executionState.selectedTask.ID, line);
    },
    [appendNodeLog, executionState.selectedTask],
  );

  // Switching plans must not leave the previous stream or its retry timer alive;
  // connectPlanStream also closes the previous subscription when reconnecting.
  useEffect(() => {
    if (activePlanStreamIdRef.current && activePlanStreamIdRef.current !== activePlanGraphId) {
      closePlanStream();
      activePlanStreamIdRef.current = null;
    }
  }, [activePlanGraphId, closePlanStream]);

  useEffect(
    () => () => {
      closePlanStream();
    },
    [closePlanStream],
  );

  const setDisplayMode = useCallback(
    (mode: ExecutionDisplayMode) => {
      dispatchExecution({ type: 'set-display-mode', mode });
    },
    [],
  );

  const closeTaskPanel = useCallback(() => {
    dispatchExecution({ type: 'close-task' });
  }, []);

  const resetRuntimeState = useCallback(() => {
    dispatchExecution({ type: 'reset' });
  }, []);

  return {
    executionState,
    selectedTaskState,
    onNodeClick,
    handleOpenTaskView,
    handleExecuteTask,
    handleRunAllTasks,
	handleApproveAndRun,
	handleRuntimeActionDecision,
	handleCancelPlan,
	handleRetryFailedPlan,
    appendSelectedTaskLog,
    setDisplayMode,
    closeTaskPanel,
    resetRuntimeState,
  };
}