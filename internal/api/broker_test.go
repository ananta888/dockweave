package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrokerMetadataUnavailableWithoutClient(t *testing.T) {
	s := &Server{}
	for _, fn := range []http.HandlerFunc{s.brokerStatus, s.brokerGrants, s.brokerSessions, s.brokerRequests, s.brokerUsage} {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	}
}
