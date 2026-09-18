package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// A plugin the host gave no database still speaks; it just pays each time.
func TestNoDatabaseIsALineInTheLogAndNotAnExit(t *testing.T) {
	r := require.New(t)
	t.Setenv(plugin.EnvDatabaseURL, "")

	var buf bytes.Buffer
	store, err := openStore(slog.New(slog.NewTextHandler(&buf, nil)))
	r.NoError(err)
	r.Nil(store)
	r.Contains(buf.String(), "no database was provided")
}

// A database that was provided and will not open is fatal.
func TestADatabaseThatWillNotOpen(t *testing.T) {
	r := require.New(t)
	t.Setenv(plugin.EnvDatabaseURL, "postgres://nobody:nothing@127.0.0.1:1/pacenote?sslmode=disable&connect_timeout=1")

	_, err := openStore(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	r.Error(err)
}
