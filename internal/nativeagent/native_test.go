package nativeagent

import "testing"

func TestLastTaskComplete(t *testing.T) {
	out := `{"type":"event_msg","payload":{"type":"token_count"}}
{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"CODE"}}
{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"ANSWER"}}`
	if got, errMsg, ok := lastTaskComplete(out); !ok || got != "ANSWER" || errMsg != "" {
		t.Fatalf("got %q err=%q ok=%v", got, errMsg, ok)
	}
}

func TestClaudeAssistantText(t *testing.T) {
	out := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"hmm"},{"type":"text","text":"Hallo"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Welt"}]}}`
	if got := claudeAssistantText(out); got != "Hallo\nWelt" {
		t.Fatalf("got %q", got)
	}
}

func TestThreadIDFromPath(t *testing.T) {
	m := threadIDRe.FindStringSubmatch("/x/rollout-2026-11-13T12-47-43-01a1d4f3-1599-7f23-bf37-5f1be3961c54.jsonl")
	if len(m) != 2 || m[1] != "01a1d4f3-1599-7f23-bf37-5f1be3961c54" {
		t.Fatalf("match = %v", m)
	}
}
