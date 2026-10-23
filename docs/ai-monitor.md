# AI Monitor Agent (Optional)

`dockweave-aimonitor` is a standalone, unprivileged component that
summarizes fleet activity and detects anomalies. The system is fully
functional without it.

## Rules (deterministic, evidence-based)

| Finding | Severity | Trigger |
|---|---|---|
| `conflict` | critical | same file held by multiple agents with ≥1 writer |
| `inactivity` | info | agent running but idle longer than the window |
| `resource` | warning | CPU/memory above thresholds; monitoring unavailable |
| `distribution` | info | several agents running, none active |

## Run

```bash
dockweave-aimonitor \
  --interval 60s \
  --inactivity 15m \
  --cpu-threshold 90 \
  --mem-threshold-mb 4096 \
  --webhook https://hooks.example/dockweave
```

`--once` performs a single analysis cycle (for cron-based setups).

## Security model

- Talks **only** to the restricted monitoring API:
  `GET /activity`, `GET /conflicts`, `POST /monitor/reports`.
- The report endpoint is the single write surface: it validates and
  size-limits the payload and appends a `monitor.report` audit event.
  It cannot influence grants, mounts or agent state.
- No host access, no docker socket, no privileges.
- No LLM integration by default; analysis is deterministic. External LLM
  hookups belong behind the webhook (user-operated).
