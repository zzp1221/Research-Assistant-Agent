import type { ChatMessage } from '../../contracts/api';

export interface ApiErrorPayload {
  code?: string;
  error?: string;
  message?: string;
  remediation?: string;
  retry_after_seconds?: number;
  missing_requirements?: {
    kind: string;
    name: string;
    detail: string;
    remediation: string;
  }[];
}

export interface FormattedApiError {
  title: string;
  detail: string;
  remediation: string;
  code: string;
}

type UnknownRecord = Record<string, unknown>;

interface KnownCodeExplanation {
  title: string;
  detail: string;
  remediation: string;
}

// Each code maps to a short Chinese explanation plus an actionable next step.
// The raw backend message is kept only as supporting context and never as the
// primary text, so users are not shown bare English errors or stack traces.
const KNOWN_CODES: Record<string, KnownCodeExplanation> = {
  intent_too_long: {
    title: '输入内容过长',
    detail: '研究目标或输入内容超过了长度限制。',
    remediation: '请精简研究目标（建议不超过 20,000 字符），或拆分成多个计划后重试。',
  },
  rate_limited: {
    title: '请求过于频繁',
    detail: '请求过于频繁，暂时被限流。',
    remediation: '请稍候几分钟再试；系统会自动恢复可用额度。',
  },
  active_plan_quota_exceeded: {
    title: '进行中的计划过多',
    detail: '同时进行的计划数量已达到上限。',
    remediation: '请等待现有计划结束，或先取消不再需要的计划，再创建新计划。',
  },
  plan_paused: {
    title: '计划已暂停',
    detail: '该计划当前处于暂停状态，不能继续执行。',
    remediation: '请先恢复计划运行，再启动节点或整图执行。',
  },
  plan_already_terminal: {
    title: '计划已结束',
    detail: '该计划已经结束，无法继续执行或审批。',
    remediation: '请新建计划，或从历史记录中重新运行该计划。',
  },
  approval_expired: {
    title: '审批已过期',
    detail: '这次审批已经超过有效时间。',
    remediation: '请重新发起审批或重新生成计划后再操作。',
  },
  plan_already_running: {
    title: '计划正在运行',
    detail: '该计划已经在运行中。',
    remediation: '请等待当前执行结束，无需重复启动。',
  },
  quota_exceeded: {
    title: '超出配额',
    detail: '上传或存储配额已经用尽。',
    remediation: '请删除不再需要的上传文件或旧计划，释放空间后重试。',
  },
  malware_detected: {
    title: '文件未通过安全扫描',
    detail: '上传的文件被安全检查判定为恶意文件，已被阻止。',
    remediation: '请更换文件后重新上传；不要重试已被拦截的文件。',
  },
  scan_failed: {
    title: '文件安全扫描失败',
    detail: '上传文件未能完成安全扫描。',
    remediation: '请稍后重试上传；若持续失败，请联系管理员检查病毒扫描服务。',
  },
  missing_requirements: {
    title: '缺少必需材料',
    detail: '缺少继续执行所需的材料或配置。',
    remediation: '请根据缺失材料列表补齐对应资源后重试。',
  },
};

const UNKNOWN_TITLE = '请求失败';
const UNKNOWN_DETAIL = '发生了未预期的错误，请稍后重试。';
const UNKNOWN_REMEDIATION = '请稍后重试；若持续失败，请检查后端服务状态，并附上错误码联系维护者。';
const MAX_DETAIL_LENGTH = 400;
const MAX_REMEDIATION_LENGTH = 240;

const CODE_PATTERN = /^[a-z][a-z0-9_]{1,63}$/i;

// Only used to keep Node/Go stack frames out of user-facing details. The raw
// Error.stack property is never read, so a stack cannot leak through this path.
const STACK_LINE_PATTERNS: RegExp[] = [
  /^\s*at\s+\S/,
  /^\s*File\s+"/,
  /^\s*Traceback\s*\(most recent call last\)\s*:?/i,
  /^\s*panic\s*:/i,
  /^\s*goroutine\s+\d+/i,
  /^\s*Caused by\s*:/i,
  /^\s*\.\.\.\s+\d+\s+more\s*$/i,
  /^\s*(?:[A-Za-z]:[\\/]|\/)[^\n]*\.(?:go|py|js|jsx|ts|tsx|java|rs):\d+/,
];

