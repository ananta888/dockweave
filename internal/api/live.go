package api

import (
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// liveRegistry tracks work in flight (endpoint jobs, gate runs). The zero
// value is ready to use; entries are removed when the request finishes, so a
// crashed request handler cannot leave a stale entry behind.
type liveRegistry struct {
	mu    sync.Mutex
	seq   uint64
	items map[uint64]model.LiveWork
}

// begin registers w and returns the function that removes it again.
func (l *liveRegistry) begin(w model.LiveWork) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[uint64]model.LiveWork{}
	}
	l.seq++
	id := l.seq
	w.ID = strconv.FormatUint(id, 10)
	if w.StartedAt.IsZero() {
		w.StartedAt = time.Now().UTC()
	}
	l.items[id] = w
	return func() {
		l.mu.Lock()
		delete(l.items, id)
		l.mu.Unlock()
	}
}

func (l *liveRegistry) snapshot() []model.LiveWork {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.LiveWork, 0, len(l.items))
	for _, w := range l.items {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// liveWork lists the work in flight (read-only; admin and monitor).
func (s *Server) liveWork(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.live.snapshot(),
		"now":   time.Now().UTC(),
	})
}
