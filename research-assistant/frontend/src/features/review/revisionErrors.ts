/**
 * Normalized view of a revision API failure.
 *
 * The backend answers failures with `{ error, code }` and an HTTP status. The
 * panel needs three things from that: a stable reason code to show (the design
 * asks for `revision_not_latest` / `plan_running` / `plan_paused` to be spelled
 * out), the HTTP status, and — critically — whether the request definitely
 * failed or whether its outcome is simply unknown (no response at all, or a
 * 5xx that may have committed the write), in which case the client must
 * reconcile with the server instead of retrying.
 */
export interface ApiErrorInfo {
  status: number | null;
  code: string;
  message: string;
  payload: Record<string, unknown>;
  /** True when the outcome is unproven: no HTTP response, or a 5xx. */
  unknownOutcome: boolean;
}

const asRecord = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {};

/**
 * A 5xx is *not* a proven failure: the server may have committed the write and
 * then failed while building the response (`plan_update_failed` after
 * `store.Create` is exactly that shape). Only explicit client-side rejections
 * (4xx such as 409 conflict or 400 validation) prove nothing was applied.
 */
const NO_RESPONSE_CODES = new Set([
  'ECONNABORTED',
  'ETIMEDOUT',
  'ERR_NETWORK',
  'ECONNRESET',
  'ENOTFOUND',
  'EAI_AGAIN',
  'EPIPE',
  'ERR_CANCELED',
  'network_error',
  'ECONNREFUSED',
  'ERR_BAD_RESPONSE',
]);

export const classifyApiError = (error: unknown, fallbackMessage = '请求失败'): ApiErrorInfo => {
  const record = asRecord(error);
  const response = asRecord(record.response);
  const payload = asRecord(response.data);
  const rawStatus = response.status;
  const status = typeof rawStatus === 'number' && Number.isFinite(rawStatus) ? rawStatus : null;
  const code = typeof payload.code === 'string' && payload.code.trim() !== ''
    ? payload.code
    : typeof record.code === 'string'
      ? record.code
      : '';
  const payloadMessage =
    (typeof payload.error === 'string' && payload.error) ||
    (typeof payload.message === 'string' && payload.message) ||
    '';
  const errorMessage = typeof record.message === 'string' && record.message ? record.message : '';
  const message = payloadMessage || errorMessage || fallbackMessage;
  const hasRequest = record.request !== undefined && record.request !== null;
  // Unknown outcome = the request may have reached the server, so the client
  // must reconcile before retrying: no HTTP response at all, a 5xx, or a
  // body-less protocol error.
  const unknownOutcome =
    status === null
      ? hasRequest || NO_RESPONSE_CODES.has(code) || errorMessage === 'Network Error'
      : status >= 500;

  return { status, code, message, payload, unknownOutcome };
};

export const REVISION_ERROR_LABELS: Record<string, string> = {
  revision_conflict: '服务端已有更新的版本（revision_conflict），本地修改不会自动覆盖',
  revision_not_latest: '该版本不是最新版本（revision_not_latest），只能批准并运行最新版本',
  revision_not_found: '版本不存在（revision_not_found），请刷新版本列表',
  revision_empty: '该版本没有可执行快照（revision_empty）',
  revision_store_unavailable: '版本存储暂不可用（revision_store_unavailable）',
  plan_running: '计划正在运行（plan_running），不能重复启动',
  plan_already_running: '计划已在运行（plan_already_running），不能重复启动',
  plan_paused: '计划已暂停（plan_paused），请先恢复再运行',
  plan_update_failed: '保存计划失败（plan_update_failed）',
  review_pending: '该版本尚未批准（review_pending），请先批准该版本',
  review_rejected: '该计划审阅已被拒绝（review_rejected），需要重新批准',
  validation_failed: '计划校验未通过（validation_failed），请修正后重试',
  unauthorized: '当前身份无权访问该计划（unauthorized）',
  forbidden: '当前身份无权访问该计划（forbidden）',
  not_found: '计划不存在或已被删除（not_found）',
};

export const describeRevisionErrorCode = (code: string): string => {
  if (!code) return '';
  return REVISION_ERROR_LABELS[code] ?? `服务端返回 ${code}`;
};

/** Human readable one-liner combining the reason label with the server text. */
export const describeApiError = (info: ApiErrorInfo, fallbackMessage = '请求失败'): string => {
  const label = describeRevisionErrorCode(info.code);
  const detail = info.message && info.message !== fallbackMessage ? info.message : '';
  if (label && detail) return `${label}：${detail}`;
  if (label) return label;
  return detail || fallbackMessage;
};

export const isRevisionConflict = (info: ApiErrorInfo): boolean =>
  info.code === 'revision_conflict' || (info.status === 409 && info.code === 'revision_conflict');

export const conflictServerRevision = (info: ApiErrorInfo): number => {
  const raw = info.payload.current_revision;
  const value = typeof raw === 'number' ? raw : typeof raw === 'string' ? Number(raw) : Number.NaN;
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
};

export interface ServerValidationError {
  node_id?: string;
  field?: string;
  message: string;
  remediation?: string;
}

export const validationIssuesFromPayload = (info: ApiErrorInfo): ServerValidationError[] => {
  const raw = info.payload.validation_errors;
  if (!Array.isArray(raw)) return [];
  const issues: ServerValidationError[] = [];
  raw.forEach((entry) => {
    const record = asRecord(entry);
    const message = typeof record.message === 'string' ? record.message : '';
    if (!message) return;
    issues.push({
      node_id: typeof record.node_id === 'string' ? record.node_id : undefined,
      field: typeof record.field === 'string' ? record.field : undefined,
      message,
      remediation: typeof record.remediation === 'string' ? record.remediation : undefined,
    });
  });
  return issues;
};
