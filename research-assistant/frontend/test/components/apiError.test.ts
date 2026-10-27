import { describe, expect, it, vi } from 'vitest';
import { appendApiErrorMessage, formatApiError } from '../../src/services/api/apiError';

const axiosError = (data: unknown, status = 400) => {
  const error = new Error(`Request failed with status code ${status}`) as Error & {
    response: { status: number; data: unknown };
    isAxiosError: boolean;
  };
  error.response = { status, data };
  error.isAxiosError = true;
  error.stack = 'Error: Request failed\n    at sendRequest (/app/src/services/client.ts:10:5)';
  return error;
};

describe('formatApiError known codes', () => {
  const cases: [string, string][] = [
    ['intent_too_long', '输入内容过长'],
    ['rate_limited', '请求过于频繁'],
    ['active_plan_quota_exceeded', '进行中的计划过多'],
    ['plan_paused', '计划已暂停'],
    ['plan_already_terminal', '计划已结束'],
    ['approval_expired', '审批已过期'],
    ['plan_already_running', '计划正在运行'],
    ['quota_exceeded', '超出配额'],
    ['malware_detected', '文件未通过安全扫描'],
    ['scan_failed', '文件安全扫描失败'],
    ['missing_requirements', '缺少必需材料'],
  ];

  it.each(cases)('maps %s to a Chinese title and remediation', (code, title) => {
    const formatted = formatApiError(axiosError({ code, error: 'backend detail' }));
    expect(formatted.code).toBe(code);
    expect(formatted.title).toBe(title);
    expect(formatted.remediation.length).toBeGreaterThan(0);
    expect(formatted.detail).not.toContain('sendRequest');
    expect(formatted.detail).not.toContain('client.ts');
  });

  it('shows the retry delay when the backend rate limits a request', () => {
    const formatted = formatApiError(axiosError({ code: 'rate_limited', retry_after_seconds: 12 }, 429));
    expect(formatted.code).toBe('rate_limited');
    expect(formatted.detail).toContain('12 秒');
    expect(formatted.detail).not.toContain('Request failed with status code');
  });

  it('accepts JSON text in an axios response body', () => {
    const formatted = formatApiError({
      message: 'Request failed with status code 429',
      response: {
        status: 429,
        data: JSON.stringify({ code: 'rate_limited', retry_after_seconds: '30' }),
      },
    });
    expect(formatted.code).toBe('rate_limited');
    expect(formatted.detail).toContain('30 秒');
  });

  it('renders missing requirements as a readable detail and remediation', () => {
    const formatted = formatApiError(axiosError({
      error: 'paper metadata is missing',
      missing_requirements: [
        {
          kind: 'paper_metadata',
          name: 'paper',
          detail: '没有可提取的正文文本',
          remediation: '请上传带文本层的论文 PDF',
        },
      ],
    }));

    expect(formatted.code).toBe('missing_requirements');
    expect(formatted.title).toBe('缺少必需材料');
    expect(formatted.detail).toContain('1 项必需材料');
    expect(formatted.detail).toContain('没有可提取的正文文本');
    expect(formatted.remediation).toContain('请上传带文本层的论文 PDF');
  });

  it('infers missing_requirements when the payload only has the list', () => {
    const formatted = formatApiError({
      response: {
        status: 400,
        data: {
          error: 'paper metadata is missing',
          missing_requirements: [
            { kind: 'paper_metadata', name: 'paper', detail: 'no text layer', remediation: '上传可提取文本的 PDF' },
          ],
        },
      },
    });
    expect(formatted.code).toBe('missing_requirements');
    expect(formatted.detail).toContain('no text layer');
    expect(formatted.remediation).toBe('上传可提取文本的 PDF');
  });
});

describe('formatApiError fallbacks', () => {
  it('returns the unknown code and a sanitized axios message', () => {
    const formatted = formatApiError(axiosError({ error: 'internal exploded' }, 500));
    expect(formatted.code).toBe('unknown');
    expect(formatted.title).toBe('请求失败');
    expect(formatted.detail).toBe('internal exploded');
    expect(formatted.remediation.length).toBeGreaterThan(0);
  });

  it('falls back to Error.message for non-axios errors', () => {
    const formatted = formatApiError(new Error('boom'));
    expect(formatted.code).toBe('unknown');
    expect(formatted.detail).toBe('boom');
  });

  it('never exposes raw stack frames', () => {
    const error = new Error('boom\n    at handler (/app/src/x.ts:3:9)\n    at main (/app/src/y.ts:8:1)');
    error.stack = 'Error: boom\n    at handler (/app/src/x.ts:3:9)';
    const formatted = formatApiError(error);
    expect(formatted.detail).toBe('boom');
    expect(formatted.detail).not.toContain('/app/src');
    expect(formatted.remediation).not.toContain('at handler');
  });

  it('strips stacks from axios payload messages too', () => {
    const formatted = formatApiError(axiosError({
      code: 'scan_failed',
      error: 'scan failed\n    at scan (/app/src/scan.ts:4:2)',
    }));
    expect(formatted.code).toBe('scan_failed');
    expect(formatted.detail).not.toContain('/app/src/scan.ts');
  });

  it('describes a bodiless HTTP failure with the status code', () => {
    const formatted = formatApiError({
      message: 'Request failed with status code 503',
      response: { status: 503, data: '' },
    });
    expect(formatted.code).toBe('unknown');
    expect(formatted.detail).toContain('HTTP 503');
  });

  it('handles nullish, string and empty-object errors without throwing', () => {
    for (const error of [null, undefined, '', {}, 'plain failure']) {
      const formatted = formatApiError(error);
      expect(formatted.code).toBe('unknown');
      expect(typeof formatted.detail).toBe('string');
      expect(formatted.detail.length).toBeGreaterThan(0);
    }
  });

  it('maps network failures to actionable Chinese text', () => {
    const formatted = formatApiError(new Error('Network Error'));
    expect(formatted.code).toBe('unknown');
    expect(formatted.detail).toContain('后端');
    expect(formatted.remediation).toContain('重试');
  });
});

describe('appendApiErrorMessage', () => {
  it('appends a system message with title, remediation and code', () => {
    const append = vi.fn();
    const formatted = appendApiErrorMessage(append, axiosError({ code: 'plan_paused', error: 'plan is paused' }));

    expect(formatted.code).toBe('plan_paused');
    expect(append).toHaveBeenCalledTimes(1);
    const message = append.mock.calls[0][0] as { role: string; text: string };
    expect(message.role).toBe('system');
    expect(message.text).toContain('计划已暂停');
    expect(message.text).toContain('修复建议');
    expect(message.text).toContain('plan_paused');
    expect(message.text).not.toContain('at sendRequest');
  });
});
