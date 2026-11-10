import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Check,
  GitCompare,
  GitFork,
  Loader2,
  RefreshCw,
  Save,
  ShieldCheck,
  X,
} from 'lucide-react';
import type {
  PlanDiffEntry,
  PlanRevision,
  PlanRevisionSummary,
  ValidationIssue,
} from '../../contracts/api';
import {
  approveRevision,
  createRevision,
  executeRevision,
  getPlanDiff,
  getRevision,
  listRevisions,
  rejectRevision,
  validateRevision,
  type RequestIdentity,
} from '../../services/api/researchAssistantApi';
import { LeaveDraftDialog } from './LeaveDraftDialog';
import {
  classifyApiError,
  conflictServerRevision,
  describeApiError,
  isRevisionConflict,
  validationIssuesFromPayload,
} from './revisionErrors';
import {
  cloneForm,
  collectFormIssues,
  formFromGraph,
  formSignature,
  graphFromForm,
  isFormDirty,
  parseInputsText,
  type DraftForm,
  type DraftNode,
  type FormIssue,
} from './reviewDraft';
import {
  captureEventBoundary,
  reconcileApproval,
  reconcileExecution,
  reconcileSaveOutcome,
  type EventBoundary,
} from './serverStateReconciliation';

export interface PlanReviewPanelProps {
  planId: string;
  /** Server-derived identity; only needed to reconcile unknown outcomes. */
  identity?: RequestIdentity;
  onClose: () => void;
  /** A draft revision was created; the panel stays open on that revision. */
  onSaved?: (revision: number) => void;
  /** Execution was accepted; the shell may close the panel and observe. */
  onExecutionStarted?: (revision: number) => void;
  /**
   * Lets the shell route plan/session switches through the same leave guard.
   * The guard calls `proceed` when leaving is allowed and resolves `true`; it
   * resolves `false` when the user stays, or when the flow is busy or a save
   * outcome is still unconfirmed.
   */
  onRegisterLeaveGuard?: (guard: ((proceed: () => void) => Promise<boolean>) | null) => void;
  /** @deprecated kept for compatibility; only used as a fallback start signal. */
  onApplied?: () => void;
}

interface RevisionValidation {
  revision: number;
  valid: boolean;
  issues: ValidationIssue[];
}

interface ExecutionUnknownState {
  revision: number;
  boundary: EventBoundary;
}

const revisionStatusLabel = (status: string): string => {
  switch (status) {
    case 'draft':
      return '草稿';
    case 'approved':
      return '已批准';
    case 'executing':
      return '执行中';
    case 'completed':
      return '已完成';
    case 'superseded':
      return '已被取代';
    case 'rejected':
      return '已拒绝';
    default:
      return status;
  }
};

const formatTime = (value?: string): string => {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
};