const GENERIC_MESSAGES: RegExp[] = [
  /^request failed with status code \d+$/i,
  /^network error$/i,
  /^failed to fetch$/i,
  /^networkerror$/i,
  /^timeout of \d+ms exceeded$/i,
  /^http \d+$/i,
];

const isRecord = (value: unknown): value is UnknownRecord =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const readString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const readNumber = (value: unknown): number | undefined => {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
};

// Backend codes are snake_case. Requiring an underscore keeps a one-word
// human message such as "timeout" visible as the error detail.
const looksLikeApiCode = (value: string): boolean => CODE_PATTERN.test(value.trim()) && value.includes('_');

const normalizeCode = (value: string): string => value.trim().toLowerCase().replace(/[\s-]+/g, '_');

const stripStackTrace = (text: string): string =>
  text
    .split(/\r?\n/)
    .filter((line) => !STACK_LINE_PATTERNS.some((pattern) => pattern.test(line)))
    .join('\n');

const INLINE_STACK_FRAME = /\s+at\s+[^\s()]+(?:\s*\([^()]*:\d+(?::\d+)?\))?/g;

const sanitizeText = (value: unknown, maxLength = MAX_DETAIL_LENGTH): string => {
  const raw = typeof value === 'string' ? value : '';
  if (!raw.trim()) return '';
  const cleaned = stripStackTrace(raw).replace(INLINE_STACK_FRAME, '').replace(/\s+/g, ' ').trim();
  if (!cleaned) return '';
  return cleaned.length > maxLength ? `${cleaned.slice(0, maxLength - 1)}…` : cleaned;
};

const isGenericMessage = (message: string): boolean =>
  GENERIC_MESSAGES.some((pattern) => pattern.test(message));

const isNetworkMessage = (message: string): boolean =>
  /network error|failed to fetch|networkerror|err_network|econnrefused|econnreset/i.test(message);

const isTimeoutMessage = (message: string): boolean =>
  /timeout|timed out|econnaborted|etimedout/i.test(message);

const isHtmlPayload = (message: string): boolean =>
  /<!doctype|<html|<body|<\/body>/i.test(message);

const parseJsonRecord = (value: unknown): UnknownRecord | null => {
  if (isRecord(value)) return value;
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    return isRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
};

const isPayloadShape = (record: UnknownRecord): boolean =>
  'code' in record ||
  'error' in record ||
  'message' in record ||
  'remediation' in record ||
  'retry_after_seconds' in record ||
  'missing_requirements' in record;

interface ErrorContext {
  payload: UnknownRecord | null;
  fallbackText: string;
  status?: number;
}

const extractErrorContext = (error: unknown): ErrorContext => {
  if (isRecord(error)) {
    const response = isRecord(error.response) ? error.response : null;
    if (response) {
      const status = readNumber(response.status);
      const data = response.data;
      const payload = parseJsonRecord(data);
      const dataText = typeof data === 'string' ? data : '';
      const messageText = readString(error.message);
      return { payload, fallbackText: payload ? messageText : dataText || messageText, status };
    }
    // A plain thrown object such as { code, message } is treated like a
    // response body; Error instances are excluded so their message wins.
    if (!(error instanceof Error) && isPayloadShape(error)) {
      return { payload: error, fallbackText: '' };
    }
    if (error instanceof Error) {
      return { payload: null, fallbackText: readString(error.message) };
    }
    return { payload: null, fallbackText: '' };
  }
  if (typeof error === 'string') return { payload: null, fallbackText: error };
  if (typeof error === 'number' || typeof error === 'boolean') {
    return { payload: null, fallbackText: String(error) };
  }
  return { payload: null, fallbackText: '' };
};

interface ParsedMissingRequirement {
  kind: string;
  name: string;
  detail: string;
  remediation: string;
}

const parseMissingRequirements = (value: unknown): ParsedMissingRequirement[] => {
  if (!Array.isArray(value)) return [];
  return value.filter(isRecord).map((item) => ({
    kind: readString(item.kind) || 'unknown',
    name: readString(item.name),
    detail: sanitizeText(item.detail, 240),
    remediation: sanitizeText(item.remediation, MAX_REMEDIATION_LENGTH),
  }));
};

