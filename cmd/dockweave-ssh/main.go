// dockweave-ssh operates the DockWeave SSH signature broker: it generates the
// SSH CA, manages DENY-by-default grants and issues short-lived,
// destination-bound user certificates. The CA private key never leaves the
// operator; agents only ever receive a short-lived certificate + ephemeral key.
//
// Phase-2 scope of docs/ssh-broker-design.md. The agent-side wrapper and
// FIDO2 are later phases.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ananta888/dockweave/internal/sshbroker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dockweave-ssh: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: dockweave-ssh <init|grant|issue|list> [flags]")
	}
	cmd, args := os.Args[1], os.Args[2:]

	caPath := flag.String("ca", "ssh-ca.pem", "path to the SSH CA private key")
	policyPath := flag.String("policy", "ssh-policy.json", "path to the policy file")

	switch cmd {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		ca := fs.String("ca", *caPath, "CA private key output path")
		policy := fs.String("policy", *policyPath, "policy output path")
		_ = fs.Parse(args)
		return initCmd(*ca, *policy)

	case "grant":
		fs := flag.NewFlagSet("grant", flag.ExitOnError)
		policy := fs.String("policy", *policyPath, "policy file")
		agent := fs.String("agent", "", "agent identity")
		host := fs.String("host", "", "destination host")
		user := fs.String("user", "", "destination user")
		ttl := fs.String("ttl", "", "certificate TTL (e.g. 15m)")
		_ = fs.Parse(args)
		if *agent == "" || *host == "" || *user == "" {
			return fmt.Errorf("--agent, --host and --user are required")
		}
		return grantCmd(*policy, sshbroker.Grant{Agent: *agent, Host: *host, User: *user, TTL: *ttl})

	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		policy := fs.String("policy", *policyPath, "policy file")
		agent := fs.String("agent", "", "filter by agent")
		_ = fs.Parse(args)
		return listCmd(*policy, *agent)

	case "issue":
		fs := flag.NewFlagSet("issue", flag.ExitOnError)
		ca := fs.String("ca", *caPath, "CA private key")
		policy := fs.String("policy", *policyPath, "policy file")
		agent := fs.String("agent", "", "agent identity")
		host := fs.String("host", "", "destination host")
		user := fs.String("user", "", "destination user")
		ttl := fs.Duration("ttl", 0, "certificate TTL override")
		out := fs.String("out", "", "output directory for cert.pub and key (default: stdout)")
		_ = fs.Parse(args)
		if *agent == "" || *user == "" {
			return fmt.Errorf("--agent and --user are required")
		}
		return issueCmd(*ca, *policy, *agent, *host, *user, *ttl, *out)

	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		ca := fs.String("ca", *caPath, "CA private key")
		policy := fs.String("policy", *policyPath, "policy file")
		agent := fs.String("agent", "", "agent identity")
		host := fs.String("host", "", "destination host (required)")
		user := fs.String("user", "", "destination user (required)")
		ttl := fs.Duration("ttl", 0, "certificate TTL override")
		_ = fs.Parse(args)
		if *agent == "" || *host == "" || *user == "" {
			return fmt.Errorf("--agent, --host and --user are required")
		}
		return runCmd(*ca, *policy, *agent, *host, *user, *ttl, fs.Args())

	case "git-ssh":
		// Drop-in GIT_SSH_COMMAND target: git appends "<host> <command>".
		// Usage: GIT_SSH_COMMAND="dockweave-ssh git-ssh --ca C --policy P --agent A --user git"
		fs := flag.NewFlagSet("git-ssh", flag.ExitOnError)
		ca := fs.String("ca", *caPath, "CA private key")
		policy := fs.String("policy", *policyPath, "policy file")
		agent := fs.String("agent", "", "agent identity")
		user := fs.String("user", "", "destination user (required)")
		ttl := fs.Duration("ttl", 0, "certificate TTL override")
		_ = fs.Parse(args)
		rest := fs.Args()
		if *agent == "" || *user == "" {
			return fmt.Errorf("--agent and --user are required")
		}
		if len(rest) < 1 {
			return fmt.Errorf("git-ssh: destination host missing (set GIT_SSH_COMMAND correctly)")
		}
		return runCmd(*ca, *policy, *agent, rest[0], *user, *ttl, rest[1:])

	default:
		return fmt.Errorf("unknown command %q (init|grant|issue|list|run|git-ssh)", cmd)
	}
}

