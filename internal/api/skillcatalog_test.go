package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/skillcatalog"
)

func TestSkillCatalogHandler(t *testing.T) {
	c, err := skillcatalog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Skills: c}
	rec := httptest.NewRecorder()
	s.skillCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/v1/skills/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "keycloak-oidc-human-guide") {
		t.Fatalf("catalog missing bundled skill: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "anthropics/skills") {
		t.Fatal("catalog missing Claude store")
	}
}

func TestSkillCatalogUnavailable(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.skillCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/v1/skills/catalog", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
