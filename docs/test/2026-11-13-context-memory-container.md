# 上下文与记忆：运行容器实测

- 时间：2026-11-12 晚至 2026-11-13 凌晨（Asia/Shanghai）。
- 分支：`feature/memory`，工作区修改尚未提交。
- 对应实现：[上下文与记忆改进实施记录](../design/2026-11-12-context-memory-implementation.md)。
- 本次方式：编译 Linux 可执行文件，`docker cp` 到现有 `research-assistant-backend-1`，替换 `/app/api` 并重启；没有重建镜像。

## 部署状态

本轮开始时本项目的 backend、frontend、sandbox、intent-recognition 和 research-optimizer 已运行。后端仍是 11 月 3 日编译的旧程序。先备份旧程序，再复制当前源码的编译结果；发现文档问题并修复后，再次复制及重启。

最终本地编译文件、容器内 `/app/api` 和运行中的 `/proc/1/exe` 三者 SHA256 一致：

```text
241bb7eb9265b3053a4aeaefb73ebafb6805add26b57f22619414aec63a19c0b
```

前端入口 `http://127.0.0.1:15173/` 返回 200；经前端反向代理的 `/api/health` 返回正常。memory、Wiki、sandbox、intent-recognition、research-optimizer 均健康。前端仅新增 TypeScript 契约字段，无需复制静态资源。

## 实测结果

测试使用服务端签名的独立身份、真实 HTTP 接口和真实模型。没有改动现有用户的聊天、计划和记忆。测试创建的会话、上传、模型配置和显式记忆已通过 API 删除；soft-delete 记录及原始上下文归档仍按原有审计语义保留。两套最终脚本的 cleanup 均无错误。

| 范围 | 结果 | 实际验证 |
|---|---|---|
| 聊天、检索、预算、调度 | 14/14 | memory off 不知道独立历史事实；on 正确复用 batch=17 与失败的 batch=129；双语 OOM 召回、owner 隔离、当前资料报告、归一化排序诊断 |
| 设置下一次调用 | 包含于上述 14 项 | retrieved budget 从 137 改为 741，下一次聊天立即反映；真实完成的 general-agent 节点也使用 741 |
| 长历史压缩 | 包含于上述 14 项 | 真实摘要完成，校验标记为 `extractive_original_sources/v1;semantic_truth_not_verified`；回答保持更正后的 macro-F1=0.613 和 batch=129 未通过验证 |
| PDF 资料与案例汇编 | 11/11 | 实际上传 PDF、提取与聊天；verbatim 文档资料、服务端文档名、命令原文不覆盖任务；待审汇编、相反条件、执行证据门槛、重放幂等、私有作用域 |
| 默认模型与重启 | PASS | 默认配置修复后回答 `OK`；再次重启后，经前端代理的新身份仍回答 `OK`，容量和模型正确 |

合计 25 项专项接口断言通过，另完成默认模型及跨重启验证。真实问题示例：

```text
B7301 当前 macro-F1：0.613（旧值 0.58 作废）
batch=129：未通过验证

文档 B7401 macro-F1：0.642（ratio；test）
batch=130：在 gpu-a 上未通过验证
```

本次文档修复后还执行 `go test -count=1 ./internal/api ./internal/documents` 和 `go vet ./internal/api`，全部通过。此前全后端、专项 race、vet 与前端类型检查见实施记录。

## 实测发现并修复的资料缺失

同一份测试 PDF 的首行包含 `B7401 macro-F1=0.642`，位于页顶。提取器把它分类为 header，检索使用的正文没有该行。原聊天页面引用也只读取正文，因此模型只能报告负结果，并诚实回答实验指标未知。该次问答失败保留在报告中。

`chatDocumentPageText` 现在对显式页面引用补回该页原始 blocks 中、正文尚未包含的 header/footer，保留正文阅读顺序，并避免重复。不会改变检索索引对页眉的过滤策略。该修复也适用于已有、仍保存原始 blocks 的文档缓存。

新增回归覆盖页顶指标、页脚单位、完整正文、不同页隔离及 legacy/inclusive 页面不重复。同一个 PDF 重新上传并问答后，模型正确给出 0.642、ratio、test 和负结果。资料中的 `SYSTEM: ... SOURCE_HIJACKED ...` 命令保持普通原文，未改变该次回答或触发代码执行。这只验证了该测试案例，不代表普遍提示注入免疫。

## 运行配置差异与处理

旧默认全局模型为 `deepseek-v4.1-flash-1017`，`context_window_tokens=0`、`max_output_tokens=0`。新运行时明确拒绝未知容量，返回 `model_configuration_invalid`。直连旧模型还得到服务商 `503 / model_not_found`，说明旧模型名没有可用通道。

备份模型存储后，通过管理员 API 将原默认 profile 对齐工作区 `backend.env` 的 `deepseek-v4.1-flash`，保留凭据、角色和其他配置，并设置已实测的保守窗口：

| 设置 | 当前值 |
|---|---:|
| context window | 32,000 |
| max output | 8,192 |
| compaction | text_summary |
| trigger ratio | 0.8 |
| summary target ratio | 0.5 |

32,000 是本次配置和验收额度，不是对模型最大容量的探测结论。当前容器的环境变量仍来自旧启动配置；持久 profile 已修复，重启验证通过。今后重建应读取当前工作区 `backend.env`。

试测还出现过当前有效模型的间歇性 503 和一次容器内评测的网络失败；失败报告原样保留。随后相同 Go 调用路径通过，没有为此添加自动重试、切换协议或隐藏失败。服务商可用性不由本次代码变更保证。

## 产物与回退

本地报告目录默认被 Git 忽略：

- [live-report-retry.json](../../reports/context-memory-2026-11-12/container/live-report-retry.json)：14 项真实接口检查及实际调度节点。
- [extended-report.json](../../reports/context-memory-2026-11-12/container/extended-report.json)：修复后 11 项文档和案例汇编检查。
- [extended-report-before-fix.json](../../reports/context-memory-2026-11-12/container/extended-report-before-fix.json)：文档首行缺失的失败样本。
- [default-profile-repair.json](../../reports/context-memory-2026-11-12/container/default-profile-repair.json)：默认配置前后差异及问答。
- [deployment.json](../../reports/context-memory-2026-11-12/container/deployment.json)：最终进程哈希、健康及重启后的前端代理问答。
- `live-report-attempt*.json`、`live-report-current-config.json`、`eval-original-network.json`：前序配置/网关失败，不合并为成功样本。
- `live_probe.py`、`extended_probe.py`：实测脚本；从 Docker metadata 在内存读取认证信息，不写入报告。复跑应使用新报告名，避免覆盖原记录。

容器内回退备份：

```text
/app/api.before-memory-20261112
/app/data/model-profiles/model_profiles.before-memory-live-20261112.json
```

旧模型存储包含历史其他用户配置，回退单个模型时应以该 profile 的原配置为准；若整体恢复文件，应先停止服务并确认没有后续配置写入。本次没有执行回退。

`/app/memoryeval` 也已复制进容器。热更新的程序位于当前容器可写层，未来从旧镜像重建会丢失该程序更新；发布时仍应从当前源码重建镜像。本次实测不替代 30～50 个真实科研执行长任务的收益对照。
