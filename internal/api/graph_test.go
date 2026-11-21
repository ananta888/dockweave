package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// RF-006: the daemon PUT /api/v1/graph accepts exactly one JSON object with an
// optional nonnegative integer revision; anything else is a 400 that leaves
// the stored document untouched.
func TestPutGraphValidatesInputAndKeepsStoredDocument(t *testing.T) {
	f := newPipelineFixture(t)
	stored := `{"revision":3,"connectors":[{"id":"c1"}],"big":12345678901234567890}`
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/graph", stored); rec.Code != http.StatusOK {
		t.Fatalf("valid graph: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"null":             `null`,
		"array":            `[]`,
		"string":           `"x"`,
		"number":           `7`,
		"true":             `true`,
		"false":            `false`,
		"empty":            ``,
		"truncated":        `{`,
		"two documents":    `{} {}`,
		"trailing garbage": `{}x`,
		"null revision":    `{"revision":null}`,
		"fraction":         `{"revision":1.5}`,
		"negative":         `{"revision":-1}`,
		"string revision":  `{"revision":"1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/graph", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d %s", rec.Code, rec.Body)
			}
			var e struct {
				Error struct{ Code string } `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error.Code != "invalid" {
				t.Fatalf("want structured invalid error, got %s", rec.Body)
			}
			var doc graphDocument
			get := f.do(t, RoleAdmin, http.MethodGet, "/api/v1/graph", "")
			if err := json.Unmarshal(get.Body.Bytes(), &doc); err != nil || !doc.Saved {
				t.Fatalf("stored document lost: %v %s", err, get.Body)
			}
			if string(doc.Body) != stored {
				t.Fatalf("stored document changed:\n got %s\nwant %s", doc.Body, stored)
			}
		})
	}
}

func TestPutGraphOnFreshStoreRejectsNullWithoutPanic(t *testing.T) {
	f := newPipelineFixture(t)
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/graph", `null`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	var doc graphDocument
	_ = json.Unmarshal(f.do(t, RoleAdmin, http.MethodGet, "/api/v1/graph", "").Body.Bytes(), &doc)
	if doc.Saved {
		t.Fatalf("nothing may be stored: %s", doc.Body)
	}
}
