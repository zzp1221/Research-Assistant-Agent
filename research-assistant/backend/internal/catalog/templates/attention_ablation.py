import json
import math
import statistics
import time

import torch
import torch.nn as nn


DMODEL = 64
WARMUP_RUNS = 8
TIMED_RUNS = 40
DEVICE = torch.device("cuda" if torch.cuda.is_available() else "cpu")
VARIANTS = json.loads(r'''{{.VariantsJSON}}''')


class AblationAttention(nn.Module):
    def __init__(self, heads, use_scaling=True, use_residual=True):
        super().__init__()
        if DMODEL % heads != 0:
            raise ValueError("d_model must be divisible by heads")
        self.heads = heads
        self.head_dim = DMODEL // heads
        self.use_scaling = use_scaling
        self.use_residual = use_residual
        self.qkv = nn.Linear(DMODEL, DMODEL * 3, bias=False)
        self.output = nn.Linear(DMODEL, DMODEL, bias=False)

    def forward(self, x):
        batch, length, _ = x.shape
        qkv = self.qkv(x).reshape(batch, length, 3, self.heads, self.head_dim)
        q, k, v = qkv.unbind(dim=2)
        q = q.transpose(1, 2)
        k = k.transpose(1, 2)
        v = v.transpose(1, 2)
        scores = torch.matmul(q, k.transpose(-2, -1))
        if self.use_scaling:
            scores = scores / math.sqrt(self.head_dim)
        weights = torch.softmax(scores, dim=-1)
        attended = torch.matmul(weights, v).transpose(1, 2).contiguous().reshape(batch, length, DMODEL)
        output = self.output(attended)
        if self.use_residual:
            output = output + x
        entropy = -(weights * weights.clamp_min(1e-12).log()).sum(dim=-1).mean()
        return output, entropy


def run_variant(config, shared_state):
    torch.manual_seed(config["seed"])
    if torch.cuda.is_available():
        torch.cuda.manual_seed_all(config["seed"])
    inputs = torch.randn(config["batch_size"], config["sequence_length"], DMODEL, device=DEVICE)
    model = AblationAttention(config["heads"], config["use_scaling"], config["use_residual"])
    model.load_state_dict(shared_state)
    model.to(DEVICE)
    model.eval()
    with torch.no_grad():
        for _ in range(WARMUP_RUNS):
            model(inputs)
        if DEVICE.type == "cuda":
            torch.cuda.synchronize()
        elapsed = []
        output = None
        entropy = None
        for _ in range(TIMED_RUNS):
            start = time.perf_counter()
            output, entropy = model(inputs)
            if DEVICE.type == "cuda":
                torch.cuda.synchronize()
            elapsed.append((time.perf_counter() - start) * 1000.0)
    return {
        **config,
        "latency_median_ms": round(statistics.median(elapsed), 6),
        "attention_entropy": round(float(entropy.item()), 6),
        "output_l2": round(float(output.norm().item()), 6),
        "parameter_count": sum(p.numel() for p in model.parameters()),
    }


def percent_change(value, baseline):
    if baseline == 0:
        return None
    return round((value - baseline) / baseline * 100.0, 6)


def main():
    torch.manual_seed(20260717)
    if DEVICE.type == "cpu":
        torch.set_num_threads(1)
    reference = AblationAttention(4, True, True)
    shared_state = reference.state_dict()
    results = [run_variant(config, shared_state) for config in VARIANTS]
    by_name = {item["name"]: item for item in results}
    baseline = by_name["baseline"]
    comparisons = {}
    for item in results:
        if item["name"] == "baseline":
            continue
        comparisons[item["name"]] = {
            "latency_median_ms": percent_change(item["latency_median_ms"], baseline["latency_median_ms"]),
            "attention_entropy": percent_change(item["attention_entropy"], baseline["attention_entropy"]),
            "output_l2": percent_change(item["output_l2"], baseline["output_l2"]),
        }
    metrics = {
        "status": "ok",
        "reproduction_scope": "attention_structure_ablation",
        "paper": {{.PaperTitleJSON}},
        "repo_entry": "research_assistant_generated_attention_ablation",
        "device": str(DEVICE),
        "torch_version": torch.__version__,
        "config": {
            "d_model": DMODEL,
            "warmup_runs": WARMUP_RUNS,
            "timed_runs": TIMED_RUNS,
        },
        "results": results,
        "comparisons_percent_vs_baseline": comparisons,
        "notes": [
            "The repository is cloned and scanned by Research Assistant Agent before this bounded harness is generated.",
            "This is a structural smoke ablation, not WMT14 training or paper BLEU reproduction.",
        ],
    }
    print(json.dumps(metrics, ensure_ascii=True, sort_keys=True))


if __name__ == "__main__":
    main()
