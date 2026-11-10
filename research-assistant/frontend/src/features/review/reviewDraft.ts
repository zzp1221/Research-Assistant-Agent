import type { PlanGraph } from '../../contracts/api';

/**
 * Draft form model for the plan review panel.
 *
 * Numeric inputs are kept as raw strings on purpose: parsing straight into
 * `number` turns an empty or malformed field into a silent `0`, which hides the
 * edit from the dirty check and would send a value the user never typed. The
 * raw text is compared verbatim (after trimming) and only converted when the
 * user explicitly saves, after validation.
 */
export interface DraftNode {
  id: string;
  name: string;
  type: string;
  description: string;
  assignedTo: string;
  timeoutSeconds: string;
  retryLimit: string;
  dependencies: string[];
  enabled: boolean;
  inputsText: string;
}

export interface DraftForm {
  title: string;
  maxCostUSD: string;
  maxTokens: string;
  maxDurationSeconds: string;
  nodes: DraftNode[];
}

export interface FormIssue {
  nodeId?: string;
  field: string;
  message: string;
}

const formatNumberInput = (value?: number | null): string =>
  typeof value === 'number' && Number.isFinite(value) ? String(value) : '';

const formatInputs = (inputs?: Record<string, unknown>): string => {
  try {
    return JSON.stringify(inputs ?? {}, null, 2);
  } catch {
    return '{}';
  }
};

export const formFromGraph = (graph: PlanGraph): DraftForm => ({
  title: graph.title ?? '',
  maxCostUSD: formatNumberInput(graph.budget?.max_cost_usd),
  maxTokens: formatNumberInput(graph.budget?.max_tokens),
  maxDurationSeconds: formatNumberInput(graph.budget?.max_duration_seconds),
  nodes: (graph.nodes ?? []).map((node) => ({
    id: node.id,
    name: node.name ?? '',
    type: node.type ?? '',
    description: node.description ?? '',
    assignedTo: node.assigned_to ?? '',
    timeoutSeconds: formatNumberInput(node.timeout_seconds),
    retryLimit: formatNumberInput(node.retry_limit),
    dependencies: [...(node.dependencies ?? [])],
    enabled: node.enabled !== false,
    inputsText: formatInputs(node.inputs),
  })),
});

export const cloneForm = (form: DraftForm): DraftForm => ({
  title: form.title,
  maxCostUSD: form.maxCostUSD,
  maxTokens: form.maxTokens,
  maxDurationSeconds: form.maxDurationSeconds,
  nodes: form.nodes.map((node) => ({
    ...node,
    dependencies: [...node.dependencies],
  })),
});

