# Reports

[简体中文](README.md) | English

This directory holds **local/container-generated** acceptance reports and E2E artifacts; it is not committed by default.

`.gitignore` conventions:

- Only this `README.md` (and its English version) is kept;
- JSON, Markdown, PNG, PDF fixtures, and evaluation reports under `reports/**` are local artifacts;
- Conclusions worth keeping should be redacted and moved into `docs/` or module READMEs rather than committing raw run logs.

## How reports are generated

E2E scripts live in `research-assistant/test/e2e/` and write to `/reports` inside containers. Example:

```bash
docker run --rm --network research-assistant_research-assistant-network \
  -v "$PWD/research-assistant/test/e2e:/scripts:ro" \
  -v "$PWD/reports:/reports" \
  -e RA_API_URL=http://backend:8080 -e RA_API_TOKEN=*** \
  node:22-bookworm-slim node /scripts/http-smoke.mjs
```

## Historical reports

The following directories are local artifacts (not committed) useful for reruns:

- `reports/eval-2026-10-23/`: Phase 1 scorecard and real-provider acceptance (PDF/translation/plotting/benchmark).
- `reports/eval-2026-10-24/`: four-scenario regression, translation follow-up tests, defect list.
- `reports/eval-2026-10-25/`: Louvain paper four-scenario regression and root-cause analysis.
- `reports/eval-2026-10-28/`: 2026-10-28 large-scale test (`REPORT.md`, `summary.json`, round7 logs, plotting/paper/Apriori/rank_bm25/Playwright evidence and cleanup).
- `reports/p0-acceptance/`, `reports/p1-acceptance/`, `reports/rc4-eval/`: P0/P1/RC4 stage acceptance evidence.

Conclusions should be synced to [docs/verification.md](../docs/verification.md); commit-oriented changes go to [docs/CHANGELOG.md](../docs/CHANGELOG.md).
