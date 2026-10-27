import { describe, expect, it } from 'vitest';
import {
  DEFAULT_LOG_TAIL,
  filterLogs,
  splitLogLines,
  windowLogs,
} from '../../src/features/execution/logWindow';

describe('splitLogLines', () => {
  it('returns no lines for empty or whitespace-only logs', () => {
    expect(splitLogLines('')).toEqual([]);
    expect(splitLogLines('   ')).toEqual([]);
    expect(splitLogLines('\n\n')).toEqual([]);
  });

  it('splits unix, windows and legacy mac newlines without a trailing blank row', () => {
    expect(splitLogLines('a\nb\n')).toEqual(['a', 'b']);
    expect(splitLogLines('a\r\nb\r\nc')).toEqual(['a', 'b', 'c']);
    expect(splitLogLines('a\rb')).toEqual(['a', 'b']);
  });

  it('keeps meaningful blank lines inside a log', () => {
    expect(splitLogLines('a\n\nb\n')).toEqual(['a', '', 'b']);
  });
});

describe('filterLogs', () => {
  const lines = [
    '[INFO] start',
    '[ERROR] boom',
    'Traceback (most recent call last):',
    'warn: disk low',
    '警告：磁盘不足',
    '任务失败',
    '普通输出 errorless',
    '',
  ];

  it('returns every line unchanged for the all level', () => {
    expect(filterLogs(lines, 'all')).toBe(lines);
  });

  it('keeps only error lines, including stack headers and Chinese markers', () => {
    expect(filterLogs(lines, 'error')).toEqual([
      '[ERROR] boom',
      'Traceback (most recent call last):',
      '任务失败',
    ]);
  });

  it('keeps only warning lines', () => {
    expect(filterLogs(lines, 'warn')).toEqual(['warn: disk low', '警告：磁盘不足']);
  });

  it('treats info as everything that is neither an error nor a warning', () => {
    expect(filterLogs(lines, 'info')).toEqual(['[INFO] start', '普通输出 errorless', '']);
  });

  it('does not mutate the source list', () => {
    const source = ['[ERROR] one', 'two'];
    filterLogs(source, 'error');
    expect(source).toEqual(['[ERROR] one', 'two']);
  });
});

describe('windowLogs', () => {
  const lines = Array.from({ length: 10 }, (_, index) => 'line-' + (index + 1));

  it('returns the trailing lines and counts the hidden ones', () => {
    expect(windowLogs(lines, 3)).toEqual({
      lines: ['line-8', 'line-9', 'line-10'],
      hiddenCount: 7,
    });
  });

  it('does not hide anything when the tail covers the whole log', () => {
    expect(windowLogs(lines, lines.length)).toEqual({ lines, hiddenCount: 0 });
    expect(windowLogs(lines, 99)).toEqual({ lines, hiddenCount: 0 });
  });

  it('treats a zero or negative tail as hiding the whole log', () => {
    expect(windowLogs(lines, 0)).toEqual({ lines: [], hiddenCount: lines.length });
    expect(windowLogs(lines, -5)).toEqual({ lines: [], hiddenCount: lines.length });
  });

  it('handles fractional tails and empty inputs', () => {
    expect(windowLogs(lines, 2.9)).toEqual({
      lines: ['line-9', 'line-10'],
      hiddenCount: 8,
    });
    expect(windowLogs([], 10)).toEqual({ lines: [], hiddenCount: 0 });
    expect(windowLogs(lines, Number.NaN)).toEqual({ lines: [], hiddenCount: lines.length });
  });

  it('does not mutate the source list', () => {
    const source = [...lines];
    windowLogs(lines, 3);
    expect(lines).toEqual(source);
  });

  it('renders only the default tail for a 10k-line log', () => {
    const huge = Array.from({ length: 10_000 }, (_, index) => 'line-' + (index + 1));
    const result = windowLogs(huge, DEFAULT_LOG_TAIL);
    expect(DEFAULT_LOG_TAIL).toBe(500);
    expect(result.lines).toHaveLength(500);
    expect(result.lines[0]).toBe('line-9501');
    expect(result.lines[result.lines.length - 1]).toBe('line-10000');
    expect(result.hiddenCount).toBe(9500);
  });
});
