package voice

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The vendor allows so many requests at a time and refuses the rest, so the
// gate holds callers to that number rather than letting the account do the
// refusing.
func TestTheGateHoldsCallersToThePlan(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	var g gate
	var inside, peak atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := g.enter(context.Background(), 3)
			if err != nil {
				return
			}
			defer release()
			n := inside.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inside.Add(-1)
		}()
	}
	close(start)
	wg.Wait()
	r.LessOrEqual(peak.Load(), int64(3), "more lines were at the vendor than the plan allows")
	r.Positive(peak.Load())
	r.Zero(inside.Load(), "a slot was not given back")
}

// A caller that waits longer than it can afford gives up, and says so in a way
// that tells the operator nothing was spent.
func TestACallerThatCannotWaitGivesUp(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	var g gate
	held, err := g.enter(context.Background(), 1)
	r.NoError(err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release, err := g.enter(ctx, 1)
	r.Nil(release)
	r.ErrorIs(err, ErrBusy)
	r.ErrorIs(err, context.DeadlineExceeded)

	// With the slot back, the next caller goes through.
	held()
	release, err = g.enter(context.Background(), 1)
	r.NoError(err)
	release()
	release() // giving a slot back twice is one slot back
	release2, err := g.enter(context.Background(), 1)
	r.NoError(err)
	release2()
}

// Changing plan changes the gate, without a restart and without stranding the
// callers that hold a slot of the old size.
func TestTheGateFollowsTheSetting(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	var g gate
	one, err := g.enter(context.Background(), 1)
	r.NoError(err)

	// At one, a second caller waits; at three, it does not.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = g.enter(ctx, 1)
	r.Error(err)
	wider, err := g.enter(context.Background(), 3)
	r.NoError(err)
	wider()
	one() // the old size's slot goes back where it came from
	r.Equal(3, g.size)

	// A size below one is still one: a gate that admits nobody is a plugin
	// that speaks nothing.
	release, err := g.enter(context.Background(), 0)
	r.NoError(err)
	release()
	r.Equal(1, g.size)
}

// A line already spoken never reaches the gate: it is served from the cache,
// so a full gate does not stop it. Checked here through the setting, which is
// what the operator turns.
func TestTheSettingIsRead(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	values := settings()
	r.Equal(DefaultAtOnce, configOf(values, nil).atOnce, "nothing set is the smallest plan")
	values[SettingAtOnce] = "15"
	r.Equal(15, configOf(values, nil).atOnce)
	values[SettingAtOnce] = "9999"
	r.Equal(MaxAtOnce, configOf(values, nil).atOnce, "a number typed in a box has a ceiling")
	values[SettingAtOnce] = "0"
	r.Equal(DefaultAtOnce, configOf(values, nil).atOnce)
	values[SettingAtOnce] = "not a number"
	r.Equal(DefaultAtOnce, configOf(values, nil).atOnce)
}
