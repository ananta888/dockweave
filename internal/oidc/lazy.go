package oidc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrProviderUnavailable reports that OIDC discovery has not succeeded yet
// (for example because the IdP starts after the gateway).
var ErrProviderUnavailable = errors.New("identity provider unavailable")

// DiscoverFunc performs OIDC discovery; NewProvider in production.
type DiscoverFunc func(ctx context.Context, cfg Config) (Provider, error)

// LazyProvider performs discovery on demand and retries it until it
// succeeds, so an IdP that becomes reachable after the gateway started is
// picked up without a restart. Failed attempts are rate limited so a login
// storm cannot hammer an unreachable IdP.
type LazyProvider struct {
	cfg      Config
	discover DiscoverFunc
	timeout  time.Duration
	minRetry time.Duration
	now      func() time.Time

	mu       sync.Mutex
	prov     Provider
	lastTry  time.Time
	lastErr  error
	inFlight chan struct{}
}

// NewLazyProvider returns a provider that discovers the IdP lazily.
func NewLazyProvider(cfg Config, discover DiscoverFunc) *LazyProvider {
	if discover == nil {
		discover = NewProvider
	}
	return &LazyProvider{cfg: cfg, discover: discover, timeout: 10 * time.Second, minRetry: 5 * time.Second, now: time.Now}
}

// Ready reports whether discovery has succeeded.
func (l *LazyProvider) Ready() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prov != nil
}

// Ensure returns the discovered provider, attempting discovery when none is
// cached and the retry interval has elapsed. Concurrent callers share one
// attempt.
func (l *LazyProvider) Ensure(ctx context.Context) (Provider, error) {
	l.mu.Lock()
	if l.prov != nil {
		p := l.prov
		l.mu.Unlock()
		return p, nil
	}
	if wait := l.inFlight; wait != nil {
		l.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, ctx.Err())
		}
		return l.result()
	}
	if !l.lastTry.IsZero() && l.now().Sub(l.lastTry) < l.minRetry {
		err := l.lastErr
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	done := make(chan struct{})
	l.inFlight = done
	l.lastTry = l.now()
	l.mu.Unlock()

	dctx, cancel := context.WithTimeout(ctx, l.timeout)
	prov, err := l.discover(dctx, l.cfg)
	cancel()

	l.mu.Lock()
	if err == nil {
		l.prov = prov
		l.lastErr = nil
	} else {
		l.lastErr = err
	}
	l.inFlight = nil
	l.mu.Unlock()
	close(done)
	return l.result()
}

func (l *LazyProvider) result() (Provider, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prov != nil {
		return l.prov, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, l.lastErr)
}

// Warm retries discovery in the background with exponential backoff (capped
// at maxBackoff) until it succeeds or ctx ends. onReady runs once on success.
func (l *LazyProvider) Warm(ctx context.Context, maxBackoff time.Duration, onFail func(error), onReady func()) {
	backoff := l.minRetry
	for {
		_, err := l.Ensure(ctx)
		if err == nil {
			if onReady != nil {
				onReady()
			}
			return
		}
		if onFail != nil {
			onFail(err)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// AuthURL implements Provider.
func (l *LazyProvider) AuthURL(state, nonce, codeChallenge string) (string, error) {
	p, err := l.Ensure(context.Background())
	if err != nil {
		return "", err
	}
	return p.AuthURL(state, nonce, codeChallenge)
}

// Exchange implements Provider.
func (l *LazyProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Principal, error) {
	p, err := l.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	return p.Exchange(ctx, code, codeVerifier, nonce)
}

// EndSessionURL implements Provider; without discovery it falls back to "/".
func (l *LazyProvider) EndSessionURL(state string) string {
	l.mu.Lock()
	p := l.prov
	l.mu.Unlock()
	if p == nil {
		return "/"
	}
	return p.EndSessionURL(state)
}
