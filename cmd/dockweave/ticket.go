package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type ticketProviderView struct {
	Name   string `json:"name"`
	Health struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"health"`
	Capabilities map[string]bool `json:"capabilities"`
}

func ticketCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ticket", Short: "Ticketing (ADR 0003): provider status and setup"}
	provider := &cobra.Command{
		Use:   "provider",
		Short: "Show the configured ticket provider, mirror, health and capabilities",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			var out struct {
				Configured bool                `json:"configured"`
				Available  []string            `json:"available"`
				Error      string              `json:"error"`
				Primary    *ticketProviderView `json:"primary"`
				Mirror     *ticketProviderView `json:"mirror"`
			}
			if err := client().Do(context.Background(), http.MethodGet, "/api/v1/tickets/provider", nil, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			if !out.Configured {
				fmt.Printf("ticketing: off – %s\n(available: %s; `dockweave ticket provider detect` proposes a setting)\n", out.Error, strings.Join(out.Available, ", "))
				return nil
			}
			for _, v := range []struct {
				role string
				p    *ticketProviderView
			}{{"primary", out.Primary}, {"mirror", out.Mirror}} {
				if v.p == nil {
					fmt.Printf("%-8s none\n", v.role)
					continue
				}
				state := "ok"
				if !v.p.Health.OK {
					state = "UNAVAILABLE"
				}
				var caps []string
				for k, on := range v.p.Capabilities {
					if on {
						caps = append(caps, k)
					}
				}
				sort.Strings(caps)
				fmt.Printf("%-8s %-10s %s %s  [%s]\n", v.role, v.p.Name, state, v.p.Health.Detail, strings.Join(caps, ","))
			}
			return nil
		},
	}
	var pcURL string
	detect := &cobra.Command{
		Use:   "detect",
		Short: "Propose TICKET_PROVIDER for dockweaved.conf (setup only; never switches a running daemon)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			version, err := paperclipVersion(ctx, pcURL)
			if err != nil {
				fmt.Printf("Paperclip not reachable at %s (%v).\nProposal for /etc/dockweave/dockweaved.conf:\n\n  TICKET_PROVIDER=native\n", pcURL, err)
				return nil
			}
			fmt.Printf("Paperclip %s reachable at %s.\nProposal for /etc/dockweave/dockweaved.conf:\n\n  TICKET_PROVIDER=paperclip\n  PAPERCLIP_URL=%s\n  # optional live mirror into the native store (ADR 0003 D-8):\n  # TICKET_MIRROR=native\n", version, pcURL, pcURL)
			fmt.Println("\nThen restart dockweaved. The provider is never switched automatically.")
			return nil
		},
	}
	detect.Flags().StringVar(&pcURL, "paperclip-url", envOr("PAPERCLIP_API_URL", "http://127.0.0.1:3100"), "Paperclip base URL")
	provider.AddCommand(detect)
	cmd.AddCommand(provider)
	return cmd
}

func paperclipVersion(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var st struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil || st.Status != "ok" {
		return "", fmt.Errorf("unexpected health answer (HTTP %d)", resp.StatusCode)
	}
	return st.Version, nil
}