/** Deterministic JSON: object keys are sorted so nested inputs compare stably. */
const stableStringify = (value: unknown): string => {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return `[${value.map((entry) => stableStringify(entry)).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, entry]) => entry !== undefined)
    .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0))
    .map(([key, entry]) => `${JSON.stringify(key)}:${stableStringify(entry)}`);
  return `{${entries.join(',')}}`;
};

/**
 * Normalize one raw numeric field for comparison. A valid number is reduced to
 * its canonical form (`1.50` and `1.5` are the same edit) while empty or
 * malformed text stays verbatim so it can never collapse into a clean `0`.
 */
const canonicalNumber = (raw: string): string => {
  const trimmed = raw.trim();
  if (trimmed === '') return '';
  const value = Number(trimmed);
  return Number.isFinite(value) ? String(value) : `raw:${trimmed}`;
};

const canonicalInputs = (text: string): string => {
  const trimmed = text.trim();
  if (trimmed === '') return 'json:{}';
  try {
    return `json:${stableStringify(JSON.parse(trimmed))}`;
  } catch {
    return `raw:${text}`;
  }
};

/**
 * Text fields are compared trimmed, and `graphFromForm` submits the same
 * trimmed value, so "clean" always means "what would be submitted is already
 * saved".
 */
const canonicalText = (value: string): string => value.trim();

/** Dependency order is user-editable, so compare it the way it is submitted. */
const normalizeDependencies = (dependencies: string[]): string[] =>
  [...new Set(dependencies)].sort((left, right) => (left < right ? -1 : left > right ? 1 : 0));

/** Canonical, order-independent projection of a draft used for dirty checks. */
export const canonicalizeForm = (form: DraftForm) => ({
  title: canonicalText(form.title),
  budget: {
    maxCostUSD: canonicalNumber(form.maxCostUSD),
    maxTokens: canonicalNumber(form.maxTokens),
    maxDurationSeconds: canonicalNumber(form.maxDurationSeconds),
  },
  nodes: form.nodes
    .map((node) => ({
      id: node.id,
      name: canonicalText(node.name),
      type: node.type,
      description: canonicalText(node.description),
      assignedTo: canonicalText(node.assignedTo),
      timeoutSeconds: canonicalNumber(node.timeoutSeconds),
      retryLimit: canonicalNumber(node.retryLimit),
      dependencies: normalizeDependencies(node.dependencies),
      enabled: node.enabled,
      inputs: canonicalInputs(node.inputsText),
    }))
    .sort((left, right) => (left.id < right.id ? -1 : left.id > right.id ? 1 : 0)),
});

export const formSignature = (form: DraftForm): string => JSON.stringify(canonicalizeForm(form));

/** True when the draft differs from the loaded baseline (R-03/R-04). */
export const isFormDirty = (form: DraftForm | null, baseline: DraftForm | null): boolean => {
  if (!form || !baseline) return false;
  return formSignature(form) !== formSignature(baseline);
};

export interface NumericParse {
  ok: boolean;
  value?: number;
  message?: string;
}

export const parseNumericField = (
  raw: string,
  options: { label: string; integer: boolean; optional: boolean },
): NumericParse => {
  const trimmed = raw.trim();
  if (trimmed === '') {
    return options.optional
      ? { ok: true }
      : { ok: false, message: `${options.label}不能为空，请输入数字（不会自动按 0 处理）` };
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value)) {
    return { ok: false, message: `${options.label}必须是数字` };
  }
  if (value < 0) {
    return { ok: false, message: `${options.label}不能为负数` };
  }
  if (options.integer && !Number.isInteger(value)) {
    return { ok: false, message: `${options.label}必须是整数` };
  }
  return { ok: true, value };
};

export interface InputsParse {
  ok: boolean;
  value?: Record<string, unknown>;
  message?: string;
}

export const parseInputsText = (text: string): InputsParse => {
  const trimmed = text.trim();
  if (trimmed === '') return { ok: true, value: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (error) {
    const detail = error instanceof Error ? error.message : 'JSON 解析失败';
    const positionMatch = /position (\d+)/i.exec(detail);
    let where = '';
    if (positionMatch) {
      const position = Number(positionMatch[1]);
      const before = trimmed.slice(0, position);
      const line = before.split('\n').length;
      const column = position - before.lastIndexOf('\n');
      where = `（第 ${line} 行第 ${column} 列）`;
    }
    return { ok: false, message: `输入不是合法 JSON${where}：${detail}` };
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, message: '输入必须是 JSON 对象（{ ... }）' };
  }
  return { ok: true, value: parsed as Record<string, unknown> };
};

/**
 * Validate every editable field and report where the problem is, so the panel
 * can point at the offending field instead of failing the request later.
 */
export const collectFormIssues = (form: DraftForm, baseline: DraftForm | null): FormIssue[] => {
  const issues: FormIssue[] = [];
  const budgetChecks: Array<{ field: string; raw: string; base: string; label: string; integer: boolean }> = [
    { field: 'budget.max_cost_usd', raw: form.maxCostUSD, base: baseline?.maxCostUSD ?? '', label: '最大成本 USD', integer: false },
    { field: 'budget.max_tokens', raw: form.maxTokens, base: baseline?.maxTokens ?? '', label: '最大 tokens', integer: true },
    {
      field: 'budget.max_duration_seconds',
      raw: form.maxDurationSeconds,
      base: baseline?.maxDurationSeconds ?? '',
      label: '最大时长（秒）',
      integer: true,
    },
  ];
  budgetChecks.forEach((check) => {
    const optional = check.base.trim() === '';
    const result = parseNumericField(check.raw, { label: check.label, integer: check.integer, optional });
    if (!result.ok) issues.push({ field: check.field, message: result.message ?? `${check.label}不合法` });
  });
  form.nodes.forEach((node) => {
    const baseNode = baseline?.nodes.find((candidate) => candidate.id === node.id);
    const nodeChecks: Array<{ field: string; raw: string; base: string; label: string }> = [
      { field: 'timeout_seconds', raw: node.timeoutSeconds, base: baseNode?.timeoutSeconds ?? '', label: '超时（秒）' },
      { field: 'retry_limit', raw: node.retryLimit, base: baseNode?.retryLimit ?? '', label: '重试次数' },
    ];
    nodeChecks.forEach((check) => {
      const optional = check.base.trim() === '';
      const result = parseNumericField(check.raw, { label: check.label, integer: true, optional });
      if (!result.ok) {
        issues.push({ nodeId: node.id, field: check.field, message: result.message ?? `${check.label}不合法` });
      }
    });
    const inputs = parseInputsText(node.inputsText);
    if (!inputs.ok) {
      issues.push({ nodeId: node.id, field: 'inputs', message: inputs.message ?? '输入 JSON 不合法' });
    }
  });
  return issues;
};

const toNumber = (raw: string): number | undefined => {
  const trimmed = raw.trim();
  if (trimmed === '') return undefined;
  const value = Number(trimmed);
  return Number.isFinite(value) ? value : undefined;
};

const finiteOrUndefined = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;

/**
 * Build the graph sent to `createRevision`. Callers must run
 * `collectFormIssues` first.
 *
 * Required numeric fields (`budget.max_duration_seconds`, `retry_limit`) can
 * never be dropped from the payload: deleting them would only move the failure
 * to the server. When the draft leaves one empty — a state validation already
 * rejects — the loaded base value is kept, so the submitted graph stays legal
 * without ever inventing a `0` from the user's text.
 *
 * Text is submitted trimmed, exactly like `canonicalizeForm` compares it, so
 * "clean" and "already saved" can never disagree.
 */
export const graphFromForm = (base: PlanGraph, form: DraftForm): PlanGraph => {
  const baseBudget = base.budget ?? ({ max_task_attempts: 0, max_duration_seconds: 0 } as PlanGraph['budget']);
  const budget: PlanGraph['budget'] = {
    ...baseBudget,
    max_duration_seconds:
      toNumber(form.maxDurationSeconds) ?? finiteOrUndefined(baseBudget.max_duration_seconds) ?? 0,
  };
  const cost = toNumber(form.maxCostUSD);
  if (cost === undefined) delete budget.max_cost_usd;
  else budget.max_cost_usd = cost;
  const tokens = toNumber(form.maxTokens);
  if (tokens === undefined) delete budget.max_tokens;
  else budget.max_tokens = tokens;

  return {
    ...base,
    title: form.title.trim(),
    budget,
    nodes: (base.nodes ?? []).map((node) => {
      const draft = form.nodes.find((candidate) => candidate.id === node.id);
      if (!draft) return node;
      const inputs = parseInputsText(draft.inputsText);
      const next: PlanGraph['nodes'][number] = {
        ...node,
        name: draft.name.trim(),
        description: draft.description.trim(),
        assigned_to: draft.assignedTo.trim(),
        dependencies: normalizeDependencies(draft.dependencies),
        enabled: draft.enabled,
        // An unparsable draft keeps the loaded inputs rather than silently
        // becoming `{}`; validation has already blocked such a save.
        inputs: inputs.ok ? (inputs.value ?? {}) : (node.inputs ?? {}),
        retry_limit: toNumber(draft.retryLimit) ?? finiteOrUndefined(node.retry_limit) ?? 0,
      };
      const timeout = toNumber(draft.timeoutSeconds);
      if (timeout === undefined) delete next.timeout_seconds;
      else next.timeout_seconds = timeout;
      return next;
    }),
  };
};
