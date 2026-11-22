package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// The signed-in human reaches the daemon only through a configured gateway
// peer (ADR 0003 D-6); from any other peer the header changes nothing.
func TestForwardedHumanOnlyFromGatewayPeer(t *testing.T) {
	f := ticketFixture(t)
	h := PeerAuthMiddleware(NewAuthPolicy([]uint32{1000, 1001}, []uint32{2000}).WithGateways([]uint32{1001, 2000}), f.srv.mux)
	alice, _ := json.Marshal(model.HumanActor{ID: "https://idp/realms/dw|alice-sub", Name: "alice", Groups: []string{"dw-author"}})
	create := func(uid uint32, header string) (int, ticketing.Ticket) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets", strings.NewReader(`{"title":"x"}`))
		req = req.WithContext(ContextWithPeer(req.Context(), "peer", uid, ""))
		if header != "" {
			req.Header.Set(model.ActorHeader, header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var tk ticketing.Ticket
		_ = json.Unmarshal(rec.Body.Bytes(), &tk)
		return rec.Code, tk
	}
	// Gateway peer: the human is the author.
	if code, tk := create(1001, string(alice)); code != 201 || tk.CreatedBy == nil || tk.CreatedBy.ID != "https://idp/realms/dw|alice-sub" || tk.CreatedBy.Name != "alice" {
		t.Fatalf("gateway: %d %+v", code, tk.CreatedBy)
	}
	// Another admin peer (e.g. the CLI) cannot claim to be alice.
	if code, tk := create(1000, string(alice)); code != 201 || tk.CreatedBy == nil || tk.CreatedBy.ID != "peer:peer" {
		t.Fatalf("non-gateway peer: %d %+v", code, tk.CreatedBy)
	}
	// A gateway uid without the admin role gets no forwarded identity and,
	// being monitor, cannot write anyway.
	if code, _ := create(2000, string(alice)); code != http.StatusForbidden {
		t.Fatalf("monitor gateway uid: %d", code)
	}
	// Malformed header from the gateway is a 400, not a silent fallback.
	for _, bad := range []string{"{", `{"id":""}`, `{"id":"x","groups":[""]}`} {
		if code, _ := create(1001, bad); code != http.StatusBadRequest {
			t.Fatalf("bad header %q: %d", bad, code)
		}
	}
}
