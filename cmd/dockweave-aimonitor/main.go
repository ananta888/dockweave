// dockweave-aimonitor is the optional AI monitor agent. It runs unprivileged,
// polls the restricted monitoring API (activity, conflicts), applies
// deterministic analysis rules and submits reports through the daemon's
// report endpoint (and optionally a webhook). The system works fully
// without this component.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ananta888/dockweave/internal/aimonitor"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
)

func main() {
	socket := flag.String("socket", envOr("DOCKWEAVE_SOCKET", "/run/dockweave/dockweaved.sock"), "daemon socket")
	interval := flag.Duration("interval", time.Minute, "poll interval")
	inactivity := flag.Duration("inactivity", 15*time.Minute, "idle duration before inactivity finding")
	cpu := flag.Float64("cpu-threshold", 90, "CPU percent threshold")
	memMB := flag.Int64("mem-threshold-mb", 4096, "memory threshold in MiB")
	webhook := flag.String("webhook", "", "optional webhook URL receiving reports as JSON")
	once := flag.Bool("once", false, "run a single cycle and exit")
	flag.Parse()

	cfg := aimonitor.Config{
		Inactivity:          *inactivity,
		CPUThresholdPercent: *cpu,
		MemThresholdMB:      *memMB,
	}
	client := sdk.New(*socket)
	state := aimonitor.NewState()

	cycle := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		acts, err := client.Activity(ctx)
		if err != nil {
			log.Printf("activity: %v", err)
			return
		}
		confPtrs, err := client.Conflicts(ctx)
		if err != nil {
			log.Printf("conflicts: %v", err)
			confPtrs = nil
		}
		var conflicts []model.FileConflict
		for _, c := range confPtrs {
			conflicts = append(conflicts, *c)
		}
		rep := aimonitor.Analyze(state, acts, conflicts, cfg, time.Now())
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
		submitReport(ctx, client, rep, *webhook)
	}

	if *once {
		cycle()
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	log.Printf("dockweave-aimonitor: interval=%s inactivity=%s cpu=%.0f%% mem=%dMiB webhook=%v",
		*interval, *inactivity, *cpu, *memMB, *webhook != "")
	cycle()
	for {
		select {
		case <-sig:
			log.Println("shutting down")
			return
		case <-ticker.C:
			cycle()
		}
	}
}

func submitReport(ctx context.Context, client *sdk.Client, rep *aimonitor.Report, webhook string) {
	// Submit to the daemon's restricted report endpoint (audit trail).
	if err := client.SubmitMonitorReport(ctx, rep); err != nil {
		log.Printf("submit report: %v", err)
	}
	if webhook == "" {
		return
	}
	b, err := json.Marshal(rep)
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(wctx, http.MethodPost, webhook, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	resp.Body.Close()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
