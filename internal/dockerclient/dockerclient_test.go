package dockerclient

import (
	"reflect"
	"testing"
)

func TestContainerEnvDefaultsHomeToMemory(t *testing.T) {
	// Without HOME: memory is the agent home (tool state persists there).
	got := containerEnv([]string{"FOO=bar"})
	want := []string{"HOME=/memory", "FOO=bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// No agent env at all.
	got = containerEnv(nil)
	want = []string{"HOME=/memory"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestContainerEnvKeepsExplicitHome(t *testing.T) {
	in := []string{"HOME=/custom", "FOO=bar"}
	got := containerEnv(in)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("explicit HOME must win: got %v", got)
	}
}
