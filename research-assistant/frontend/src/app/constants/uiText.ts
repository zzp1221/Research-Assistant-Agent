export const uiText = {
  appWelcome: '描述研究目标。我会先生成可执行的节点图，再按依赖顺序调度；每一步的日志、产物与环境清单都保留在右侧检查器。',
  planGenerated: '执行图已生成。核对节点与依赖后，在右侧执行台启动计划。',
  backendError: '请求失败：请确认 Go 服务在 :8080 端口运行，或检查网络与代理设置。',
  graphTitle: '多智能体执行流程',
  graphHint: '等待生成执行计划',
  runAll: '运行计划',
  planStartMessage: '已启动整图执行，正在订阅计划级事件流。',
  planStartFailedMessage: '启动执行失败：请确认后端计划服务可用后重试。',
  noPlanMessage: '当前前端已经切换到 plan_graph 主流程，无法回退旧任务列表执行链路。请重新生成计划后再执行。',
} as const;