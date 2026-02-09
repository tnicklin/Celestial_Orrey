package raiderio

import "context"

// Poller defines the interface for polling RaiderIO for new M+ keys.
type Poller interface {
	Start(ctx context.Context) error
	Stop()
}
