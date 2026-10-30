export const planStatusLabel = (status: string | null, reviewPending: boolean): string => {
  if (reviewPending) return '待审阅';
  switch (status) {
    case 'in_progress':
      return '运行中';
    case 'awaiting_approval':
      return '待批准';
    case 'awaiting_review':
      return '待审阅';
    case 'awaiting_input':
      return '等待回答';
    case 'completed':
      return '已完成';
    case 'failed':
      return '失败';
    case 'canceled':
      return '已取消';
    case 'pending':
      return '待运行';
    default:
      return status ? '已就绪' : '未创建计划';
  }
};
