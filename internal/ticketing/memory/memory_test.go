package memory

import (
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/contracttest"
)

func TestMemoryProviderContract(t *testing.T) {
	contracttest.Run(t, func(*testing.T) ticketing.Provider { return New("memory") })
}
