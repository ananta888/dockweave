package ticketing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Config selects the primary provider and an optional mirror (ADR 0003 D-3).
// Options are adapter specific (e.g. Paperclip URL); they never contain
// secrets inline – adapters read secrets from files named here.
type Config struct {
	Primary string            `json:"primary"`
	Mirror  string            `json:"mirror,omitempty"` // "" or "none" = no mirror
	Options map[string]string `json:"options,omitempty"`
}

// Factory builds a provider from the configuration (Abstract Factory).
type Factory func(cfg Config) (Provider, error)

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes an adapter selectable by name. Adapters call it from their
// constructor package; registering a name twice panics (programming error).
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || f == nil {
		panic("ticketing: empty adapter registration")
	}
	if _, dup := factories[name]; dup {
		panic("ticketing: adapter registered twice: " + name)
	}
	factories[name] = f
}

// Registered lists the selectable adapter names.
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(factories))
	for n := range factories {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Selection is the opened primary provider and the optional mirror.
type Selection struct {
	Primary Provider
	Mirror  Provider // nil when no mirror is configured
}

// Open builds the configured providers. An unknown name is an error, never a
// silent fallback to another provider: switching stores implicitly would
// split tickets (ADR 0003 D-3).
func Open(cfg Config) (*Selection, error) {
	primary := strings.ToLower(strings.TrimSpace(cfg.Primary))
	if primary == "" {
		return nil, fmt.Errorf("%w: no ticket provider configured", ErrInvalid)
	}
	mirror := strings.ToLower(strings.TrimSpace(cfg.Mirror))
	if mirror == "none" {
		mirror = ""
	}
	if mirror == primary {
		return nil, fmt.Errorf("%w: mirror must differ from the primary provider %q", ErrInvalid, primary)
	}
	p, err := build(primary, cfg)
	if err != nil {
		return nil, err
	}
	sel := &Selection{Primary: p}
	if mirror != "" {
		m, err := build(mirror, cfg)
		if err != nil {
			return nil, fmt.Errorf("mirror: %w", err)
		}
		sel.Mirror = m
	}
	return sel, nil
}

func build(name string, cfg Config) (Provider, error) {
	regMu.RLock()
	f, ok := factories[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: unknown ticket provider %q (available: %s)", ErrInvalid, name, strings.Join(Registered(), ", "))
	}
	p, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("open ticket provider %q: %w", name, err)
	}
	return p, nil
}
