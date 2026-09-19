package voice

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// The vendor allows so many requests in flight at once, and how many depends on
// what the operator pays for: two on the smallest plan, fifteen on the largest.
// A plugin asking for a lap's lines asks for all of them together, and a client
// speaking a line of its own arrives in the middle of that, so without a gate
// the account's limit is reached by ordinary use and the vendor refuses the
// difference — which reaches the driver as silence.
//
// So every call that will reach the vendor takes a slot first and gives it back
// after. A line already spoken never gets here: it comes from the cache, costs
// nothing and waits for nobody.

// ErrBusy reports a line that waited for a slot until its caller gave up. It is
// not a failure of the vendor and nothing was spent; the caller speaks the
// words itself.
var ErrBusy = errors.New("voice: too many lines are being spoken at once")

// gate admits a fixed number of callers at a time. Its size is the operator's
// setting, read on every call, so raising the plan raises the gate without a
// restart.
type gate struct {
	mu    sync.Mutex
	size  int
	slots chan struct{}
}

// enter waits for a slot and returns the release. The context governs the
// wait: a caller with a deadline of its own gives up when that passes, and
// gets [ErrBusy] rather than a vendor error, because the vendor was never
// asked.
func (g *gate) enter(ctx context.Context, size int) (release func(), err error) {
	slots := g.open(size)
	select {
	case slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots }) }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
}

// open is the channel of slots at the wanted size, built on the first call and
// rebuilt when the setting changes. Callers already holding a slot of the old
// size release into the old channel, which is then finished with: a resize is
// an operator changing plan, not something that happens during a lap.
func (g *gate) open(size int) chan struct{} {
	if size < 1 {
		size = 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.slots == nil || g.size != size {
		g.slots, g.size = make(chan struct{}, size), size
	}
	return g.slots
}
