// Command voice is the plugin that speaks: text in, audio out, with Cartesia.
//
// It is started by the Pacenote server, not by a person. Running it from a
// shell prints the handshake line and exits, which is what go-plugin does when
// nothing is on the other end.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pacenote-sim/plugin"

	"github.com/pacenote-sim/voice"
)

// openTimeout bounds the one thing this program does before it starts serving.
const openTimeout = 10 * time.Second

func main() {
	// Standard error, because standard output is the handshake. The host
	// captures this, scrubs it and echoes it into its own log.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	store, err := openStore(log)
	if err != nil {
		// Fatal on purpose: this plugin declared a database, so one was made
		// for it, and running without it would pay for every line for a season.
		fmt.Fprintf(os.Stderr, "voice: %v\n", err)
		os.Exit(1)
	}
	if store != nil {
		defer store.Close()
	}

	plugin.Serve(voice.New(store, log))
}

// openStore connects to the database the host handed over. None is a line in
// the log rather than an exit: the plugin still speaks, it just pays each time.
func openStore(log *slog.Logger) (*voice.Store, error) {
	dsn, err := plugin.DatabaseURL()
	if err != nil {
		log.Warn("no database was provided, so every line will be paid for", "reason", err)
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	return voice.Open(ctx, dsn)
}