const buildMissingRequirementsDetail = (missing: ParsedMissingRequirement[]): string => {
  if (missing.length === 0) return '';
  const preview = missing
    .slice(0, 3)
    .map((item) => {
      const label = item.name || item.kind;
      return item.detail ? `${label}：${item.detail}` : label;
    })
    .join('；');
  return `缺少 ${missing.length} 项必需材料：${preview}${missing.length > 3 ? ' 等' : ''}。`;
};

const knownDetail = (code: string, message: string, retryAfter: number | undefined, missing: ParsedMissingRequirement[]): string => {
  const explanation = KNOWN_CODES[code];
  if (!explanation) return '';
  if (code === 'missing_requirements') {
    const summary = buildMissingRequirementsDetail(missing);
    if (summary) return message ? `${summary}（${message}）` : summary;
    return message || explanation.detail;
  }
  if (code === 'rate_limited' && retryAfter !== undefined && retryAfter > 0) {
    return `请求过于频繁，请在 ${Math.ceil(retryAfter)} 秒后重试。`;
  }
  if (message && !isGenericMessage(message) && !isHtmlPayload(message)) {
    return `${explanation.detail}（${message}）`;
  }
  return explanation.detail;
};

export const formatApiError = (error: unknown): FormattedApiError => {
  try {
    const context = extractErrorContext(error);
    const payload = context.payload;
    const missing = parseMissingRequirements(payload?.missing_requirements);
    const payloadCode = readString(payload?.code);
    const payloadError = readString(payload?.error);
    const payloadMessage = readString(payload?.message);
    const fallbackText = sanitizeText(context.fallbackText);
    const errorAsCode = looksLikeApiCode(payloadError) ? normalizeCode(payloadError) : '';
    const codeCandidate = normalizeCode(payloadCode) || errorAsCode;
    const knownCode = KNOWN_CODES[codeCandidate] ? codeCandidate : '';
    const code = knownCode || (missing.length > 0 ? 'missing_requirements' : 'unknown');
    const message = sanitizeText(payloadMessage || (errorAsCode ? '' : payloadError) || context.fallbackText);
    const retryAfter = readNumber(payload?.retry_after_seconds);
    const payloadRemediation = sanitizeText(payload?.remediation, MAX_REMEDIATION_LENGTH);
    const missingRemediation = missing.find((item) => item.remediation)?.remediation ?? '';
    const explanation = KNOWN_CODES[code];
    const remediation =
      payloadRemediation ||
      (code === 'missing_requirements' ? missingRemediation : '') ||
      explanation?.remediation ||
      UNKNOWN_REMEDIATION;

    let detail = explanation ? knownDetail(code, message, retryAfter, missing) : '';
    if (!detail) {
      if (isNetworkMessage(fallbackText || message)) {
        detail = '请求未能到达后端，可能是服务未启动、网络中断或代理异常。';
      } else if (isTimeoutMessage(fallbackText || message)) {
        detail = '请求等待超时。后端可能仍在处理，请稍后刷新计划状态再试。';
      } else if (message && !isHtmlPayload(message) && !isGenericMessage(message)) {
        detail = message;
      } else if (context.status !== undefined) {
        detail = `服务返回 HTTP ${context.status}，但没有提供更多错误信息。`;
      } else {
        detail = UNKNOWN_DETAIL;
      }
    }

    return {
      title: explanation?.title ?? UNKNOWN_TITLE,
      detail,
      remediation,
      code,
    };
  } catch {
    return {
      title: UNKNOWN_TITLE,
      detail: UNKNOWN_DETAIL,
      remediation: UNKNOWN_REMEDIATION,
      code: 'unknown',
    };
  }
};

/**
 * Convenience wrapper for chat-oriented callers: formats an API error and
 * appends the same title/detail/remediation/code as a system message. Kept
 * separate from formatApiError so non-chat surfaces can render structured UI.
 */
export const appendApiErrorMessage = (
  appendChatMessage: (message: ChatMessage) => void,
  error: unknown,
): FormattedApiError => {
  const formatted = formatApiError(error);
  appendChatMessage({
    role: 'system',
    text: `**${formatted.title}**\n\n${formatted.detail}\n\n修复建议：${formatted.remediation}\n\n错误码：\`${formatted.code}\``,
  });
  return formatted;
};