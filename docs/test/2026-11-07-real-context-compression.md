# 真实模型上下文压缩测试

日期：2026-11-07；分支：`feature/context`。所有编译和测试均在 Docker 中执行。网络请求使用项目实际配置的凭据，仅发送合成实验记录，不读取用户聊天内容。

## 结果

| 项目 | 真实返回 | 结论 |
|---|---|---|
| 已配置模型 `deepseek-v4.1-flash-1017` 的普通聊天 | HTTP 503，`model_not_found`，无可用 channel | 当前配置模型名已不可用 |
| 服务商模型列表 | 返回 `deepseek-v4.1-flash`、`glm-5.3-flash`、`minimax-m3.1-flash` | 后续仅以测试覆盖名称使用 `deepseek-v4.1-flash` |
| `deepseek-v4.1-flash` Chat Completions | HTTP 200，返回 `OK` | 基本生成通过 |
| `deepseek-v4.1-flash` Responses create | HTTP 200，返回 `OK` | Responses 基本生成通过 |
| `/responses/compact` | HTTP 503，`model_not_found`，`no channel supports /v1/responses/compact` | 当前账户/路由无法使用独立原生压缩 |
| `/responses/input_tokens` | HTTP 404，`Invalid URL` | 无法启用项目所需的原生密文计数 |
| Responses SSE + `context_management` | HTTP 200，完整 completed 响应；输入 usage 为 3051 tokens、阈值 2048，输出仅 reasoning/message，无 compaction item | 接收参数，但本次未证明服务端实际压缩；不能据此配置能力为 yes |
| `text_summary` + 后续生成 | 1 次摘要 + 1 次生成，摘要契约与 9 项答案字段断言通过 | 真实文本摘要压缩通过 |

网关地址为 `https://api.ai-pixel.online/v1`。上述原生能力结果仅代表本次账户、模型和网关路由；不推断模型厂商的全局能力。服务商 `/models` 对该模型宣称支持 `openai-response-compact`，但实际 compact 请求被拒绝，因此以实际返回为准。

## 文本压缩的实际证据

通过 `modelresolver.CompleteManaged` 执行真实摘要、校验、落盘及后续生成。历史中的关键记录放在会被摘要覆盖的中间消息，包含划分 80/20、seed 47、macro-F1、失败 B17=0.41、最佳 B23=0.58、无网络批准和禁止用测试标签训练。最新输入只请求这些字段，不重新提供答案。最终返回：

```json
{"split":"80/20","seed":47,"metric":"macro-F1","failed_trial":"B17","failed_score":0.41,"best_trial":"B23","best_score":0.58,"network_approved":false,"test_labels_for_training":false}
```

成功测试使用临时本地窗口 16384、输出预算 4096、触发比例 0.30；这些是用于强制触发的小预算，不代表已验证的模型最大容量。正式 profile 及环境未修改。

- 相同本地估算方法：原输入 4023 → 摘要后生成输入 891 tokens，减少约 77.9%；此百分比是本地估算，不是服务商精确压缩率。
- 摘要服务商 usage：输入 3023、输出 3574，其中推理 token 2693；可见摘要约 683 tokens。
- 后续生成 usage：输入 1034、输出 178；两次请求合计输入 4057、输出 3752 tokens。
- 摘要耗时 26.699 秒，后续生成约 5.369 秒，整轮约 32.07 秒。
- 状态报告为 `text_summary / completed`，generation 2，HTTP 请求计数为压缩 1、生成 1、计数 0。
- 当前没有已核验的 USD 费率，费用保持 unknown；不将网关 credit 字段冒充美元费用。

另测较小的 1536/2048 输出预算时，推理 token 消耗导致 `finish_reason=length`、JSON 截断。项目拒绝无效摘要，原输入仍可容纳时继续使用原件生成，没有把回退误判成压缩成功。故该模型不能用可见摘要长度代替实际输出预算。

## 重现与边界

测试文件为 `research-assistant/backend/internal/modelresolver/runtime_live_test.go`；默认跳过，不会让常规 `go test` 访问真实模型。容器中可显式设置 `RUN_CONTEXT_LIVE_TESTS=1`，可用 `CONTEXT_LIVE_MODEL` 临时覆盖模型名、`CONTEXT_LIVE_OWNER` 选择路由 owner。

测试 binary 在 Go 构建容器编译后复制到实际后端容器运行，凭据在该容器内读取，没有导出凭据到宿主机。文本测试的本地 relay 只透传真实 HTTP 请求/响应并观察合成摘要诊断，不伪造回答。源码包的普通测试和 vet 也在容器中通过。

本次只验证了一组合成记录，未证明长科研任务的整体质量、实际服务容量或净费用节省。原生 compact 和计数接口不可用，因此无法完成该部署的原生压缩后生成闭环；server SSE 只证明正常生成与完整事件解析。未修改正式模型名称、窗口或能力配置，运行服务未替换为新镜像。测试容器、复制的 binary 和临时状态在收尾清理。

官方协议对照：[OpenAI Compaction](https://developers.openai.com/api/docs/guides/compaction)。本轮已实际读取该页；服务商能力仍以上表的真实请求为准。