const renderValue = (value: unknown): string => {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

export function PlanReviewPanel({
  planId,
  identity,
  onClose,
  onSaved,
  onExecutionStarted,
  onRegisterLeaveGuard,
  onApplied,
}: PlanReviewPanelProps) {
  const [revisions, setRevisions] = useState<PlanRevisionSummary[]>([]);
  const [currentRevision, setCurrentRevision] = useState(0);
  const [latestRevision, setLatestRevision] = useState(0);
  const [selected, setSelected] = useState<number | null>(null);
  const [detail, setDetail] = useState<PlanRevision | null>(null);
  const [form, setForm] = useState<DraftForm | null>(null);
  const [baseline, setBaseline] = useState<DraftForm | null>(null);
  const [activeNodeId, setActiveNodeId] = useState<string>('');
  const [diff, setDiff] = useState<PlanDiffEntry[]>([]);
  const [diffFrom, setDiffFrom] = useState<number | null>(null);
  const [diffTo, setDiffTo] = useState<number | null>(null);
  const [validation, setValidation] = useState<RevisionValidation | null>(null);
  const [fieldIssues, setFieldIssues] = useState<FormIssue[]>([]);
  const [highlightedField, setHighlightedField] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [busyLabel, setBusyLabel] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [conflict, setConflict] = useState<{ serverRevision: number } | null>(null);
  const [saveUnknown, setSaveUnknown] = useState(false);
  const [executionUnknown, setExecutionUnknown] = useState<ExecutionUnknownState | null>(null);
  const [approvalUnknown, setApprovalUnknown] = useState<number | null>(null);
  const [approvedPending, setApprovedPending] = useState<number | null>(null);
  const [forkSource, setForkSource] = useState<number | null>(null);
  const [leaveDialogOpen, setLeaveDialogOpen] = useState(false);
  const [leaveBusy, setLeaveBusy] = useState(false);
  const [leaveError, setLeaveError] = useState('');
  const [detailRefresh, setDetailRefresh] = useState(0);

  // One generation per plan view: every async result checks it before it may
  // touch state, so a slow response for plan A can never paint plan B.
  const generationRef = useRef(0);
  // Synchronous submit lock: state updates are too slow to stop a double click.
  const submitLockRef = useRef<string | null>(null);
  // base_revision is fixed when editing starts (the latest revision observed
  // then) and never silently follows a newer server revision.
  const baseRevisionRef = useRef(0);
  const observedLatestRef = useRef(0);
  const loadedRevisionRef = useRef<number | null>(null);
  const dirtyRef = useRef(false);
  const busyRef = useRef(false);
  const saveUnknownRef = useRef(false);
  const executionUnknownRef = useRef(false);
  const leaveOpenRef = useRef(false);
  const leaveBusyRef = useRef(false);
  const approvalUnknownRef = useRef<number | null>(null);
  const pendingLeaveRef = useRef<{ proceed: () => void; resolve: (value: boolean) => void } | null>(null);

  useEffect(() => () => {
    generationRef.current += 1;
    const pending = pendingLeaveRef.current;
    pendingLeaveRef.current = null;
    pending?.resolve(false);
  }, []);

  const dirty = useMemo(
    () => isFormDirty(form, baseline) || forkSource !== null,
    [form, baseline, forkSource],
  );
  const isHistoryRevision = selected !== null && latestRevision > 0 && selected < latestRevision;
  // Learning about a newer server revision (409) must not lock the draft the
  // user is being asked to keep: only an explicitly opened old snapshot is
  // read-only, and the conflict banner owns the comparison instead.
  const readOnly = isHistoryRevision && forkSource !== selected && !dirty && conflict === null;
  // An explicit fork is a request to save this snapshot as a new revision, so
  // it stays savable even though nothing was edited yet.
  const savable = dirty;

  useEffect(() => {
    dirtyRef.current = dirty;
  }, [dirty]);
  useEffect(() => {
    busyRef.current = busy;
  }, [busy]);
  useEffect(() => {
    saveUnknownRef.current = saveUnknown;
  }, [saveUnknown]);
  useEffect(() => {
    executionUnknownRef.current = executionUnknown !== null;
  }, [executionUnknown]);
  useEffect(() => {
    leaveOpenRef.current = leaveDialogOpen;
  }, [leaveDialogOpen]);
  useEffect(() => {
    leaveBusyRef.current = leaveBusy;
  }, [leaveBusy]);
  useEffect(() => {
    approvalUnknownRef.current = approvalUnknown;
  }, [approvalUnknown]);

  const loadRevisions = useCallback(
    async (gen: number) => {
      if (gen !== generationRef.current) return;
      try {
        const payload = await listRevisions(planId);
        if (gen !== generationRef.current) return;
        const list = payload?.revisions ?? [];
        setRevisions(list);
        setCurrentRevision(payload?.current_revision ?? 0);
        const observed = Math.max(
          Number(payload?.latest_revision ?? 0) || 0,
          list.reduce((max, entry) => Math.max(max, entry?.revision ?? 0), 0),
        );
        observedLatestRef.current = Math.max(observedLatestRef.current, observed);
        setLatestRevision((previous) => Math.max(previous, observed));
        setSelected((previous) => previous ?? (list.length > 0 ? list[list.length - 1].revision : null));
      } catch (err) {
        if (gen !== generationRef.current) return;
        setError(err instanceof Error ? err.message : '加载 revision 失败');
      } finally {
        if (gen === generationRef.current) setLoading(false);
      }
    },
    [planId],
  );

  // Plan generation: switching plans discards every in-flight result below.
  useEffect(() => {
    generationRef.current += 1;
    const gen = generationRef.current;
    setRevisions([]);
    setCurrentRevision(0);
    setLatestRevision(0);
    setSelected(null);
    setDetail(null);
    setForm(null);
    setBaseline(null);
    setValidation(null);
    setFieldIssues([]);
    setDiff([]);
    setDiffFrom(null);
    setDiffTo(null);
    setConflict(null);
    setSaveUnknown(false);
    setExecutionUnknown(null);
    setApprovalUnknown(null);
    setApprovedPending(null);
    setForkSource(null);
    setError('');
    setNotice('');
    setLoading(true);
    const pending = pendingLeaveRef.current;
    pendingLeaveRef.current = null;
    pending?.resolve(false);
    leaveOpenRef.current = false;
    setLeaveDialogOpen(false);
    setLeaveBusy(false);
    setLeaveError('');
    // The old plan's request will never clear its own busy flag (the
    // generation check fails), so both the lock and the flag are reset here.
    setBusy(false);
    setBusyLabel('');
    observedLatestRef.current = 0;
    baseRevisionRef.current = 0;
    loadedRevisionRef.current = null;
    // The in-flight request belongs to the old plan and its `finally` will no
    // longer run (its generation check fails), so the lock must be cleared
    // here or the new plan can never submit.
    submitLockRef.current = null;
    void loadRevisions(gen);
  }, [planId, loadRevisions]);

  useEffect(() => {
    if (selected === null) return;
    if (loadedRevisionRef.current === selected) {
      // Just saved this revision: keep the exact draft the user submitted as
      // the new baseline instead of re-deriving it from the server snapshot.
      loadedRevisionRef.current = null;
      return;
    }
    const gen = generationRef.current;
    let cancelled = false;
    setDetail(null);
    setForm(null);
    setBaseline(null);
    setActiveNodeId('');
    setValidation(null);
    setFieldIssues([]);
    setError('');
    getRevision(planId, selected)
      .then((revision) => {
        if (cancelled || gen !== generationRef.current) return;
        setDetail(revision);
        if (revision?.graph) {
          const nextForm = formFromGraph(revision.graph);
          setForm(nextForm);
          setBaseline(cloneForm(nextForm));
          setActiveNodeId(revision.graph.nodes?.[0]?.id ?? '');
          baseRevisionRef.current = observedLatestRef.current > 0 ? observedLatestRef.current : selected;
        }
      })
      .catch((err) => {
        if (cancelled || gen !== generationRef.current) return;
        setError(err instanceof Error ? err.message : '加载 revision 详情失败');
      });
    return () => {
      cancelled = true;
    };
  }, [planId, selected, detailRefresh]);

  const activeNode = useMemo(
    () => form?.nodes.find((node) => node.id === activeNodeId) ?? null,
    [form, activeNodeId],
  );

  const updateForm = useCallback((next: DraftForm) => {
    setForm(next);
    // Any edit invalidates the previous validation result (R-04).
    setValidation(null);
    setFieldIssues([]);
    setHighlightedField('');
  }, []);

  const updateNode = (patch: Partial<DraftNode>) => {
    if (!form || !activeNode) return;
    updateForm({
      ...form,
      nodes: form.nodes.map((node) => (node.id === activeNode.id ? { ...node, ...patch } : node)),
    });
  };

  const notifyExecutionStarted = useCallback(
    (revision: number) => {
      if (onExecutionStarted) onExecutionStarted(revision);
      else onApplied?.();
    },
    [onExecutionStarted, onApplied],
  );

  const validateRevisionFor = useCallback(
    async (revision: number, gen: number) => {
      try {
        const result = await validateRevision(planId, revision);
        if (gen !== generationRef.current) return;
        const valid = result?.valid === true;
        setValidation({ revision, valid, issues: result?.errors ?? [] });
        setNotice(valid ? `r${revision} 校验通过` : `r${revision} 校验未通过`);
      } catch (err) {
        if (gen !== generationRef.current) return;
        const info = classifyApiError(err, '校验失败');
        setValidation(null);
        setError(`校验请求失败：${describeApiError(info, '校验失败')}`);
      }
    },
    [planId],
  );

  interface SaveOptions {
    validateAfter: boolean;
    fromLeaveDialog?: boolean;
  }

  const saveDraft = useCallback(
    async ({ validateAfter, fromLeaveDialog = false }: SaveOptions): Promise<{ ok: boolean; revision?: number }> => {
      if (!form || !detail?.graph) return { ok: false };
      if (submitLockRef.current) return { ok: false };
      if (readOnly) {
        setError('历史版本只读；请先“基于此版本创建草稿”。');
        return { ok: false };
      }
      if (saveUnknownRef.current || executionUnknownRef.current || approvalUnknownRef.current !== null) {
        setError('上次提交结果待确认，请先核对服务端状态。');
        return { ok: false };
      }
      const issues = collectFormIssues(form, baseline);
      if (issues.length > 0) {
        setFieldIssues(issues);
        setError(`有 ${issues.length} 处字段不合法，未提交保存。`);
        if (fromLeaveDialog) setLeaveError(`有 ${issues.length} 处字段不合法，请先修正后再保存。`);
        return { ok: false };
      }
      const base = baseRevisionRef.current;
      if (!(base > 0)) {
        setError('尚未观察到服务端最新版本号，暂时无法保存；请刷新版本列表。');
        return { ok: false };
      }
      const gen = generationRef.current;
      const submittedForm = cloneForm(form);
      submitLockRef.current = 'save';
      setBusy(true);
      setBusyLabel('保存中');
      setError('');
      setNotice('');
      setFieldIssues([]);
      try {
        const graph = graphFromForm(detail.graph, submittedForm);
        const created = await createRevision(planId, {
          base_revision: base,
          reason: 'edited in review panel',
          graph: graph as unknown as Record<string, unknown>,
        });
        if (gen !== generationRef.current) return { ok: false };
        const createdNumber = Number(
          (created as { revision?: { revision?: number } } | undefined)?.revision?.revision ?? 0,
        );
        if (!(createdNumber > 0)) {
          setSaveUnknown(true);
          setNotice('保存请求已返回，但没有可用的新版本号；草稿保留在本地，请核对服务端版本。');
          return { ok: false };
        }
        // The response number is authoritative for everything that follows.
        setBaseline(cloneForm(submittedForm));
        setForm(cloneForm(submittedForm));
        loadedRevisionRef.current = createdNumber;
        setSelected(createdNumber);
        setLatestRevision((previous) => Math.max(previous, createdNumber));
        observedLatestRef.current = Math.max(observedLatestRef.current, createdNumber);
        baseRevisionRef.current = createdNumber;
        setForkSource(null);
        setConflict(null);
        setValidation(null);
        setNotice(`已保存为 r${createdNumber}`);
        onSaved?.(createdNumber);
        let createdDetail: PlanRevision | null = null;
        try {
          createdDetail = await getRevision(planId, createdNumber);
        } catch {
          createdDetail = null;
        }
        if (gen !== generationRef.current) return { ok: false };
        setDetail(createdDetail ?? { ...(detail as PlanRevision), revision: createdNumber, graph });
        void loadRevisions(gen);
        if (validateAfter) await validateRevisionFor(createdNumber, gen);
        return { ok: true, revision: createdNumber };
      } catch (err) {
        if (gen !== generationRef.current) return { ok: false };
        const info = classifyApiError(err, '保存草稿失败');
        if (isRevisionConflict(info)) {
          const serverRevision = conflictServerRevision(info);
          setConflict({ serverRevision });
          setError(describeApiError(info, '保存草稿失败'));
          setNotice('本地修改已保留；请比较服务端新版本后再决定，不会自动覆盖。');
          if (fromLeaveDialog) setLeaveError('服务端已有更新版本，本地修改已保留；请先比较再决定。');
          void loadRevisions(gen);
        } else if (info.code === 'validation_failed') {
          const serverIssues = validationIssuesFromPayload(info);
          setValidation({ revision: selected ?? 0, valid: false, issues: serverIssues });
          setError(describeApiError(info, '保存草稿失败'));
          if (fromLeaveDialog) setLeaveError('服务端校验未通过，草稿已保留。');
        } else if (info.unknownOutcome) {
          setSaveUnknown(true);
          setNotice('保存请求结果不明；草稿保留在本地，请先核对服务端版本，不会自动重发。');
          if (fromLeaveDialog) setLeaveError('保存结果不明，草稿已保留；请先核对服务端版本。');
        } else {
          setError(`保存草稿失败：${describeApiError(info, '保存草稿失败')}`);
          if (fromLeaveDialog) setLeaveError(describeApiError(info, '保存草稿失败'));
        }
        return { ok: false };
      } finally {
        if (gen === generationRef.current) {
          submitLockRef.current = null;
          setBusy(false);
          setBusyLabel('');
        }
      }
    },
    [baseline, detail, form, loadRevisions, onSaved, planId, readOnly, selected, validateRevisionFor],
  );

  const handleSaveDraft = async () => {
    if (!savable) {
      setNotice('当前没有未保存的修改。');
      return;
    }
    await saveDraft({ validateAfter: false });
  };

  const handleValidate = async () => {
    if (selected === null || readOnly || !detail?.graph || executionUnknown || approvalUnknown !== null) return;
    if (submitLockRef.current) return;
    if (dirty) {
      // R-01: save first, then validate the revision the server returned.
      await saveDraft({ validateAfter: true });
      return;
    }
    if (saveUnknown) {
      setError('保存结果待确认，请先核对服务端版本。');
      return;
    }
    const gen = generationRef.current;
    submitLockRef.current = 'validate';
    setBusy(true);
    setBusyLabel('校验中');
    setError('');
    try {
      await validateRevisionFor(selected, gen);
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const startExecution = async (target: number, gen: number) => {
    // Record what the server had already published before this submit so an
    // old same-revision start event can never be read as this execution.
    const boundary = await captureEventBoundary(planId, identity);
    if (gen !== generationRef.current) return;
    try {
      await executeRevision(planId, target);
      if (gen !== generationRef.current) return;
      setApprovedPending(null);
      setExecutionUnknown(null);
      setNotice(`r${target} 已批准并开始执行`);
      notifyExecutionStarted(target);
    } catch (err) {
      if (gen !== generationRef.current) return;
      const info = classifyApiError(err, '启动失败');
      const reason = describeApiError(info, '启动失败');
      if (!info.unknownOutcome) {
        // A definite rejection (409 plan_running, 400 plan_paused, ...) proves
        // this request started nothing, so the approval still stands and the
        // user may re-run only the execute step.
        setExecutionUnknown(null);
        setApprovedPending(target);
        setError(`r${target} 已批准，启动失败：${reason}`);
        setNotice('本次启动已明确失败；可以只重试启动，不需要重新批准。');
        return;
      }
      // Network failure or 5xx: the start may have been accepted. Ask the
      // server first, never retry, and keep the lock unless a start is proven.
      // The retry affordance is withdrawn for exactly this reason: offering
      // "重试启动" while the first start may be running is what duplicates it.
      // `handleReconcileExecution` restores it once a start is ruled out.
      setApprovedPending(null);
      setExecutionUnknown({ revision: target, boundary });
      setError(`启动 r${target} 的结果不明：${reason}`);
      setNotice('不会重复启动；正在核对服务端状态。');
      const observation = await reconcileExecution({ planId, revision: target, boundary, identity });
      if (gen !== generationRef.current) return;
      if (observation.state === 'started') {
        setExecutionUnknown(null);
        setApprovedPending(null);
        setNotice(`r${target} 已在服务端运行：${observation.detail}`);
        notifyExecutionStarted(target);
        return;
      }
      // A pending snapshot is not proof that nothing started, so the lock and
      // the banner stay until the user reconciles again.
      setNotice(`已核对服务端状态，仍无法确认启动：${observation.detail} 不会重复启动。`);
    }
  };

  const approveBlockers = useMemo(() => {
    if (busy) return '当前请求进行中';
    if (saveUnknown) return '保存结果待确认，请先核对服务端版本';
    if (approvalUnknown !== null) return '批准结果待确认，请先核对服务端状态';
    if (executionUnknown) return '启动结果待确认，请先核对服务端状态';
    if (readOnly) return '历史版本只读，不能批准运行';
    if (dirty) return '有未保存修改，请先保存并校验';
    if (conflict) return `服务端已有 r${conflict.serverRevision || latestRevision} 版本，请先比较`;
    if (selected === null) return '未选择版本';
    if (latestRevision > 0 && selected !== latestRevision) return `只能批准并运行最新版本 r${latestRevision}`;
    if (!validation || validation.revision !== selected) return '请先校验该版本';
    if (!validation.valid) return '校验未通过，不能批准运行';
    return '';
  }, [approvalUnknown, busy, conflict, dirty, executionUnknown, latestRevision, readOnly, saveUnknown, selected, validation]);

  const runApprove = async (target: number) => {
    const gen = generationRef.current;
    submitLockRef.current = 'approve';
    setBusy(true);
    setBusyLabel('批准中');
    setError('');
    setNotice('');
    try {
      await approveRevision(planId, target);
    } catch (err) {
      if (gen === generationRef.current) {
        const info = classifyApiError(err, '批准失败');
        setError(`批准 r${target} 失败：${describeApiError(info, '批准失败')}`);
        if (info.unknownOutcome) {
          // A 5xx/timeout may have moved the review gate. Read the server's
          // record before offering anything that could approve twice.
          setApprovalUnknown(target);
          setNotice('批准结果不明（服务端可能已批准）；已停止启动，请先核对服务端状态。');
        }
      }
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
      void loadRevisions(gen);
      return;
    }
    if (gen !== generationRef.current) return;
    // Approval and execution are two requests: remember the approval so a
    // failed start can be retried on the same revision without re-approving.
    setApprovedPending(target);
    setBusyLabel('启动中');
    try {
      await startExecution(target, gen);
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
      void loadRevisions(gen);
    }
  };

  const handleApproveAndRun = async () => {
    const target = selected;
    if (target === null || submitLockRef.current) return;
    // The blockers apply to the execute-only retry as well: an unknown startup
    // or an outstanding reconcile must never be bypassed by "retry".
    if (approveBlockers) {
      setError(approveBlockers);
      return;
    }
    if (approvedPending === target) {
      await retryExecution(target);
      return;
    }
    await runApprove(target);
  };

  const retryExecution = async (target: number) => {
    if (submitLockRef.current) return;
    // The approved revision is frozen, so a draft edited after the approval
    // must be saved and re-validated before the start is retried: otherwise
    // the executed graph would not be the one the user is looking at.
    if (dirty) {
      setError('有未保存修改：请先保存并重新校验该版本，再重试启动。');
      return;
    }
    const gen = generationRef.current;
    submitLockRef.current = 'execute';
    setBusy(true);
    setBusyLabel('启动中');
    setError('');
    setNotice('');
    try {
      await startExecution(target, gen);
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
      void loadRevisions(gen);
    }
  };

  const handleReconcileExecution = async () => {
    const pending = executionUnknown;
    if (!pending || submitLockRef.current) return;
    const gen = generationRef.current;
    submitLockRef.current = 'reconcile-execution';
    setBusy(true);
    setBusyLabel('核对中');
    setError('');
    try {
      const observation = await reconcileExecution({
        planId,
        revision: pending.revision,
        boundary: pending.boundary,
        identity,
      });
      if (gen !== generationRef.current) return;
      if (observation.state === 'started') {
        setExecutionUnknown(null);
        setApprovedPending(null);
        setNotice(`已确认 r${pending.revision} 已启动：${observation.detail}`);
        notifyExecutionStarted(pending.revision);
      } else if (observation.state === 'not_started') {
        setExecutionUnknown(null);
        setApprovedPending(pending.revision);
        setNotice(`已确认 r${pending.revision} 未启动：${observation.detail} 可以只重试启动，不需要重新批准。`);
      } else {
        // Still unproven: keep the lock and the retention notice, so the user
        // cannot "retry" a start that may already be running.
        setError(`仍无法确认 r${pending.revision} 的启动状态：${observation.detail}`);
        setNotice('核对结果不明，仍不会重复启动；请稍后再次核对计划状态。');
      }
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const handleReconcileSave = async () => {
    if (!form || submitLockRef.current) return;
    const gen = generationRef.current;
    submitLockRef.current = 'reconcile-save';
    setBusy(true);
    setBusyLabel('核对中');
    setError('');
    const signature = formSignature(form);
    try {
      const observation = await reconcileSaveOutcome({
        planId,
        baseRevision: baseRevisionRef.current,
        signature,
      });
      if (gen !== generationRef.current) return;
      if (observation.state === 'found' && observation.revision) {
        const revision = observation.revision;
        setSaveUnknown(false);
        setBaseline(cloneForm(form));
        loadedRevisionRef.current = revision;
        setSelected(revision);
        setLatestRevision((previous) => Math.max(previous, revision));
        observedLatestRef.current = Math.max(observedLatestRef.current, revision);
        baseRevisionRef.current = revision;
        setNotice(`已找到服务端 r${revision} 与本地内容一致，并作为当前版本。`);
        onSaved?.(revision);
        try {
          const loaded = await getRevision(planId, revision);
          if (gen === generationRef.current && loaded) setDetail(loaded);
        } catch {
          // The revision is already identified by content; a detail reload
          // failure must not undo the reconciliation.
        }
        void loadRevisions(gen);
      } else if (observation.state === 'absent') {
        // "Not found" is not proof that nothing was written: a 5xx may have
        // committed the row while the history read is still stale. The draft
        // therefore stays frozen, and reopening saving needs an explicit,
        // separately confirmed decision — never an automatic retry.
        setNotice(
          '服务端版本列表暂未包含与本地内容一致的版本，但保存结果仍不能确定；草稿保持冻结，请稍后再次核对。',
        );
        void loadRevisions(gen);
      } else {
        setError(`核对保存结果失败：${observation.detail}`);
      }
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const handleReconcileApproval = async () => {
    const target = approvalUnknown;
    if (target === null || submitLockRef.current) return;
    const gen = generationRef.current;
    submitLockRef.current = 'reconcile-approval';
    setBusy(true);
    setBusyLabel('核对中');
    setError('');
    try {
      const observation = await reconcileApproval({ planId, revision: target });
      if (gen !== generationRef.current) return;
      if (observation.state === 'approved') {
        setApprovalUnknown(null);
        setApprovedPending(target);
        setNotice(`已确认 r${target} 已批准：${observation.detail} 可以只重试启动。`);
      } else if (observation.state === 'not_approved') {
        setApprovalUnknown(null);
        setApprovedPending(null);
        setNotice(`已确认 r${target} 未批准：${observation.detail} 可以重新批准。`);
      } else {
        setError(`仍无法确认 r${target} 的批准状态：${observation.detail}`);
        setNotice('核对结果不明，不会重新批准；请稍后再次核对。');
      }
      void loadRevisions(gen);
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const handleReject = async () => {
    if (selected === null || submitLockRef.current) return;
    const target = selected;
    const gen = generationRef.current;
    submitLockRef.current = 'reject';
    setBusy(true);
    setBusyLabel('拒绝中');
    setError('');
    try {
      await rejectRevision(planId, target);
      if (gen !== generationRef.current) return;
      setNotice(`已拒绝 r${target}`);
      await loadRevisions(gen);
    } catch (err) {
      if (gen !== generationRef.current) return;
      const info = classifyApiError(err, '拒绝失败');
      setError(`拒绝 r${target} 失败：${describeApiError(info, '拒绝失败')}`);
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const handleCompare = async (from: number, to: number) => {
    if (submitLockRef.current) return;
    const gen = generationRef.current;
    submitLockRef.current = 'diff';
    setBusy(true);
    setBusyLabel('加载差异');
    setError('');
    try {
      const entries = await getPlanDiff(planId, from, to);
      if (gen !== generationRef.current) return;
      setDiff(entries ?? []);
      setDiffFrom(from);
      setDiffTo(to);
    } catch (err) {
      if (gen !== generationRef.current) return;
      setError(err instanceof Error ? err.message : 'diff 加载失败');
    } finally {
      if (gen === generationRef.current) {
        submitLockRef.current = null;
        setBusy(false);
        setBusyLabel('');
      }
    }
  };

  const handleForkFromHistory = () => {
    if (selected === null || !form) return;
    baseRevisionRef.current = observedLatestRef.current;
    setForkSource(selected);
    setNotice(`已基于 r${selected} 创建本地草稿；保存时以最新 r${latestRevision} 为基准。`);
    if (latestRevision > selected) void handleCompare(selected, latestRevision);
  };

  const handleAdoptServerBase = () => {
    const serverRevision = conflict?.serverRevision || latestRevision;
    if (!(serverRevision > 0)) return;
    baseRevisionRef.current = serverRevision;
    setConflict(null);
    setNotice(`已改用服务端 r${serverRevision} 作为保存基准；本地草稿保持不变，请确认差异后再保存。`);
  };

  const closeLeaveDialog = useCallback((result: boolean) => {
    const pending = pendingLeaveRef.current;
    pendingLeaveRef.current = null;
    setLeaveDialogOpen(false);
    setLeaveBusy(false);
    setLeaveError('');
    pending?.resolve(result);
  }, []);

  const requestLeave = useCallback(
    (proceed: () => void): Promise<boolean> => {
      if (leaveOpenRef.current) return Promise.resolve(false);
      if (submitLockRef.current || busyRef.current) {
        setNotice('当前请求进行中，完成前不会离开或切换版本。');
        return Promise.resolve(false);
      }
      if (saveUnknownRef.current) {
        setError('保存结果待确认，请先核对服务端版本。');
        return Promise.resolve(false);
      }
      if (executionUnknownRef.current) {
        setError('启动结果待确认，请先核对服务端状态。');
        return Promise.resolve(false);
      }
      if (approvalUnknownRef.current) {
        setError('批准结果待确认，请先核对服务端状态。');
        return Promise.resolve(false);
      }
      if (!dirtyRef.current) {
        proceed();
        return Promise.resolve(true);
      }
      return new Promise<boolean>((resolve) => {
        // A second request while the prompt is open must not replace the first
        // continuation; it is refused, and the first one still owns the prompt.
        if (pendingLeaveRef.current) {
          resolve(false);
          return;
        }
        pendingLeaveRef.current = { proceed, resolve };
        setLeaveError('');
        setLeaveDialogOpen(true);
      });
    },
    [],
  );

  const handleSelectRevision = (revision: number) => {
    if (busy || revision === selected) return;
    void requestLeave(() => {
      setForkSource(null);
      setApprovedPending(null);
      setConflict(null);
      setDiff([]);
      setSelected(revision);
    });
  };

  const handleRefresh = () => {
    if (busy) return;
    void requestLeave(() => {
      const gen = generationRef.current;
      void loadRevisions(gen).then(() => {
        if (gen === generationRef.current) setDetailRefresh((value) => value + 1);
      });
    });
  };

  const handleLeaveKeepEditing = () => closeLeaveDialog(false);

  const handleLeaveDiscard = () => {
    const pending = pendingLeaveRef.current;
    if (baseline) setForm(cloneForm(baseline));
    setValidation(null);
    setFieldIssues([]);
    setHighlightedField('');
    pendingLeaveRef.current = null;
    setLeaveDialogOpen(false);
    setLeaveBusy(false);
    setLeaveError('');
    pending?.proceed();
    pending?.resolve(true);
  };

  const handleLeaveSaveAndContinue = async () => {
    if (leaveBusyRef.current || submitLockRef.current) return;
    const gen = generationRef.current;
    setLeaveBusy(true);
    setLeaveError('');
    const result = await saveDraft({ validateAfter: false, fromLeaveDialog: true });
    if (gen !== generationRef.current) return;
    setLeaveBusy(false);
    if (!result.ok) return;
    const pending = pendingLeaveRef.current;
    pendingLeaveRef.current = null;
    setLeaveDialogOpen(false);
    pending?.proceed();
    pending?.resolve(true);
  };

  useEffect(() => {
    if (!onRegisterLeaveGuard) return;
    onRegisterLeaveGuard(requestLeave);
    return () => onRegisterLeaveGuard(null);
  }, [onRegisterLeaveGuard, requestLeave]);

  // Escape is routed through the same guard as the close button.
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (leaveOpenRef.current) return;
      event.preventDefault();
      void requestLeave(onClose);
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose, requestLeave]);

  // Escape inside the leave dialog is owned by the dialog itself (`onCancel`
  // plus its own keydown handler, both with preventDefault). A second window
  // listener here would close the prompt twice; the guard above already
  // refuses any other leave path while the prompt is open.

  // Native browser prompt only; desktop window close has no bridge hook yet.
  useEffect(() => {
    if (!dirty && !busy) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '有未保存的计划修改';
      return '有未保存的计划修改';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [busy, dirty]);

  const stateChip = (() => {
    if (busy) return busyLabel || '处理中';
    if (saveUnknown) return '保存结果待确认';
    if (approvalUnknown !== null) return '批准结果待确认';
    if (executionUnknown) return '启动结果待确认';
    if (conflict) return '保存冲突待处理';
    if (dirty) return '有未保存修改';
    return '已保存';
  })();

  const localIssueFor = (field: string, nodeId?: string): FormIssue | undefined =>
    fieldIssues.find((issue) => issue.field === field && (issue.nodeId ?? '') === (nodeId ?? ''));

  const serverIssueFor = (field: string, nodeId?: string): ValidationIssue | undefined => {
    return validation?.issues.find(
      (issue) => (issue.field ?? '') === field && (issue.node_id ?? '') === (nodeId ?? ''),
    );
  };

  // While a save is unconfirmed the local content is the only evidence of what
  // was submitted, so the form is frozen until reconciliation resolves it.
  const formDisabled = readOnly || busy || saveUnknown || executionUnknown !== null || approvalUnknown !== null;
  const activeInputsParse = activeNode ? parseInputsText(activeNode.inputsText) : null;
  const approveLabel =
    selected !== null && approvedPending === selected ? `重试启动 r${selected}` : selected !== null ? `批准并运行 r${selected}` : '批准并运行';

  return (
    <div className="relative flex h-full max-h-full min-h-0 flex-col overflow-hidden rounded-3xl bg-paper text-ink">
      <header className="flex min-h-12 shrink-0 flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-proof" />
          <span className="text-[17px] font-semibold tracking-[-0.02em]">计划审阅</span>
          <span className="text-[9px] text-muted">版本 · 差异 · 审阅</span>
        </div>
        <div className="flex items-center gap-2">
          <span
            data-testid="review-state-chip"
            className={
              'journal-num rounded-full border px-2 py-0.5 text-[10px] ' +
              (dirty || saveUnknown || executionUnknown || conflict
                ? 'border-warn/60 text-warn'
                : 'border-line text-muted')
            }
          >
            {stateChip}
          </span>
          <button
            type="button"
            onClick={() => void requestLeave(onClose)}
            aria-label="关闭审阅面板"
            className="flex h-11 w-11 items-center justify-center rounded-lg border border-line text-ink-soft transition-colors hover:border-accent hover:text-accent sm:h-8 sm:w-8"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      </header>

      {error && <div className="border-b border-danger/40 bg-danger/10 px-4 py-2 text-[12px] text-danger">{error}</div>}
      {notice && <div className="border-b border-ok/40 bg-ok/10 px-4 py-2 text-[12px] text-ink">{notice}</div>}

      {conflict && (
        <div className="flex flex-wrap items-center gap-2 border-b border-warn/50 bg-warn/10 px-4 py-2 text-[11px] text-ink">
          <AlertTriangle className="h-3.5 w-3.5 text-warn" />
          <span>
            服务端已有 r{conflict.serverRevision || latestRevision}，本地修改仍然保留；不会自动覆盖或重试。
          </span>
          <button
            type="button"
            disabled={busy}
            onClick={() => void handleCompare(baseRevisionRef.current, conflict.serverRevision || latestRevision)}
            className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] hover:border-accent hover:text-accent disabled:opacity-40"
          >
            <GitCompare className="h-3 w-3" /> 比较服务端版本
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={handleAdoptServerBase}
            className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] hover:border-accent hover:text-accent disabled:opacity-40"
          >
            以服务端 r{conflict.serverRevision || latestRevision} 为基准继续编辑
          </button>
        </div>
      )}

      {saveUnknown && (
        <div className="flex flex-wrap items-center gap-2 border-b border-warn/50 bg-warn/10 px-4 py-2 text-[11px] text-ink">
          <AlertTriangle className="h-3.5 w-3.5 text-warn" />
          <span>保存结果待确认：草稿保留在本地，批准与执行已禁用，不会自动重发保存请求。</span>
          <button
            type="button"
            disabled={busy}
            onClick={() => void handleReconcileSave()}
            className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] hover:border-accent hover:text-accent disabled:opacity-40"
          >
            核对服务端版本
          </button>
        </div>
      )}

      {executionUnknown && (
        <div className="flex flex-wrap items-center gap-2 border-b border-warn/50 bg-warn/10 px-4 py-2 text-[11px] text-ink">
          <AlertTriangle className="h-3.5 w-3.5 text-warn" />
          <span>r{executionUnknown.revision} 的启动结果待确认：不会重复启动，请先核对服务端状态。</span>
          <button
            type="button"
            disabled={busy}
            onClick={() => void handleReconcileExecution()}
            className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] hover:border-accent hover:text-accent disabled:opacity-40"
          >
            核对启动状态
          </button>
        </div>
      )}

      {approvalUnknown !== null && (
        <div className="flex flex-wrap items-center gap-2 border-b border-warn/50 bg-warn/10 px-4 py-2 text-[11px] text-ink">
          <AlertTriangle className="h-3.5 w-3.5 text-warn" />
          <span>r{approvalUnknown} 的批准结果待确认：不会重新批准或启动，请先核对服务端状态。</span>
          <button
            type="button"
            disabled={busy}
            onClick={() => void handleReconcileApproval()}
            className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] hover:border-accent hover:text-accent disabled:opacity-40"
          >
            核对批准状态
          </button>
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <aside className="max-h-28 w-full shrink-0 overflow-auto border-b border-line md:max-h-none md:w-56 md:border-r md:border-b-0">
          <div className="flex items-center justify-between px-3 py-2">
            <span className="text-[9px] font-semibold text-muted">历史 revision</span>
            <button
              type="button"
              onClick={handleRefresh}
              aria-label="刷新 revision"
              className="text-ink-soft hover:text-ink"
            >
              <RefreshCw className={'h-3 w-3 ' + (loading ? 'animate-spin' : '')} />
            </button>
          </div>
          <ul className="flex min-w-max md:block md:min-w-0">
            {revisions.map((revision) => (
              <li key={revision.id}>
                <button
                  type="button"
                  disabled={busy}
                  aria-current={selected === revision.revision ? 'true' : undefined}
                  onClick={() => handleSelectRevision(revision.revision)}
                  className={
                    'flex w-full flex-col gap-0.5 border-l-2 px-3 py-2 text-left disabled:opacity-60 ' +
                    (selected === revision.revision ? 'border-proof bg-surface' : 'border-transparent hover:bg-surface')
                  }
                >
                  <span className="flex items-center justify-between text-[12px] font-medium">
                    <span>r{revision.revision}</span>
                    <span className="journal-num text-[10px] text-muted">{revisionStatusLabel(revision.status)}</span>
                  </span>
                  <span className="truncate text-[11px] text-muted">{revision.reason || '（无说明）'}</span>
                  <span className="journal-num text-[9px] text-muted">{formatTime(revision.created_at)}</span>
                </button>
              </li>
            ))}
          </ul>
          {selected !== null && selected > 1 && (
            <div className="border-t border-line px-3 py-2">
              <button
                type="button"
                disabled={busy}
                onClick={() => void handleCompare(selected - 1, selected)}
                className="inline-flex items-center gap-1 text-[11px] text-ink-soft hover:text-ink disabled:opacity-40"
              >
                <GitCompare className="h-3 w-3" /> 与 r{selected - 1} 比较
              </button>
            </div>
          )}
          {isHistoryRevision && (
            <div className="border-t border-line px-3 py-2">
              <button
                type="button"
                disabled={busy}
                onClick={() => void handleCompare(selected as number, latestRevision)}
                className="inline-flex items-center gap-1 text-[11px] text-ink-soft hover:text-ink disabled:opacity-40"
              >
                <GitCompare className="h-3 w-3" /> 与最新 r{latestRevision} 比较
              </button>
            </div>
          )}
        </aside>

        <section className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
          <div className="border-b border-line px-4 py-2">
            <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted">
              <span>正在编辑 r{selected ?? '—'}</span>
              <span>·</span>
              <span>当前执行 r{currentRevision || '—'}</span>
              <span>·</span>
              <span>最新 r{latestRevision || '—'}</span>
              {dirty && <span className="text-warn">· 本地草稿有未保存修改</span>}
              {readOnly && <span className="text-warn">· 历史版本只读</span>}
              {forkSource === selected && selected !== null && <span className="text-warn">· 基于 r{forkSource} 的新草稿</span>}
              {detail?.hash && (
                <>
                  <span>·</span>
                  <span className="journal-num">{detail.hash.slice(0, 12)}</span>
                </>
              )}
            </div>
            {readOnly && (
              <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] text-muted">
                <span>历史版本默认只读，避免把旧快照无提示覆盖为最新草稿。</span>
                <button
                  type="button"
                  disabled={busy}
                  onClick={handleForkFromHistory}
                  className="inline-flex h-6 items-center gap-1 rounded-lg border border-line px-2 text-[10px] text-ink-soft hover:border-accent hover:text-accent disabled:opacity-40"
                >
                  <GitFork className="h-3 w-3" /> 基于此版本创建草稿
                </button>
              </div>
            )}
          </div>

          {form && (
            <div className="border-b border-line bg-surface px-4 py-2.5">
              <div className="flex flex-wrap items-end gap-3">
                <div className="mr-1">
                  <div className="text-[9px] font-semibold text-muted">预算 / Budget</div>
                  <div className="mt-0.5 text-[10px] text-muted">保存并校验后生效</div>
                </div>
                <label className="block text-[10px] text-muted">
                  最大成本 USD
                  <input
                    type="text"
                    inputMode="decimal"
                    aria-label="最大成本 USD"
                    aria-invalid={Boolean(localIssueFor('budget.max_cost_usd') || serverIssueFor('budget.max_cost_usd'))}
                    disabled={formDisabled}
                    value={form.maxCostUSD}
                    onChange={(event) => updateForm({ ...form, maxCostUSD: event.target.value })}
                    className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink disabled:opacity-60"
                  />
                </label>
                <label className="block text-[10px] text-muted">
                  最大 tokens
                  <input
                    type="text"
                    inputMode="numeric"
                    aria-label="最大 tokens"
                    aria-invalid={Boolean(localIssueFor('budget.max_tokens') || serverIssueFor('budget.max_tokens'))}
                    disabled={formDisabled}
                    value={form.maxTokens}
                    onChange={(event) => updateForm({ ...form, maxTokens: event.target.value })}
                    className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink disabled:opacity-60"
                  />
                </label>
                <label className="block text-[10px] text-muted">
                  最大时长（秒）
                  <input
                    type="text"
                    inputMode="numeric"
                    aria-label="最大时长（秒）"
                    aria-invalid={Boolean(
                      localIssueFor('budget.max_duration_seconds') || serverIssueFor('budget.max_duration_seconds'),
                    )}
                    disabled={formDisabled}
                    value={form.maxDurationSeconds}
                    onChange={(event) => updateForm({ ...form, maxDurationSeconds: event.target.value })}
                    className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink disabled:opacity-60"
                  />
                </label>
              </div>
              {(localIssueFor('budget.max_cost_usd') ||
                localIssueFor('budget.max_tokens') ||
                localIssueFor('budget.max_duration_seconds') ||
                localIssueFor('title')) && (
                <ul className="mt-1 space-y-0.5 text-[10px] text-danger">
                  {localIssueFor('title') && <li>{localIssueFor('title')?.message}</li>}
                  {localIssueFor('budget.max_cost_usd') && <li>{localIssueFor('budget.max_cost_usd')?.message}</li>}
                  {localIssueFor('budget.max_tokens') && <li>{localIssueFor('budget.max_tokens')?.message}</li>}
                  {localIssueFor('budget.max_duration_seconds') && (
                    <li>{localIssueFor('budget.max_duration_seconds')?.message}</li>
                  )}
                </ul>
              )}
            </div>
          )}

          {form && (
            <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] overflow-hidden md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] md:grid-rows-1">
              <div className="max-h-32 min-h-0 overflow-y-auto border-b border-line md:max-h-none md:border-r md:border-b-0">
                <div className="px-3 py-2 text-[9px] font-semibold text-muted">节点</div>
                <ul>
                  {form.nodes.map((node) => (
                    <li key={node.id}>
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => setActiveNodeId(node.id)}
                        className={
                          'flex w-full items-center justify-between border-l-2 px-3 py-1.5 text-left text-[12px] disabled:opacity-60 ' +
                          (activeNodeId === node.id ? 'border-proof bg-surface' : 'border-transparent hover:bg-surface')
                        }
                      >
                        <span className="min-w-0 truncate">{node.name || node.id}</span>
                        <span className="journal-num text-[9px] text-muted">{node.type}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </div>

              <div className="min-h-0 overflow-y-auto p-3">
                {activeNode ? (
                  <div className="space-y-3">
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">名称</span>
                      <input
                        value={activeNode.name}
                        aria-label="节点名称"
                        aria-invalid={Boolean(localIssueFor('name', activeNode.id))}
                        disabled={formDisabled}
                        onChange={(event) => updateNode({ name: event.target.value })}
                        className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] disabled:opacity-60"
                      />
                    </label>
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">描述</span>
                      <textarea
                        value={activeNode.description}
                        aria-label="节点描述"
                        disabled={formDisabled}
                        rows={2}
                        onChange={(event) => updateNode({ description: event.target.value })}
                        className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] disabled:opacity-60"
                      />
                    </label>
                    <div className="grid grid-cols-2 gap-2">
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">执行者</span>
                        <input
                          value={activeNode.assignedTo}
                          aria-label="执行者"
                          disabled={formDisabled}
                          onChange={(event) => updateNode({ assignedTo: event.target.value })}
                          className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] disabled:opacity-60"
                        />
                      </label>
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">超时（秒）</span>
                        <input
                          type="text"
                          inputMode="numeric"
                          value={activeNode.timeoutSeconds}
                          aria-label="超时（秒）"
                          aria-invalid={Boolean(localIssueFor('timeout_seconds', activeNode.id))}
                          disabled={formDisabled}
                          onChange={(event) => updateNode({ timeoutSeconds: event.target.value })}
                          className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] disabled:opacity-60"
                        />
                      </label>
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">重试</span>
                        <input
                          type="text"
                          inputMode="numeric"
                          value={activeNode.retryLimit}
                          aria-label="重试次数"
                          aria-invalid={Boolean(localIssueFor('retry_limit', activeNode.id))}
                          disabled={formDisabled}
                          onChange={(event) => updateNode({ retryLimit: event.target.value })}
                          className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px] disabled:opacity-60"
                        />
                      </label>
                      <label className="flex items-end gap-2 text-[11px]">
                        <input
                          type="checkbox"
                          checked={activeNode.enabled}
                          aria-label="启用节点"
                          disabled={formDisabled}
                          onChange={(event) => updateNode({ enabled: event.target.checked })}
                        />
                        <span>启用节点</span>
                      </label>
                    </div>
                    {localIssueFor('timeout_seconds', activeNode.id) && (
                      <p className="text-[10px] text-danger">{localIssueFor('timeout_seconds', activeNode.id)?.message}</p>
                    )}
                    {localIssueFor('retry_limit', activeNode.id) && (
                      <p className="text-[10px] text-danger">{localIssueFor('retry_limit', activeNode.id)?.message}</p>
                    )}
                    <fieldset className="border border-line p-2">
                      <legend className="journal-caps px-1 text-[9px] text-muted">依赖</legend>
                      <div className="max-h-32 space-y-1 overflow-y-auto">
                        {form.nodes
                          .filter((candidate) => candidate.id !== activeNode.id)
                          .map((candidate) => (
                            <label key={candidate.id} className="flex items-center gap-2 text-[11px]">
                              <input
                                type="checkbox"
                                disabled={formDisabled}
                                checked={activeNode.dependencies.includes(candidate.id)}
                                onChange={(event) => {
                                  const next = event.target.checked
                                    ? [...activeNode.dependencies, candidate.id]
                                    : activeNode.dependencies.filter((id) => id !== candidate.id);
                                  updateNode({ dependencies: next });
                                }}
                              />
                              <span className="truncate">{candidate.name || candidate.id}</span>
                            </label>
                          ))}
                      </div>
                    </fieldset>
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">输入 JSON</span>
                      <textarea
                        key={activeNode.id}
                        rows={4}
                        value={activeNode.inputsText}
                        aria-label="输入 JSON"
                        aria-invalid={Boolean(localIssueFor('inputs', activeNode.id)) || activeInputsParse?.ok === false}
                        disabled={formDisabled}
                        onChange={(event) => updateNode({ inputsText: event.target.value })}
                        className={
                          'mt-1 w-full rounded-lg border bg-surface px-2 py-1.5 font-mono text-[10.5px] disabled:opacity-60 ' +
                          (activeInputsParse?.ok === false ? 'border-danger' : 'border-line')
                        }
                      />
                      {activeInputsParse?.ok === false && (
                        <p className="mt-1 text-[10px] text-danger">{activeInputsParse.message}</p>
                      )}
                    </label>
                  </div>
                ) : (
                  <p className="text-[12px] text-muted">选择一个节点进行编辑。</p>
                )}
              </div>
            </div>
          )}

          {fieldIssues.length > 0 && (
            <div className="border-t border-line px-4 py-2">
              <div className="mb-1 text-[9px] font-semibold text-danger">字段问题</div>
              <ul className="space-y-1 text-[11px]">
                {fieldIssues.map((issue, index) => (
                  <li key={`${issue.nodeId ?? 'plan'}-${issue.field}-${index}`}>
                    <button
                      type="button"
                      onClick={() => {
                        if (issue.nodeId) setActiveNodeId(issue.nodeId);
                        setHighlightedField(issue.field);
                      }}
                      className="text-left text-danger hover:underline"
                    >
                      {issue.nodeId ? <span className="journal-num">[{issue.nodeId}] </span> : null}
                      {issue.field}
                      {' — '}
                      {issue.message}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {validation && validation.issues.length > 0 && (
            <div className="border-t border-line px-4 py-2">
              <div className="mb-1 text-[9px] font-semibold text-proof">校验问题（r{validation.revision}）</div>
              <ul className="space-y-1 text-[11px]">
                {validation.issues.map((issue, index) => (
                  <li key={index}>
                    <button
                      type="button"
                      onClick={() => {
                        if (issue.node_id) setActiveNodeId(issue.node_id);
                        if (issue.field) setHighlightedField(issue.field);
                      }}
                      className="text-left text-danger hover:underline"
                    >
                      {issue.node_id ? <span className="journal-num">[{issue.node_id}] </span> : null}
                      {issue.field ? <span className="journal-num">{issue.field} </span> : null}
                      {issue.message}
                      {issue.remediation ? <span className="text-muted"> — {issue.remediation}</span> : null}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {highlightedField && (
            <div className="border-t border-line px-4 py-1 text-[10px] text-muted">
              已定位字段 <span className="journal-num text-ink">{highlightedField}</span>
            </div>
          )}

          {diff.length > 0 && (
            <div className="border-t border-line px-4 py-2">
              <div className="journal-caps mb-1 text-[9px] text-muted">
                DIFF r{diffFrom} → r{diffTo}
              </div>
              <div className="max-h-56 overflow-y-auto">
                <table className="w-full text-[11px]">
                  <thead>
                    <tr className="border-b border-line text-left text-muted">
                      <th className="py-1 pr-2">类型</th>
                      <th className="py-1 pr-2">节点/字段</th>
                      <th className="py-1 pr-2">之前</th>
                      <th className="py-1">之后</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diff.map((entry, index) => (
                      <tr key={index} className="border-b border-line/50 align-top">
                        <td className="py-1 pr-2 whitespace-nowrap">{entry.kind}</td>
                        <td className="py-1 pr-2 whitespace-nowrap">
                          {entry.node_id ?? ''} {entry.field ?? ''}
                        </td>
                        <td className="max-w-[14rem] break-words py-1 pr-2 text-muted">{renderValue(entry.before)}</td>
                        <td className="max-w-[14rem] break-words py-1">{renderValue(entry.after)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </section>
      </div>

      <footer className="flex shrink-0 flex-col items-stretch justify-between gap-2 border-t border-line px-4 py-2 sm:flex-row sm:items-center">
        <div className="min-w-0 text-[11px] text-muted">
          {validation && validation.revision === selected ? (
            validation.valid ? (
              <span className="text-ok">校验通过（r{validation.revision}）</span>
            ) : (
              <span className="text-danger">校验未通过（r{validation.revision}）</span>
            )
          ) : (
            <span>未校验{selected !== null ? `（r${selected}）` : ''}</span>
          )}
          {approveBlockers && <span className="ml-2 text-warn">{approveBlockers}</span>}
          {approvedPending !== null && (
            <span className="ml-2 text-warn">r{approvedPending} 已批准，启动失败可只重试启动</span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 [&>button]:min-h-11 sm:[&>button]:min-h-8">
          <button
            type="button"
            disabled={formDisabled || selected === null || !detail?.graph}
            onClick={() => void handleValidate()}
            className="inline-flex h-8 items-center gap-1 rounded-lg border border-line px-2.5 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40"
          >
            <Check className="h-3 w-3" /> {dirty ? '保存并校验' : '校验'}
          </button>
          <button
            type="button"
            disabled={formDisabled || !form || !savable}
            onClick={() => void handleSaveDraft()}
            className="inline-flex h-8 items-center gap-1 rounded-lg border border-line px-2.5 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40"
          >
            {busy && busyLabel === '保存中' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />} 保存草稿
          </button>
          <button
            type="button"
            disabled={formDisabled || selected === null || !detail?.graph}
            onClick={() => void handleReject()}
            className="inline-flex h-7 items-center gap-1 border border-danger/60 px-2 text-[11px] text-danger hover:border-danger disabled:opacity-40"
          >
            <X className="h-3 w-3" /> 拒绝
          </button>
          <button
            type="button"
            disabled={busy || selected === null || Boolean(approveBlockers)}
            onClick={() => void handleApproveAndRun()}
            className="inline-flex h-8 items-center gap-1 rounded-lg bg-accent px-2.5 text-[10px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-40"
          >
            <ShieldCheck className="h-3 w-3" /> {approveLabel}
          </button>
        </div>
      </footer>

      <LeaveDraftDialog
        open={leaveDialogOpen}
        busy={leaveBusy}
        error={leaveError}
        revisionLabel={selected !== null ? String(selected) : ''}
        onSaveAndContinue={() => void handleLeaveSaveAndContinue()}
        onDiscardAndContinue={handleLeaveDiscard}
        onKeepEditing={handleLeaveKeepEditing}
      />
    </div>
  );
}

export default PlanReviewPanel;
