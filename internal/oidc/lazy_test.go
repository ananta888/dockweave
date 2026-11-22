package oidc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type readyProvider struct{}

func (readyProvider) AuthURL(state, _, _ string) (string, error) {
	return "http://idp/auth?state=" + state, nil
}
func (readyProvider) Exchange(context.Context, string, string, string) (*Principal, error) {
	return &Principal{Subject: "u"}, nil
}
func (readyProvider) EndSessionURL(string) string { return "http://idp/logout" }

// An IdP that starts after the gateway is picked up on a later attempt,
// without restarting the gateway.
func TestLazyProviderRecoversWhenIdPComesUp(t *testing.T) {
	var up atomic.Bool
	var calls atomic.Int32
	l := NewLazyProvider(Config{Issuer: "http://idp"}, func(context.Context, Config) (Provider, error) {
		calls.Add(1)
		if !up.Load() {
			return nil, errors.New("connection refused")
		}
		return readyProvider{}, nil
	})
	clock := time.Unix(1000, 0)
	l.now = func() time.Time { return clock }

	if _, err := l.AuthURL("s", "n", "c"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("down IdP: err=%v", err)
	}
	if got := l.EndSessionURL("x"); got != "/" {
		t.Fatalf("EndSessionURL without discovery = %q", got)
	}
	// Within the retry interval no new discovery is attempted.
	up.Store(true)
	if _, err := l.AuthURL("s", "n", "c"); !errors.Is(err, ErrProviderUnavailable) || calls.Load() != 1 {
		t.Fatalf("rate limit: err=%v calls=%d", err, calls.Load())
	}
	clock = clock.Add(6 * time.Second)
	u, err := l.AuthURL("s", "n", "c")
	if err != nil || u == "" || !l.Ready() || calls.Load() != 2 {
		t.Fatalf("recovery: url=%q err=%v ready=%v calls=%d", u, err, l.Ready(), calls.Load())
	}
	// Once discovered, the provider is cached.
	_, _ = l.AuthURL("s", "n", "c")
	if calls.Load() != 2 || l.EndSessionURL("x") != "http://idp/logout" {
		t.Fatalf("cache: calls=%d", calls.Load())
	}
}

func TestLazyProviderSharesConcurrentAttempts(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	l := NewLazyProvider(Config{}, func(context.Context, Config) (Provider, error) {
		calls.Add(1)
		<-release
		return readyProvider{}, nil
	})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := l.Ensure(context.Background())
			errs <- err
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("discovery attempts = %d, want 1", calls.Load())
	}
}

func TestLazyProviderWarmRetriesUntilReady(t *testing.T) {
	var calls atomic.Int32
	l := NewLazyProvider(Config{}, func(context.Context, Config) (Provider, error) {
		if calls.Add(1) < 3 {
			return nil, errors.New("not yet")
		}
		return readyProvider{}, nil
	})
	l.minRetry = time.Millisecond
	ready := make(chan struct{})
	fails := 0
	go l.Warm(context.Background(), 5*time.Millisecond, func(error) { fails++ }, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("warm-up never became ready")
	}
	if !l.Ready() || calls.Load() != 3 || fails != 2 {
		t.Fatalf("ready=%v calls=%d fails=%d", l.Ready(), calls.Load(), fails)
	}
}

// While the IdP is down, a login attempt answers 503 with Retry-After
// instead of a 500, and does not leave a dangling state entry.
func TestBeginLoginWhileIdPDownIs503(t *testing.T) {
	l := NewLazyProvider(Config{}, func(context.Context, Config) (Provider, error) {
		return nil, errors.New("connection refused")
	})
	svc := NewService(l, testStore(), Config{SessionTTL: time.Hour})
	rec := httptest.NewRecorder()
	svc.BeginLogin(rec, httptest.NewRequest(http.MethodGet, "/oauth/login", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("code=%d headers=%v", rec.Code, rec.Header())
	}
	if len(svc.states) != 0 {
		t.Fatalf("dangling login state: %d", len(svc.states))
	}
}