func initCmd(caPath, policyPath string) error {
	ca, err := sshbroker.GenerateCA()
	if err != nil {
		return err
	}
	pemBytes, err := ca.MarshalPrivateKey()
	if err != nil {
		return err
	}
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		return fmt.Errorf("write ca: %w", err)
	}
	if err := sshbroker.SavePolicy(policyPath, sshbroker.NewPolicy()); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	fmt.Printf("wrote CA %s and empty policy %s\n", caPath, policyPath)
	return nil
}

func grantCmd(policyPath string, g sshbroker.Grant) error {
	p, err := sshbroker.LoadPolicy(policyPath)
	if err != nil {
		return err
	}
	if err := p.Add(g); err != nil {
		return err
	}
	if err := sshbroker.SavePolicy(policyPath, p); err != nil {
		return err
	}
	fmt.Printf("granted %s -> %s@%s\n", g.Agent, g.User, g.Host)
	return nil
}

func listCmd(policyPath, agent string) error {
	p, err := sshbroker.LoadPolicy(policyPath)
	if err != nil {
		return err
	}
	for _, g := range p.List(agent) {
		fmt.Printf("%s\t%s@%s\tttl=%s\n", g.Agent, g.User, g.Host, g.TTL)
	}
	return nil
}

func issueCmd(caPath, policyPath, agent, host, user string, ttl time.Duration, out string) error {
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	ca, err := sshbroker.LoadCA(caBytes)
	if err != nil {
		return err
	}
	p, err := sshbroker.LoadPolicy(policyPath)
	if err != nil {
		return err
	}
	issued, err := sshbroker.New(ca, p).Issue(agent, host, user, ttl)
	if err != nil {
		return err
	}
	if out == "" {
		fmt.Printf("cert:\n%s", issued.CertPEM)
		fmt.Printf("key:\n%s", issued.PrivateKeyPEM)
		return nil
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "cert.pub"), issued.CertPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "key"), issued.PrivateKeyPEM, 0o600); err != nil {
		return err
	}
	fmt.Printf("issued cert.pub + key in %s (valid until %s)\n", out, time.Unix(int64(issued.Cert.ValidBefore), 0).Format(time.RFC3339))
	return nil
}

// runCmd is the agent-side wrapper: it issues a short-lived certificate for a
// destination, writes the ephemeral key/cert to a 0700 temp dir, and execs ssh
// with them. The temp dir is removed on exit. Host key verification stays
// enabled (StrictHostKeyChecking is never disabled).
func runCmd(caPath, policyPath, agent, host, user string, ttl time.Duration, sshArgs []string) error {
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	ca, err := sshbroker.LoadCA(caBytes)
	if err != nil {
		return err
	}
	p, err := sshbroker.LoadPolicy(policyPath)
	if err != nil {
		return err
	}
	issued, err := sshbroker.New(ca, p).Issue(agent, host, user, ttl)
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "dockweave-ssh-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(dir, "key")
	certPath := filepath.Join(dir, "cert.pub")
	if err := os.WriteFile(keyPath, issued.PrivateKeyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, issued.CertPEM, 0o600); err != nil {
		return err
	}

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	cmdArgs := []string{
		"-i", keyPath,
		"-o", "CertificateFile=" + certPath,
		"-l", user,
		host,
	}
	cmdArgs = append(cmdArgs, sshArgs...)
	cmd := exec.Command(sshBin, cmdArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
