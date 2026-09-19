// Package voice is the plugin that speaks: a line of text in, audio out, with
// Cartesia. It has no opinion about racing. Engineer asks it for every line it
// writes, so a client gets words and audio in one answer; a client with words
// of its own posts them to /speak and gets the audio back.
//
// Every line spoken is kept, so the same words in the same voice are paid for
// once: the radio repeats itself, and so does a coach.
package voice

import (
	"context"
	"log/slog"
	"time"

	"github.com/pacenote-sim/plugin"

	"github.com/pacenote-sim/voice/internal/cartesia"
)

// Voice implements the plugin contract.
type Voice struct {
	// Store keeps spoken lines. A nil store is a plugin without a database,
	// which works and pays for every line.
	Store *Store
	// Log receives this plugin's own lines, on standard error, which the host
	// captures, scrubs and echoes into its log.
	Log *slog.Logger
	// BaseURL overrides where the vendor is, for tests.
	BaseURL string
	// now is the clock, injectable for tests.
	now func() time.Time
	// gate holds callers to the number of requests the operator's plan allows
	// in flight at once.
	gate gate
}

// New builds the plugin.
func New(store *Store, log *slog.Logger) *Voice {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Voice{Store: store, Log: log, now: time.Now}
}

// Settings declares what the operator has to configure.
func (v *Voice) Settings(context.Context) ([]plugin.Setting, error) { return Settings(), nil }

// Notify is told something happened. This plugin asks for no events, so
// anything that arrives is a manifest somebody edited, and is nothing to do.
func (v *Voice) Notify(context.Context, plugin.Event) (plugin.Usage, error) {
	return plugin.Usage{}, nil
}

// client is the vendor client for one call, holding the operator's key for no
// longer than the call takes.
func (v *Voice) client(cfg config) cartesia.Client {
	return cartesia.Client{Key: cfg.key.Value(), BaseURL: v.BaseURL}
}

func (v *Voice) clock() time.Time {
	if v.now == nil {
		return time.Now()
	}
	return v.now()
}
