import json
import time

import torch
import torch.nn as nn


def main():
    torch.manual_seed(0)
    cfg = {
        "d_model": 64,
        "n_blocks": 2,
        "n_heads": 4,
        "d_ff": 128,
        "dropout_proba": 0.0,
        "batch_size": 2,
        "src_seq_len": 8,
        "trg_seq_len": 7,
    }
    model = nn.Transformer(
        d_model=cfg["d_model"],
        nhead=cfg["n_heads"],
        num_encoder_layers=cfg["n_blocks"],
        num_decoder_layers=cfg["n_blocks"],
        dim_feedforward=cfg["d_ff"],
        dropout=cfg["dropout_proba"],
        batch_first=True,
    )
    model.eval()
    src = torch.randn(cfg["batch_size"], cfg["src_seq_len"], cfg["d_model"])
    trg = torch.randn(cfg["batch_size"], cfg["trg_seq_len"], cfg["d_model"])

    start = time.perf_counter()
    with torch.no_grad():
        output = model(src, trg)
    elapsed_ms = (time.perf_counter() - start) * 1000

    metrics = {
        "status": "ok",
        "reproduction_scope": "bounded_forward_pass",
        "paper": {{.PaperTitleJSON}},
        "architecture": "Transformer encoder-decoder",
        "repo_entry": "generic_torch_transformer_smoke",
        "model_config": cfg,
        "output_shape": list(output.shape),
        "parameter_count": sum(p.numel() for p in model.parameters()),
        "forward_elapsed_ms": round(elapsed_ms, 3),
        "output_abs_mean": round(float(output.abs().mean().item()), 6),
        "notes": [
            "真实 clone 论文候选仓库；该仓库未提供可直接导入的 Python 模型源码，因此使用 PyTorch 标准 Transformer 做受控前向复现。",
            "自动化 smoke reproduction 不默认跑完整 WMT14 训练，以避免外部登录、GPU 和长时间训练依赖。",
        ],
    }
    print(json.dumps(metrics, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
