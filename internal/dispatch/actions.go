package dispatch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// actions serialises side effects per key and records their outcome
// durably (wrapper parity: action_state/perform_once in pipeline.py).
type actions struct {
	log    ActionLog
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (a *actions) lock(key string) func() {
	a.mu.Lock()
	if a.locks == nil {
		a.locks = map[string]*sync.Mutex{}
	}
	m, ok := a.locks[key]
	if !ok {
		m = &sync.Mutex{}
		a.locks[key] = m
	}
	a.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// pendingBlocks: a pending marker inside the reconcile window means the
// earlier attempt may still have succeeded – do not repeat it yet.
func (a *actions) pendingBlocks(st model.DispatchAction) bool {
	return st.Status == "pending" && !st.Since.IsZero() && a.now().Sub(st.Since) < a.window
}

// unknownOutcome reports errors after which the side effect may or may not
// have happened (timeouts, unreachable provider).
func unknownOutcome(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ticketing.ErrUnavailable)
}

var errUnknownOutcome = errors.New("previous attempt has unknown outcome; reconcile before retry")

// once performs fn at most once successfully per key. A confirmed effect is
// never repeated; an unknown one blocks a retry within the reconcile window.
func (a *actions) once(ctx context.Context, key string, fn func() (string, error)) (string, error) {
	defer a.lock(key)()
	st, err := a.log.GetDispatchAction(ctx, key)
	if err != nil {
		return "", err
	}
	if st.Status == "done" {
		return st.Result, nil
	}
	if a.pendingBlocks(st) {
		return "", errUnknownOutcome
	}
	if err := a.log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "pending", Since: a.now()}); err != nil {
		return "", err
	}
	res, err := fn()
	if err == nil {
		return res, a.log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "done", Since: a.now(), Result: res})
	}
	if !unknownOutcome(err) {
		_ = a.log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "retry", Since: a.now()})
	}
	return "", err
}
