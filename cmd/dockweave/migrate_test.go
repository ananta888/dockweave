package main

import "testing"

func TestPlanEnvMigration(t *testing.T) {
	content := `# comment
OPENROUTER_API_KEY=sk-or-123
OPENAI_API_KEY="sk-openai"
GITHUB_PAT=ghp_should_be_skipped
ANTHROPIC_API_KEY=
EMPTY=value
`
	cands, skipped := planEnvMigration(content, "dev", "")
	if len(cands) != 2 {
		t.Fatalf("candidates = %+v, want 2", cands)
	}
	if cands[0].Provider != "openrouter" || cands[0].Name != "dev-openrouter-api-key" {
		t.Fatalf("unexpected first candidate: %+v", cands[0])
	}
	if cands[1].Provider != "openai" || cands[1].Name != "dev-openai-api-key" {
		t.Fatalf("unexpected second candidate: %+v", cands[1])
	}
	for _, c := range cands {
		if c.Length == 0 {
			t.Fatalf("candidate %q has zero length", c.Key)
		}
	}
	// Unknown and empty keys are reported as skipped.
	foundGithub, foundEmpty := false, false
	for _, k := range skipped {
		if k == "GITHUB_PAT" {
			foundGithub = true
		}
		if k == "EMPTY" {
			foundEmpty = true
		}
	}
	if !foundGithub || !foundEmpty {
		t.Fatalf("skipped = %v, want GITHUB_PAT and EMPTY", skipped)
	}
}

func TestPlanEnvMigrationCustomPrefix(t *testing.T) {
	cands, _ := planEnvMigration("DEEPSEEK_API_KEY=abc", "dev", "shared")
	if len(cands) != 1 || cands[0].Name != "shared-deepseek-api-key" {
		t.Fatalf("candidates = %+v", cands)
	}
}

func TestEnvValueStripsQuotes(t *testing.T) {
	content := "A=one\nB='two'\nC=\"three\"\n"
	for k, want := range map[string]string{"A": "one", "B": "two", "C": "three"} {
		if got := envValue(content, k); got != want {
			t.Errorf("envValue(%q) = %q, want %q", k, got, want)
		}
	}
}
