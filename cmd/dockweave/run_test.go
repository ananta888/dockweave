package main

import (
	"testing"

	"github.com/ananta888/dockweave/internal/model"
)

func TestProjectSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dockweave", "dockweave"},
		{"My Project", "my-project"},
		{"  weird__name.v2  ", "weird__name.v2"},
		{"---", "project"},
		{"", "project"},
		{"Ünïcode", "n-code"},
		{"a b c", "a-b-c"},
	}
	for _, tc := range cases {
		if got := projectSlug(tc.in); got != tc.want {
			t.Errorf("projectSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := projectSlug("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if len(long) > 63 {
		t.Errorf("slug longer than 63 chars: %d", len(long))
	}
}

func TestPlanProjectWorkspaceReusesSamePath(t *testing.T) {
	list := []*model.Workspace{
		{Name: "other", HostPath: "/srv/other"},
		{Name: "dockweave", HostPath: "/home/k/dockweave"},
	}
	ws, name := planProjectWorkspace(list, "/home/k/dockweave", "")
	if ws == nil || ws.Name != "dockweave" || name != "" {
		t.Fatalf("expected existing workspace, got ws=%v name=%q", ws, name)
	}
}

func TestPlanProjectWorkspaceAvoidsNameCollision(t *testing.T) {
	list := []*model.Workspace{
		{Name: "dockweave", HostPath: "/srv/elsewhere"},
	}
	ws, name := planProjectWorkspace(list, "/home/k/dockweave", "")
	if ws != nil {
		t.Fatalf("expected no existing workspace, got %v", ws)
	}
	if name != "dockweave-2" {
		t.Fatalf("name = %q, want dockweave-2", name)
	}

	_, name = planProjectWorkspace(list, "/home/k/dockweave", "custom")
	if name != "custom" {
		t.Fatalf("explicit name = %q, want custom", name)
	}
}
