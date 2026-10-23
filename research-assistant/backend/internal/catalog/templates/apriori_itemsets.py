import json
import subprocess
import sys

command = [
    sys.executable,
    "apriori.py",
    "-f",
    "INTEGRATED-DATASET.csv",
    "-s",
    "0.15",
    "-c",
    "0.6",
]
completed = subprocess.run(command, capture_output=True, text=True, timeout=120)
if completed.stdout:
    sys.stdout.write(completed.stdout)
if completed.stderr:
    sys.stderr.write(completed.stderr)
metrics = {
    "paper": {{.PaperTitleJSON}},
    "exit_code": completed.returncode,
    "frequent_itemsets": completed.stdout.count("item: "),
    "association_rules": completed.stdout.count("Rule: "),
}
print(json.dumps({"paper_smoke_metrics": metrics}, sort_keys=True))
raise SystemExit(completed.returncode)
