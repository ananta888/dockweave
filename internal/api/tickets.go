package api

import (
	"context"
	"net/http"
	"time"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// providerView is the public status of one ticket provider.
type providerView struct {
	Name         string                 `json:"name"`
	Health       ticketing.Health       `json:"health"`
	Capabilities ticketing.Capabilities `json:"capabilities"`
}

func viewOf(ctx context.Context, p ticketing.Provider) *providerView {
	if p == nil {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return &providerView{Name: p.Name(), Health: p.Health(hctx), Capabilities: p.Capabilities()}
}

// ticketProvider reports the configured ticket providers (ADR 0003 D-3).
// Ticketing is opt-in: without TICKET_PROVIDER it says so instead of
// picking a provider silently.
func (s *Server) ticketProvider(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"configured": s.Tickets != nil, "available": ticketing.Registered()}
	if s.TicketsError != "" {
		out["error"] = s.TicketsError
	}
	if s.Tickets != nil {
		out["primary"] = viewOf(r.Context(), s.Tickets.Primary)
		if s.Tickets.Mirror != nil {
			out["mirror"] = viewOf(r.Context(), s.Tickets.Mirror)
		}
	} else if s.TicketsError == "" {
		out["error"] = "ticketing is not configured: set TICKET_PROVIDER=native|paperclip in dockweaved.conf"
	}
	writeJSON(w, http.StatusOK, out)
}
