package model

import "testing"

func TestValidRuntime(t *testing.T) {
	cases := map[RuntimeType]bool{
		"":           true,
		"docker":     true,
		"herdr":      true,
		"paperclip":  true,
		"k8s":        false,
		"DOCKER":     false,
		"kubernetes": false,
	}
	for rt, want := range cases {
		if got := ValidRuntime(rt); got != want {
			t.Errorf("ValidRuntime(%q) = %v, want %v", rt, got, want)
		}
	}
}

func TestValidateRuntime(t *testing.T) {
	base := func() *Agent {
		return &Agent{Name: "a", Image: "img", NetworkMode: "bridge"}
	}
	for _, rt := range []string{"", "docker", "herdr", "paperclip"} {
		a := base()
		a.Runtime = RuntimeType(rt)
		if err := a.Validate(); err != nil {
			t.Errorf("runtime %q should validate, got error: %v", rt, err)
		}
	}
	a := base()
	a.Runtime = "k8s"
	if err := a.Validate(); err == nil {
		t.Error("unsupported runtime must be rejected by Validate")
	}
}
