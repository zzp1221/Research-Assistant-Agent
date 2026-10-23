# Phase 3 — 复跑与 Docker/磁盘清理

执行时间：2026-10-23 15:10–15:42（Asia/Shanghai）

## 1. 复跑结果

### AutoResearch — 通过

- 计划：`e71d493c-a8e8-49dc-b3ed-389a84fabff5`
- 结果：8/8 节点完成，20 个 artifact
- 验证：`autoresearch.validation/v1`，`status=validated`
- Hidden holdout：3/3 通过，observed score = 1.0，mean = 1.0，stddev = 0
- 证据：`reports/phase3-autoresearch-retest.json`

关键修复：E2E 现在会按顺序上传 `autoresearch.json`、`evaluator.py`、`holdout_evaluator.py`；
本地 `python:3.11-bullseye` 沙箱镜像已预装 `numpy==2.1.3` 与 `pytest==8.3.5`，
不再依赖 PyPI 下载。

### Benchmark — 未通过（模型生成 adapter 质量问题）

- 计划：`ab96e413-d163-48cf-98c8-cf9503988fc9`
- adapter 生成成功；preflight 连续 3 次失败
- 原因：模型生成的 adapter 使用硬编码 `SEED_DATA` 作为训练样本，`reviews.csv`
  验证集 accuracy/macro_f1 均为 0.0；不是数据集/容器/网络问题
- 本次新增的兼容修复已生效：
  - run_manifest 接受 `ok` / `success`
  - adapter 生成遇到空 JSON 时本地重试一次
- phase 1 同 provider Benchmark 证据仍为 13/13、46 artifacts、
  `benchmark.validation/v1`（`reports/phase1-benchmark-pixel.json`）
- 本次报告：`reports/phase3-benchmark-retest.json`（计划在预检失败后被取消）

## 2. Docker 清理

清理前 `docker system df`：

| 类型 | 大小 |
|---|---:|
| Images | 16.4 GB |
| Containers | 628.8 MB |
| Local Volumes | 7.921 GB |
| Build Cache | 9.583 GB |

已删除/回收：

- 测试容器：`ra-e2e-runner`、Benchmark runner、7 个 `python:3.11` 测试沙箱
- 卷：
  - `research-assistant_research-assistant-plan-data` 1.176 GB（测试 plan 数据）
  - `research-assistant-go-build` 940.6 MB
  - `research-assistant-go-mod` 259.2 MB
  - `research-assistant-playwright-browsers` 711.3 MB
- 构建缓存：9.583 GB → 0
- 旧镜像：scholar-agent 4 个、node:20、python:3.9、golang:1.22、golang:1.26.1、
  nginx:1.28、docker:27-cli、debian:bullseye-slim 等

清理后：

| 类型 | 大小 |
|---|---:|
| Images | 7.295 GB |
| Containers | 200.7 kB |
| Local Volumes | 4.231 GB |
| Build Cache | 0 B |

## 3. 虚拟磁盘压缩

- 活动 Docker data VHDX：`E:\Docker\DockerDesktopWSL\DockerDesktopWSL\disk\docker_data.vhdx`
- 压缩前：28,291,629,056 bytes（26.3 GiB）
- 使用 Ubuntu WSL 的 `qemu-img` 转换为 dynamic VHDX，压缩后：
  24,603,787,264 bytes（22.9 GiB）
- 同时删除旧的无引用 VHDX：
  `E:\Docker\DockerDesktopWSL\disk\docker_data.vhdx`（16,423,845,888 bytes）
- E: 可用空间：54,351,257,600 → 73,512,935,424 bytes（约释放 19.2 GB）

压缩后 Docker Desktop 已重新启动，`docker compose up -d` 恢复：

- backend：`127.0.0.1:18080`
- frontend：`127.0.0.1:15173`
- `/api/health`：backend/sandbox/optimizer 全部 `ok=true`

## 4. 备注

- plan data 卷已重建为空卷；历史测试 plan 不保留。
- 未删除 `dsh-*`、`pragent_*`、`runbook_*` 等非本项目卷。
- Benchmark 的剩余问题属于模型 adapter 生成质量；若要继续，应在 prompt/validator
  中强制禁止 `SEED_DATA`/硬编码样本，再跑一次 benchmark。
