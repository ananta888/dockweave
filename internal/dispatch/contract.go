package dispatch

import (
	"regexp"
	"strings"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// statusContract is appended to every worker prompt (wrapper parity).
const statusContract = `

Abschluss-Protokoll: Pruefe deine Arbeit (Build/Tests, falls vorhanden) und beende
deine Antwort mit genau einer Statuszeile:
  STATUS: DONE        - Aufgabe vollstaendig umgesetzt und geprueft
  STATUS: BLOCKED     - du brauchst eine Entscheidung oder fehlende Rechte
  STATUS: IN_PROGRESS - Teilarbeit erledigt, Rest offen
Ohne diese Zeile gilt die Aufgabe als IN_PROGRESS.`

// reviewContract is appended for the reviewer.
const reviewContract = `

Review-Vertrag: Pruefe die Arbeit der anderen Rollen (Build, Tests, Code).
Beende deine Antwort zusaetzlich mit:
  VERDICT: PASS   - freigegeben
  VERDICT: FAIL   - nicht freigegeben
  REASON: <ein Satz Begruendung>
Bei FAIL wird automatisch ein Rework-Issue beim Autor angelegt.`

// BuildPrompt renders the ticket as the agent prompt with the contracts.
func BuildPrompt(t *ticketing.Ticket, reviewer bool) string {
	head := t.Title
	if t.Identifier != "" {
		head = t.Identifier + ": " + t.Title
	}
	p := head
	if strings.TrimSpace(t.Description) != "" {
		p += "\n\n" + t.Description
	}
	p += statusContract
	if reviewer {
		p += reviewContract
	}
	return p
}

var (
	statusRe  = regexp.MustCompile(`STATUS:\s*(DONE|BLOCKED|IN_PROGRESS|IN PROGRESS)`)
	verdictRe = regexp.MustCompile(`VERDICT:\s*(PASS|FAIL)`)
	reasonRe  = regexp.MustCompile(`(?i)REASON:\s*(.+)`)
	dependsRe = regexp.MustCompile(`DEPENDS_ON:\s*([A-Za-z0-9_,-]+)`)
)

// ParseStatus applies the STATUS contract: the last status line wins; none
// means in progress (partial work is never marked done).
func ParseStatus(content string) ticketing.Status {
	hits := statusRe.FindAllStringSubmatch(strings.ToUpper(content), -1)
	if len(hits) == 0 {
		return ticketing.StatusInProgress
	}
	switch hits[len(hits)-1][1] {
	case "DONE":
		return ticketing.StatusDone
	case "BLOCKED":
		return ticketing.StatusBlocked
	}
	return ticketing.StatusInProgress
}

// ParseVerdict returns PASS, FAIL or "".
func ParseVerdict(content string) string {
	hits := verdictRe.FindAllStringSubmatch(strings.ToUpper(content), -1)
	if len(hits) == 0 {
		return ""
	}
	return hits[len(hits)-1][1]
}

// ParseReason returns the REASON line or a default.
func ParseReason(content string) string {
	if m := reasonRe.FindStringSubmatch(content); m != nil {
		return strings.TrimSpace(m[1])
	}
	return "Review FAIL"
}

// dependsOn reads "DEPENDS_ON: a,b" from a description (Lead plans).
func dependsOn(description string) []string {
	m := dependsRe.FindStringSubmatch(description)
	if m == nil {
		return nil
	}
	var out []string
	for _, d := range strings.Split(m[1], ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}
