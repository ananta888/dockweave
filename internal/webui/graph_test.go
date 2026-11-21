package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func putGraphReq(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.putGraph(rec, req)
	return rec
}

func TestPutGraphRevisionCheck(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := &Server{}

	if rec := putGraphReq(s, `{"nodes":[],"revision":0}`); rec.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", rec.Code, rec.Body)
	}
	if rec := putGraphReq(s, `{"nodes":[],"revision":0}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d, want 409", rec.Code)
	}
	if rec := putGraphReq(s, `{"nodes":[],"revision":7}`); rec.Code != http.StatusConflict {
		t.Fatalf("future revision: %d, want 409", rec.Code)
	}
	rec := putGraphReq(s, `{"nodes":[],"revision":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("current save: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Revision != 2 {
		t.Fatalf("revision = %d, want 2", out.Revision)
	}
}

func TestPutGraphLegacyFileWithoutRevision(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, _ := graphPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"nodes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	if rec := putGraphReq(s, `{"nodes":[],"revision":3}`); rec.Code != http.StatusConflict {
		t.Fatalf("revision mismatch on legacy file: %d, want 409", rec.Code)
	}
	if rec := putGraphReq(s, `{"nodes":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("legacy save at revision 0: %d %s", rec.Code, rec.Body)
	}
}

func TestPutGraphConcurrentSavesOneWins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := &Server{}
	if rec := putGraphReq(s, `{"revision":0}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	const n = 16
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = putGraphReq(s, `{"revision":1}`).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		} else if c != http.StatusConflict {
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent saves succeeded, want exactly 1", ok)
	}
}

func TestStampRevisionPreservesValues(t *testing.T) {
	out, err := stampRevision(json.RawMessage(`{"id":9007199254740993,"x":1.10,"revision":4}`), 5)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"id":9007199254740993`, `"x":1.10`, `"revision":5`} {
		if !strings.Contains(s, want) {
			t.Fatalf("stamped = %s, missing %s", s, want)
		}
	}
}
