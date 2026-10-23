# Tetragon Integration (Optional eBPF Monitoring)

DockWeave's Tetragon provider adds eBPF-based file-access evidence on top of
the always-available docker + procfs providers. It is **optional**; the
system is fully functional without it.

## How it works

```
Tetragon agent (eBPF, host) ──> TracingPolicy: security_file_open
        │
        ▼
tetra getevents --follow  ──>  /var/lib/dockweave/tetragon-events.jsonl
                                        │
                                        ▼
dockweaved (DOCKWEAVE_TETRAGON_EVENTS=/var/lib/dockweave/tetragon-events.jsonl)
        │  JSONL tail + PID attribution (docker top)
        ▼
Activity/Conflict enrichment
```

## Setup

1. Deploy Tetragon on the Docker host (non-Kubernetes):

```bash
docker run -d --name tetragon --rm --privileged \
  -v /sys/kernel:/sys/kernel:ro \
  -v /var/run/docker.sock:/var/run/docker.sock \
  quay.io/cilium/tetragon-ci:latest
```

2. Apply the DockWeave TracingPolicy (see
   `integrations/tetragon/tracing-policy.yaml`).

3. Export events to the JSONL file the daemon reads:

```bash
docker exec tetragon tetra getevents --follow \
  > /var/lib/dockweave/tetragon-events.jsonl
```

4. Start the daemon with the provider enabled:

```bash
DOCKWEAVE_TETRAGON_EVENTS=/var/lib/dockweave/tetragon-events.jsonl \
  sudo dockweaved
```

`dockweave status` lists the provider as `tetragon: ok`.

## Attribution model

- Events carry host PIDs. The daemon maps PIDs to agents via `docker top`
  (`BuildPIDMap`).
- Host-namespace paths are matched against agent workspace trees.
- Container paths (`/workspace/<name>/...`) are attributed **only** via the
  PID map — never heuristically assigned to agents.

## Documented limitations

- eBPF event delivery is best-effort; there is **no loss-free guarantee**
  over all file operations of all processes.
- Only events captured by the deployed TracingPolicy are visible.
- Very short-lived files may never appear (open/close between polls).
- The export file can rotate; the provider re-reads what is present and
  tolerates partial lines.
