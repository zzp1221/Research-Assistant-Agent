# intent_recognition

意图识别实验服务与双路由训练工程。它包含一个 FastAPI mock 服务、BERT/Qwen 训练脚本，以及固定的 train/dev/test 数据集。

## 目录

```text
intent_recognition/
├── main.py                  FastAPI 服务（当前为关键词 mock）
├── requirements.txt         服务依赖
├── data/dual_router_v1/
│   ├── train.jsonl          训练集
│   ├── dev.jsonl            验证集
│   ├── test.jsonl           测试集
│   └── manifest.json        数据来源、版本与统计
└── training/
    ├── build_dataset.py     数据构建与清洗
    ├── common.py            共享工具
    ├── train_bert.py        CPU BERT/BGE 分类器
    ├── train_qwen_lora.py   GPU Qwen LoRA 生成式路由
    └── requirements.txt     训练依赖
```

## 服务

```bash
pip install -r requirements.txt
python main.py
```

默认监听 `:8000`：

- `POST /predict`：`{"text": "..."}` -> `{"intent_type": "...", "confidence": 0.95, "entities": {}}`
- `GET /health`：`{"status": "ok"}`

当前 `main.py` 使用关键词规则模拟推理，未加载真实模型。接入 BERT/Qwen 前不要把它当作生产分类器。

## 数据

`data/dual_router_v1/` 使用 JSONL，每行至少包含文本与标签字段。`manifest.json` 记录数据版本、来源、split 数量和字段说明。训练脚本通过 `common.py` 统一读取和校验。

## 训练

```bash
cd training
pip install -r requirements.txt

python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

- `train_bert.py`：CPU 友好的编码器分类方案，适合低延迟部署。
- `train_qwen_lora.py`：GPU LoRA 生成式路由，输出结构化意图 JSON。
- 训练产物默认写入脚本同级的输出目录，具体参数见各脚本的 argparse。

## 评测

训练脚本会在 dev/test 上输出分类指标和混淆矩阵。由于数据和模型规模有限，结果只用于工程验证，不代表通用意图识别能力。

## 边界

- 该目录不参与默认 Docker Compose。
- 训练依赖较大，默认不进入 `make test`。
- 主后端已有 LLM 意图分类器；本服务是可选实验路径。
