export type LogLevel = 'all' | 'error' | 'warn' | 'info';

/**
 * How many trailing lines the execution sidebar renders before the user asks
 * for the full log. Keeping this in one place makes the UI and its tests agree
 * on the default window size.
 */
export const DEFAULT_LOG_TAIL = 500;

// Log levels are inferred from the text because the backend streams plain SSE
// log lines without a structured level field. Word boundaries keep messages
// such as "errorless" from being classified as errors; Chinese markers have no
// word boundaries, so they are matched directly.
const ERROR_PATTERN = /\b(?:error|errors|fatal|panic|exception|traceback|failed|failure)\b|错误|失败|异常|崩溃|❌/i;
const WARN_PATTERN = /\b(?:warn|warns|warned|warning|warnings|deprecated)\b|警告|告警|注意|⚠/i;

export const splitLogLines = (raw: string): string[] => {
  if (!raw || raw.trim() === '') return [];
  const normalized = raw.replace(/\r\n?/g, '\n');
  const lines = normalized.split('\n');
  // A trailing newline terminates the last line; it is not an extra blank row.
  while (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines;
};

export const filterLogs = (lines: string[], level: LogLevel): string[] => {
  if (level === 'all') return lines;
  if (level === 'error') return lines.filter((line) => ERROR_PATTERN.test(line));
  if (level === 'warn') return lines.filter((line) => WARN_PATTERN.test(line));
  // "信息" means ordinary output: everything that is neither an error nor a
  // warning. Unknown lines fall here so the filter never hides them silently.
  return lines.filter((line) => !ERROR_PATTERN.test(line) && !WARN_PATTERN.test(line));
};

export const windowLogs = (lines: string[], tail: number): { lines: string[]; hiddenCount: number } => {
  const total = lines.length;
  if (total === 0) return { lines: [], hiddenCount: 0 };
  const limit = Number.isFinite(tail) ? Math.max(0, Math.floor(tail)) : 0;
  if (limit === 0) return { lines: [], hiddenCount: total };
  if (limit >= total) return { lines: lines.slice(), hiddenCount: 0 };
  return { lines: lines.slice(total - limit), hiddenCount: total - limit };
};
