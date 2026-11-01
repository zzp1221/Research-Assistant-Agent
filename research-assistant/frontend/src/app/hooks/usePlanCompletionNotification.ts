import { useEffect, useRef } from 'react';
import { getDesktopBridge } from '../../services/desktopBridge';

/** Emits a system notification when a plan transitions to a terminal state. */
export function usePlanCompletionNotification(planId: string | null | undefined, status: string | null | undefined) {
  const previous = useRef<{ planId?: string | null; status?: string | null }>({});

  useEffect(() => {
    const last = previous.current;
    if (
      planId &&
      last.planId === planId &&
      last.status &&
      last.status !== status &&
      (status === 'completed' || status === 'failed')
    ) {
      const bridge = getDesktopBridge();
      if (bridge) {
        void bridge.notify(
          status === 'completed' ? '计划已完成' : '计划失败',
          status === 'completed' ? '计划已在后台完成。' : '计划执行失败，请打开详情查看原因。',
        );
      }
    }
    previous.current = { planId, status };
  }, [planId, status]);
}
