package gateexec

import (
	"os"
	"path/filepath"
	"testing"
)

// A workspace without .paperclip/gates.json (or without .paperclip/) means
// "nothing configured" and must stay a skip, not a gate error.
func TestReadConfigMissingFileIsNotAnError(t *testing.T) {
	for name, setup := range map[string]func(string){
		"no paperclip dir": func(string) {},
		"no gates.json": func(ws string) {
			if err := os.MkdirAll(filepath.Join(ws, ".paperclip"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			setup(ws)
			cfg, raw, err := readConfig(ws)
			if err != nil {
				t.Fatalf("missing gate config must not be an error: %v", err)
			}
			if raw != nil || len(cfg.Gates) != 0 {
				t.Fatalf("expected empty config, got raw=%q cfg=%+v", raw, cfg)
			}
		})
	}
}
