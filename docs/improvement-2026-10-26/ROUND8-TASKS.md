# 任务清单：N-01 计划库 / N-02 可复现导出 / N-03 成本与预算控制台（2026-10-28 第八轮）

## P0 基线与规格
- [x] 复核设计文档与实际代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 go build/vet（通过）
- [x] 归档第五轮 spec/plan/tasks

## P1 N-01 计划库后端
- [x] models/project.go
- [x] PlanGraph Title/ProjectID/Archived 字段
- [x] store.ListPlanSummaries（Memory/File）
- [x] internal/project Store（Memory/File）
- [x] /api/projects CRUD
- [x] listPlans 过滤 + keyset 游标 + summary 扩展
- [x] PATCH /api/plans/:id（重命名/移动/归档 + budget）
- [x] POST /api/plans/:id/duplicate
- [x] 单测（1000 计划、过滤、游标、归档、重命名、复制、409）

## P2 N-01 前端计划库
- [x] contracts + api client
- [x] PlanLibraryPanel
- [x] 顶栏入口 + 打开计划
- [x] 组件测试

## P3 N-02 可复现导出
- [x] bundle builder + manifest + checksums
- [x] environment recipe（requirements.lock/system-packages.txt/Dockerfile/run.sh）
- [x] provenance 文件
- [x] 脱敏（map + 文本 + inline base64 处理）
- [x] 大小上限 + 流式 zip
- [x] POST /api/plans/:id/reproduce/verify
- [x] MD/JSON 导出升级 + 审计/指标
- [x] 单测（结构/校验/recipe/脱敏/omitted/partial/确定性/verify）

## P4 N-03 成本与预算后端
- [x] RunBudget/TaskUsage/RunUsage 扩展
- [x] usage profile 计价 + CostSource
- [x] llmclient WithModelPrice
- [x] agent 注入 profile 价格
- [x] internal/budget Store/Manager
- [x] scheduler 预算闸门/预警/暂停/结算/释放
- [x] usage/budgets APIs
- [x] 创建计划预算配置 + PATCH budget
- [x] 单测

## P5 N-03 前端成本控制台
- [x] contracts + api client
- [x] CostConsolePanel
- [x] masthead 总成本 + ExecutionSidebar 节点成本
- [x] 组件测试

## P6 回归、E2E、清理
- [x] 后端容器全量 build/vet/test（全绿）
- [x] 前端容器 npm test/build/lint（117 tests 通过）
- [x] 其它 Go 模块（docker-core/docker-sandbox/tool）测试
- [x] 重建 backend/frontend 镜像 + compose 起栈
- [x] 新增 E2E（plan-library/repro-bundle/cost-budget/budget-recovery）
- [x] 既有 E2E 回归（http-smoke 17/17；plan-run 7 节点 completed）
- [x] 清理临时产物/空报告目录
- [x] FIX-REPORT-ROUND8

## 未实现（明确记录）
- [ ] 只读分享 token（设计标注“可选”；N-12 协作/分享非本轮目标）
- [ ] 完整 replay（在干净容器重跑对比指标；本轮只做静态校验）
- [ ] GPU/存储美元成本（无单价来源；字段已预留）
