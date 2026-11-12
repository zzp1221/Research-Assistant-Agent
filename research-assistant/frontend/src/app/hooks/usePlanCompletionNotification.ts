import { useEffect, useRef } from 'react';
import { getDesktopBridge } from '../../services/desktopBridge';

/**
 * Notification copy for a terminal plan. The status alone is not enough: a
 * completed run can still be partial (core accepted, optional work lost) and a
 * failed run can be a missing-condition block. Reading the outcome keeps the
 * notification from claiming full success, or reporting a fixable condition as
 * an ordinary execution failure.
 */
export function planNotificationText(
  status: string | null | undefined,
  outcome?: string | null,
): { title: string; body: string } | null {
  if (status === 'completed') {
    switch (outcome) {
      case 'partial':
        return { title: '计划部分完成', body: '核心目标已通过验收，部分可选工作未完成；打开详情查看缺失内容。' };
      case 'needs_review':
        return { title: '计划待复核', body: '执行已结束，但证据还不足以判定结论；打开详情复核。' };
      case 'smoke':
        return { title: '计划已完成（冒烟）', body: '本次为冒烟运行，不代表完整复现。' };
      case 'mock':
        return { title: '计划已完成（模拟）', body: '本次使用模拟数据，不代表真实结果。' };
      default:
        return { title: '计划已完成', body: '计划已在后台完成。' };
    }
  }
  if (status === 'failed') {
    if (outcome === 'blocked') {
      return { title: '计划缺少条件', body: '计划因缺少必需条件而停止；打开详情查看需要补充的内容。' };
    }
    return { title: '计划失败', body: '计划执行失败，请打开详情查看原因。' };
  }
  if (status === 'canceled') {
    return { title: '计划已取消', body: '计划已取消，未产生可用结果。' };
  }
  return null;
}

/** Emits a system notification when a plan transitions to a terminal state. */
export function usePlanCompletionNotification(
  planId: string | null | undefined,
  status: string | null | undefined,
  outcome?: string | null,
) {
  const previous = useRef<{ planId?: string | null; status?: string | null }>({});

  useEffect(() => {
    const last = previous.current;
    if (planId && last.planId === planId && last.status && last.status !== status) {
      const text = planNotificationText(status, outcome);
      const bridge = getDesktopBridge();
      if (text && bridge) {
        void bridge.notify(text.title, text.body);
      }
    }
    previous.current = { planId, status };
  }, [planId, status, outcome]);
}
