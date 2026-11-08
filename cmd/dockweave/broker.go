package main

// CLI commands for the credential broker. Secret values are only ever read
// from stdin (or a terminal password prompt) and sent to the broker's
// operator socket; they are never accepted as command-line arguments.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ananta888/dockweave/internal/brokerclient"
	"github.com/ananta888/dockweave/internal/credentialbroker"
)

func defaultBrokerAdminSocket() string {
	if v := os.Getenv("DOCKWEAVE_BROKER_ADMIN_SOCKET"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "dockweave", "broker", "admin.sock")
}

func brokerAdminClient(addr string) *brokerclient.Client {
	if addr == "" {
		addr = defaultBrokerAdminSocket()
	}
	return brokerclient.New(addr)
}

func addAdminSocketFlag(c *cobra.Command, dst *string) {
	c.PersistentFlags().StringVar(dst, "admin-socket", defaultBrokerAdminSocket(), "broker admin unix socket")
}

// readSecretInput reads a secret from stdin. On a terminal it uses a hidden
// prompt; otherwise it reads all of stdin. It never echoes the value.
func readSecretInput() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Secret: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func secretCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Manage encrypted credential values"}
	var adminSocket string

	var provider string
	put := &cobra.Command{
		Use:   "put NAME",
		Short: "Store or replace a credential (value read from stdin)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			value, err := readSecretInput()
			if err != nil {
				fail(err)
			}
			if value == "" {
				fail(errors.New("secret value is empty"))
			}
			if err := brokerAdminClient(adminSocket).PutSecret(ctx(), args[0], provider, value); err != nil {
				fail(err)
			}
			fmt.Printf("stored %s (provider %s)\n", args[0], provider)
		},
	}
	put.Flags().StringVar(&provider, "provider", "", "provider adapter (openrouter|openai|anthropic|deepseek|gemini)")
	_ = put.MarkFlagRequired("provider")

	list := &cobra.Command{
		Use:   "list",
		Short: "List credential metadata (never values)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			list, err := brokerAdminClient(adminSocket).ListSecrets(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "NAME\tPROVIDER\tUPDATED")
				for _, m := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\n", m.Name, m.Provider, m.UpdatedAt.Format(time.RFC3339))
				}
			})
		},
	}

	rotate := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Replace a credential value (provider is kept)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			c := brokerAdminClient(adminSocket)
			list, err := c.ListSecrets(ctx())
			if err != nil {
				fail(err)
			}
			provider := ""
			for _, m := range list {
				if m.Name == args[0] {
					provider = m.Provider
				}
			}
			if provider == "" {
				fail(fmt.Errorf("secret %q not found", args[0]))
			}
			value, err := readSecretInput()
			if err != nil {
				fail(err)
			}
			if value == "" {
				fail(errors.New("secret value is empty"))
			}
			if err := c.PutSecret(ctx(), args[0], provider, value); err != nil {
				fail(err)
			}
			fmt.Printf("rotated %s\n", args[0])
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke NAME",
		Short: "Delete a credential value",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := brokerAdminClient(adminSocket).DeleteSecret(ctx(), args[0]); err != nil {
				fail(err)
			}
			fmt.Printf("revoked %s\n", args[0])
		},
	}

	cmd.AddCommand(put, list, rotate, revoke)
	addAdminSocketFlag(cmd, &adminSocket)
	return cmd
}

func credentialCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "credential", Short: "Manage per-agent credential grants"}
	var adminSocket string

	var (
		models      string
		rpm         int
		concurrent  int
		reqDay      int
		reqMonth    int
		tokensDay   int64
		tokensMonth int64
		usdDay      float64
		usdMonth    float64
		promptUSD   float64
		complUSD    float64
		ttl         string
	)
	grant := &cobra.Command{
		Use:   "grant AGENT CREDENTIAL",
		Short: "Authorize an agent to use a credential (DENY by default)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			var expires *time.Time
			if ttl != "" {
				d, err := time.ParseDuration(ttl)
				if err != nil {
					fail(fmt.Errorf("ttl: %w", err))
				}
				t := time.Now().Add(d)
				expires = &t
			}
			modelList := splitCSV(models)
			g, err := brokerAdminClient(adminSocket).AddGrant(ctx(), credentialbroker.Grant{
				Agent: args[0], Credential: args[1], Models: modelList,
				ExpiresAt: expires,
				Limits: credentialbroker.Limits{
					MaxRequestsPerMinute: rpm, MaxConcurrent: concurrent,
					MaxRequestsPerDay: reqDay, MaxRequestsPerMonth: reqMonth,
					MaxTokensPerDay: tokensDay, MaxTokensPerMonth: tokensMonth,
					MaxCostUSDPerDay: usdDay, MaxCostUSDPerMonth: usdMonth,
					PromptUSDPer1K: promptUSD, CompletionUSDPer1K: complUSD,
				},
			})
			if err != nil {
				fail(err)
			}
			out(g, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "GRANT\t%s\nAGENT\t%s\nCREDENTIAL\t%s\nPROVIDER\t%s\nMODELS\t%s\n",
					g.ID, g.Agent, g.Credential, g.Provider, strings.Join(g.Models, ","))
			})
		},
	}
	gf := grant.Flags()
	gf.StringVar(&models, "models", "*", "comma-separated model allowlist (* = any)")
	gf.IntVar(&rpm, "rpm", 60, "max requests per minute")
	gf.IntVar(&concurrent, "concurrent", 4, "max concurrent requests")
	gf.IntVar(&reqDay, "requests-day", 0, "max requests per day (0 = unlimited)")
	gf.IntVar(&reqMonth, "requests-month", 0, "max requests per month (0 = unlimited)")
	gf.Int64Var(&tokensDay, "tokens-day", 0, "max tokens per day (0 = unlimited)")
	gf.Int64Var(&tokensMonth, "tokens-month", 0, "max tokens per month (0 = unlimited)")
	gf.Float64Var(&usdDay, "usd-day", 0, "max estimated cost per day in USD (0 = unlimited)")
	gf.Float64Var(&usdMonth, "usd-month", 0, "max estimated cost per month in USD (0 = unlimited)")
	gf.Float64Var(&promptUSD, "prompt-usd-per-1k", 0, "prompt price per 1K tokens for cost estimates")
	gf.Float64Var(&complUSD, "completion-usd-per-1k", 0, "completion price per 1K tokens for cost estimates")
	gf.StringVar(&ttl, "ttl", "", "grant expiry (e.g. 24h)")

	var listAgent string
	list := &cobra.Command{
		Use:   "list",
		Short: "List credential grants",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			list, err := brokerAdminClient(adminSocket).ListGrants(ctx(), listAgent)
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "ID\tAGENT\tCREDENTIAL\tPROVIDER\tMODELS\tEXPIRES")
				for _, g := range list {
					exp := "-"
					if g.ExpiresAt != nil {
						exp = g.ExpiresAt.Format(time.RFC3339)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.Agent, g.Credential,
						g.Provider, strings.Join(g.Models, ","), exp)
				}
			})
		},
	}
	list.Flags().StringVar(&listAgent, "agent", "", "filter by agent")

	revoke := &cobra.Command{
		Use:   "revoke AGENT CREDENTIAL",
		Short: "Revoke an agent's credential grant",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c := brokerAdminClient(adminSocket)
			grants, err := c.ListGrants(ctx(), args[0])
			if err != nil {
				fail(err)
			}
			found := false
			for _, g := range grants {
				if g.Credential == args[1] {
					if err := c.DeleteGrant(ctx(), g.ID); err != nil {
						fail(err)
					}
					found = true
				}
			}
			if !found {
				fail(errors.New("grant not found"))
			}
			fmt.Println("revoked")
		},
	}

	var usageID string
	usage := &cobra.Command{
		Use:   "usage",
		Short: "Show broker-side budget counters for a grant",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			u, err := brokerAdminClient(adminSocket).Usage(ctx(), usageID)
			if err != nil {
				fail(err)
			}
			out(u, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "GRANT\t%s\nREQUESTS DAY\t%d\nREQUESTS MONTH\t%d\nTOKENS DAY\t%d\nTOKENS MONTH\t%d\nCOST DAY USD\t%.4f\nCOST MONTH USD\t%.4f\n",
					u.GrantID, u.RequestsDay, u.RequestsMonth, u.TokensDay, u.TokensMonth, u.CostDayUSD, u.CostMonthUSD)
			})
		},
	}
	usage.Flags().StringVar(&usageID, "grant", "", "grant ID")
	_ = usage.MarkFlagRequired("grant")

	cmd.AddCommand(grant, list, revoke, usage)
	addAdminSocketFlag(cmd, &adminSocket)
	return cmd
}

func brokerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "broker", Short: "Inspect the credential broker"}
	var adminSocket string

	status := &cobra.Command{
		Use:   "status",
		Short: "Show broker status (never secrets)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			s, err := brokerAdminClient(adminSocket).Status(ctx())
			if err != nil {
				fail(err)
			}
			out(s, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "STATUS\t%s\nSECRETS\t%d\nGRANTS\t%d\nSESSIONS\t%d\nPROVIDERS\t%s\nEGRESS\t%s\n",
					s.Status, s.Secrets, s.Grants, s.Sessions, strings.Join(s.Providers, ","), s.Egress)
			})
		},
	}
	health := &cobra.Command{
		Use:   "health",
		Short: "Check broker health",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			s, err := brokerAdminClient(adminSocket).Status(ctx())
			if err != nil {
				fail(err)
			}
			if s.Status != "ok" {
				fail(fmt.Errorf("broker status: %s", s.Status))
			}
			fmt.Println("broker: ok")
		},
	}

	var (
		sessionAgent string
		sessionGrant string
		sessionTTL   string
	)
	create := &cobra.Command{
		Use:   "create AGENT CREDENTIAL",
		Short: "Mint a short-lived broker session token (printed once)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			r, err := brokerAdminClient(adminSocket).MintSession(ctx(), args[0], sessionGrant, args[1], sessionTTL)
			if err != nil {
				fail(err)
			}
			fmt.Printf("session %s expires %s\n", r.Session.ID, r.Session.ExpiresAt.Format(time.RFC3339))
			fmt.Printf("%s\n", r.Token)
		},
	}
	create.Flags().StringVar(&sessionGrant, "grant", "", "grant ID (default: match agent+credential)")
	create.Flags().StringVar(&sessionTTL, "ttl", "1h", "session time to live (max 24h)")

	list := &cobra.Command{
		Use:   "list",
		Short: "List broker sessions (no tokens)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			list, err := brokerAdminClient(adminSocket).ListSessions(ctx(), sessionAgent)
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "ID\tAGENT\tGRANT\tEXPIRES\tREVOKED")
				for _, s := range list {
					rev := "-"
					if s.RevokedAt != nil {
						rev = s.RevokedAt.Format(time.RFC3339)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Agent, s.GrantID,
						s.ExpiresAt.Format(time.RFC3339), rev)
				}
			})
		},
	}
	list.Flags().StringVar(&sessionAgent, "agent", "", "filter by agent")

	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke a broker session immediately",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := brokerAdminClient(adminSocket).RevokeSession(ctx(), args[0]); err != nil {
				fail(err)
			}
			fmt.Println("revoked")
		},
	}

	requests := &cobra.Command{
		Use:   "requests",
		Short: "Show recent broker requests (secret-free)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			list, err := brokerAdminClient(adminSocket).Requests(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "TIME\tAGENT\tPROVIDER\tSTATUS\tTOKENS\tPATH")
				for _, e := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d+%d\t%s\n",
						e.Time.Format(time.RFC3339), e.Agent, e.Provider, e.Status,
						e.PromptTokens, e.CompletionTokens, e.Path)
				}
			})
		},
	}

	session := &cobra.Command{Use: "session", Short: "Manage broker sessions"}
	session.AddCommand(create, list, revoke)

	cmd.AddCommand(status, health, session, requests)
	addAdminSocketFlag(cmd, &adminSocket)
	return cmd
}

func egressCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "egress", Short: "Inspect agent egress isolation"}
	var adminSocket string
	status := &cobra.Command{
		Use:   "status [AGENT]",
		Short: "Report whether egress enforcement is active (fails closed if not)",
		Run: func(cmd *cobra.Command, args []string) {
			s, err := brokerAdminClient(adminSocket).Status(ctx())
			if err != nil {
				fail(err)
			}
			fmt.Printf("egress: %s\n", s.Egress)
			if strings.Contains(strings.ToLower(s.Egress), "not enforced") {
				fail(errors.New("egress isolation is NOT enforced by this build; agents with network access can bypass the broker"))
			}
		},
	}
	cmd.AddCommand(status)
	addAdminSocketFlag(cmd, &adminSocket)
	return cmd
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
