# ask_user_question

`ask_user_question` 实现 Agent 的主动追问能力：当关键信息缺失时，工具创建待回答问题、中断当前执行，等宿主把用户答案传回后再恢复。

## 文件

- `ask_user_question.go`：工具实现、问题管理器、内存存储与恢复结构
- `ask_user_question_test.go`：基础单元测试

## 提供的能力

### 可挂到 Eino 的 Tool

`AskUserQuestionTool` 实现 Eino 的 `InvokableTool` 接口：

```go
tool := askuserquestion.NewAskUserQuestionTool(nil)
```

传入 `nil` 时使用默认内存存储。

### 可被宿主直接操作的管理器

```go
manager := askuserquestion.NewAskUserQuestionManager(nil)
tool := askuserquestion.NewAskUserQuestionTool(manager)
```

管理器支持创建问题、查询问题、列出 pending 问题和写入答案。

## 入参

```json
{
  "session_id": "session-123",
  "task_id": "task-456",
  "agent": "coder_agent",
  "question": "你希望使用论文原始超参数还是快速验证配置？",
  "reason": "训练配置会直接影响后续代码生成和运行时长。",
  "context": "当前任务是复现训练脚本。",
  "required": true,
  "suggested_answers": [
    { "label": "原始配置", "value": "使用论文原始超参数" },
    { "label": "快速验证", "value": "使用快速验证配置" }
  ]
}
```

## 运行时行为

1. 工具把问题写入 `QuestionStore`，生成 `pending` 状态的 `UserQuestion`。
2. 调用 `tool.StatefulInterrupt`，当前 Agent 执行暂停。
3. 宿主读取 `AskUserQuestionInterrupt`，把问题渲染给用户。
4. 用户回答后，宿主用 `AskUserQuestionResume` 恢复执行。
5. 工具返回用户答案字符串，Agent 继续后续步骤。

## 直接操作 Manager

```go
ctx := context.Background()
manager := askuserquestion.NewAskUserQuestionManager(nil)

question, err := manager.Ask(ctx, askuserquestion.AskUserQuestionInput{
    SessionID: "session-1",
    TaskID:    "task-1",
    Agent:     "coder_agent",
    Question:  "请确认数据集下载地址",
    Reason:    "没有数据集地址无法继续生成训练脚本",
    Required:  true,
})
if err != nil {
    panic(err)
}

answered, err := manager.Answer(ctx, question.ID, askuserquestion.AnswerUserQuestionInput{
    Answer: "https://example.com/dataset.zip",
})
if err != nil {
    panic(err)
}
_ = answered
```

## 当前限制

- `QuestionStore` 默认是内存实现，服务重启后问题丢失。
- 尚未接入 HTTP API 与前端 UI。
- 尚未实现问题过期、超时和取消机制。

## 后续接入顺序

1. 增加 pending/answer HTTP 接口。
2. 前端展示 pending question 并提交答案。
3. 让具体 Agent 通过 tool calling 使用该工具。
