# ai-services

可选的 Python AI 服务集合。当前包含意图识别服务及其训练脚本，不属于默认 Docker Compose 启动链。

## 服务

| 目录 | 说明 |
|---|---|
| `intent_recognition/` | FastAPI 意图识别服务、BERT/Qwen 训练脚本与双路由数据集 |

## 定位

主后端当前使用确定性的规则意图路由；`ai-services/intent_recognition` 保留用于实验性本地模型、离线评测和训练流程验证，生产请求默认不依赖该服务。

## 快速开始

```bash
cd intent_recognition
pip install -r requirements.txt
python main.py
```

服务默认监听 `:8000`，提供：

- `POST /predict`
- `GET /health`

## 训练

```bash
cd intent_recognition/training
pip install -r requirements.txt
python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

训练脚本读取 `data/dual_router_v1/` 下的 `train.jsonl`、`dev.jsonl`、`test.jsonl` 与 `manifest.json`。数据集字段和划分说明见 [intent_recognition/README.md](intent_recognition/README.md)。

## 边界

- `main.py` 当前是关键词 mock，不加载真实模型；替换为 BERT/Qwen 推理前不要把它当作生产分类器。
- 训练脚本需要额外依赖和 GPU/CPU 环境，默认不参与 `make test`。
- 意图识别只是规划入口的辅助能力，最终 DAG 仍由 Planner 校验。
