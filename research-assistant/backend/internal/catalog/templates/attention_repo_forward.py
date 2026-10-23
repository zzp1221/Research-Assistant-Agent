import json
import os
import sys
import time

import torch

sys.path.insert(0, os.path.join(os.getcwd(), "src"))
from architectures.machine_translation_transformer import MachineTranslationTransformer


def main():
    torch.manual_seed(0)
    cfg = {
        "d_model": 64,
        "n_blocks": 2,
        "n_heads": 4,
        "d_ff": 128,
        "dropout_proba": 0.0,
        "src_vocab_size": 96,
        "trg_vocab_size": 96,
        "batch_size": 2,
        "src_seq_len": 8,
        "trg_seq_len": 9,
    }
    model = MachineTranslationTransformer(
        d_model=cfg["d_model"],
        n_blocks=cfg["n_blocks"],
        src_vocab_size=cfg["src_vocab_size"],
        trg_vocab_size=cfg["trg_vocab_size"],
        n_heads=cfg["n_heads"],
        d_ff=cfg["d_ff"],
        dropout_proba=cfg["dropout_proba"],
    )
    model.eval()
    src = torch.randint(1, cfg["src_vocab_size"], (cfg["batch_size"], cfg["src_seq_len"]))
    trg = torch.randint(1, cfg["trg_vocab_size"], (cfg["batch_size"], cfg["trg_seq_len"]))

    start = time.perf_counter()
    with torch.no_grad():
        output = model(src, trg)
    elapsed_ms = (time.perf_counter() - start) * 1000

    metrics = {
        "status": "ok",
        "reproduction_scope": "bounded_forward_pass",
        "paper": {{.PaperTitleJSON}},
        "architecture": "Transformer encoder-decoder",
        "repo_entry": "src/architectures/machine_translation_transformer.py",
        "model_config": cfg,
        "output_shape": list(output.shape),
        "parameter_count": sum(p.numel() for p in model.parameters()),
        "forward_elapsed_ms": round(elapsed_ms, 3),
        "output_abs_mean": round(float(output.abs().mean().item()), 6),
        "notes": [
            "真实导入仓库 Transformer 模型代码并在 CPU 上执行前向传播。",
            "自动化 smoke reproduction 不默认跑完整 WMT14 训练，以避免外部登录、GPU 和长时间训练依赖。",
        ],
    }
    print(json.dumps(metrics, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
