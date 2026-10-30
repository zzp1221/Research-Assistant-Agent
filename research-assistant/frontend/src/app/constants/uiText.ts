export const uiText = {
  appWelcome:
    '把论文、数据或代码交给我：我可以检索文献、阅读并翻译 PDF、生成可执行的实验计划，并在右侧执行图里逐步运行。\n\n第一次使用？打开工具栏里的「使用手册」查看 2 分钟上手指引；也可以直接从「文献库」检索论文，导入后加入当前会话。',
  planGenerated: '执行图已生成。请核对节点与依赖，然后在右侧点击「运行计划」。',
  planGeneratedBrief: '执行图已生成。',
  planAutoStarted: '已生成执行图并自动开始运行；可在右侧执行台取消。',
  autoRunSwitchLabel: '创建计划后自动开始执行',
  autoRunSwitchHint: '关闭时，计划生成后需要手动点击「运行计划」',
  backendError: '请求失败：请确认后端服务正在运行，或检查网络与代理设置。',
  graphTitle: '多智能体执行流程',
  graphHint: '在左侧描述研究目标后，这里会生成执行图',
  runAll: '运行计划',
  planStartMessage: '已启动整图执行，正在订阅计划级事件流。',
  planStartFailedMessage: '启动执行失败：请确认后端计划服务可用后重试。',
  noPlanMessage: '当前还没有可执行的计划。请先在左侧描述研究目标并生成执行图。',
} as const;
