package main

// secret migrate moves credentials from an explicit env file into the
// broker vault and optionally switches the agent off that file. It never
// scans directories implicitly, never prints values, and never deletes
// the original file.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ananta888/dockweave/internal/credentialbroker"
)

type envCandidate struct {
	Key      string
	Provider string
	Name     string
	Length   int
}

var envProviderMap = map[string]string{
	"OPENROUTER_API_KEY": "openrouter",
	"OPENAI_API_KEY":     "openai",
	"ANTHROPIC_API_KEY":  "anthropic",
	"DEEPSEEK_API_KEY":   "deepseek",
	"GEMINI_API_KEY":     "gemini",
	"GOOGLE_API_KEY":     "gemini",
}

func planEnvMigration(content, agent, prefix string) ([]envCandidate, []string) {
	if prefix == "" {
		prefix = agent
	}
	var out []envCandidate
	var skipped []string
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		provider, known := envProviderMap[key]
		if !known || value == "" {
			skipped = append(skipped, key)
			continue
		}
		name := prefix + "-" + strings.ReplaceAll(strings.ToLower(key), "_", "-")
		out = append(out, envCandidate{Key: key, Provider: provider, Name: name, Length: len(value)})
	}
	return out, skipped
}

func secretMigrateCmd(adminSocket *string) *cobra.Command {
	var (
		agent       string
		envFile     string
		prefix      string
		dryRun      bool
		yes         bool
		grant       bool
		models      string
		disableFile bool
	)
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Move secrets from an explicit env file into the vault",
		Long: "Reads only the explicitly given env file. Detected provider keys are\n" +
			"stored in the broker vault; values are never printed. With --grant the\n" +
			"agent is granted the new credentials. --disable-env-file clears the\n" +
			"agent's env-file reference and renames the file only after all stores\n" +
			"succeeded.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if agent == "" || envFile == "" {
				fail(errors.New("--agent and --env-file are required"))
			}
			b, err := os.ReadFile(envFile)
			if err != nil {
				fail(fmt.Errorf("read env file: %w", err))
			}
			cands, skipped := planEnvMigration(string(b), agent, prefix)
			if len(cands) == 0 {
				fail(errors.New("no known provider keys found in the env file"))
			}
			out(cands, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "KEY\tPROVIDER\tVAULT NAME\tVALUE")
				for _, e := range cands {
					fmt.Fprintf(w, "%s\t%s\t%s\t*** (%d chars)\n", e.Key, e.Provider, e.Name, e.Length)
				}
				for _, k := range skipped {
					fmt.Fprintf(w, "SKIPPED\t-\t%s\tnot a known provider key\n", k)
				}
			})
			if dryRun {
				fmt.Println("dry run: nothing stored")
				return
			}
			if !yes {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					fail(errors.New("refusing to migrate non-interactively without --yes"))
				}
				fmt.Fprintf(os.Stderr, "Store %d secret(s) in the vault? [y/N] ", len(cands))
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.ToLower(strings.TrimSpace(line)) != "y" {
					fmt.Println("aborted")
					return
				}
			}
			if grant && strings.TrimSpace(models) == "" {
				fail(errors.New("--grant requires --models (use '*' to allow any model explicitly)"))
			}
			bc := brokerAdminClient(*adminSocket)
			for _, e := range cands {
				// Value is read from the already-parsed candidate; it never
				// touches argv, logs or the terminal.
				value := envValue(string(b), e.Key)
				if err := bc.PutSecret(ctx(), e.Name, e.Provider, value); err != nil {
					fail(fmt.Errorf("store %s: %w", e.Name, err))
				}
				if grant {
					_, err := bc.AddGrant(ctx(), credentialbroker.Grant{
						Agent: agent, Credential: e.Name, Models: splitCSV(models),
					})
					if err != nil {
						fail(fmt.Errorf("grant %s: %w", e.Name, err))
					}
				}
				fmt.Printf("stored %s (%s)\n", e.Name, e.Provider)
			}
			if disableFile {
				if _, err := client().SetEnvFile(ctx(), agent, ""); err != nil {
					fail(fmt.Errorf("clear agent env file: %w (secrets are stored; file kept)", err))
				}
				backup := fmt.Sprintf("%s.migrated-%s", envFile, time.Now().UTC().Format("20060102T150405Z"))
				if err := os.Rename(envFile, backup); err != nil {
					fail(fmt.Errorf("rename env file: %w (agent no longer uses it)", err))
				}
				fmt.Printf("env file disabled and moved to %s\n", backup)
			}
			fmt.Println("next: start the broker, set the agent's provider base URL to the broker and restart the agent")
		},
	}
	f := c.Flags()
	f.StringVar(&agent, "agent", "", "agent name or ID")
	f.StringVar(&envFile, "env-file", "", "explicit env file to migrate")
	f.StringVar(&prefix, "prefix", "", "credential name prefix (default: agent name)")
	f.BoolVar(&dryRun, "dry-run", false, "show candidates without storing")
	f.BoolVar(&yes, "yes", false, "do not prompt (required when stdin is not a terminal)")
	f.BoolVar(&grant, "grant", false, "create credential grants for the agent")
	f.StringVar(&models, "models", "", "model allowlist for --grant (comma-separated, '*' = any)")
	f.BoolVar(&disableFile, "disable-env-file", false, "clear the agent env file and rename the file after success")
	return c
}

func envValue(content, key string) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
