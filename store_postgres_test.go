//go:build postgres

package voice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// The cache, against a real PostgreSQL laid out the way the host lays one out.
// Behind a build tag for the same reason every other module's are: `go test
// ./...` has to pass on a machine with no PostgreSQL.

// EnvURL points at a PostgreSQL the tests may create schemas on.
const EnvURL = "PACENOTE_TEST_DATABASE_URL"

func plugged(t *testing.T) *Store {
	t.Helper()
	r := require.New(t)

	dsn := os.Getenv(EnvURL)
	if dsn == "" {
		t.Skipf("%s is not set, so there is nothing to run this against", EnvURL)
	}
	ctx := context.Background()

	var b [6]byte
	_, err := rand.Read(b[:])
	r.NoError(err)
	schema := "plugin_voice_test_" + hex.EncodeToString(b[:])

	admin, err := pgx.Connect(ctx, dsn)
	r.NoError(err)
	defer func() { _ = admin.Close(ctx) }()
	_, err = admin.Exec(ctx, `CREATE SCHEMA `+schema)
	r.NoError(err)
	t.Cleanup(func() {
		c, connErr := pgx.Connect(context.Background(), dsn)
		if connErr != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})

	files, err := filepath.Glob("migrations/*.sql")
	r.NoError(err)
	r.NotEmpty(files)
	sort.Strings(files)
	for _, f := range files {
		migration, readErr := os.ReadFile(f)
		r.NoError(readErr)
		_, err = admin.Exec(ctx, `SET search_path = `+schema+`;`+string(migration))
		r.NoErrorf(err, "%s would not apply", f)
	}

	u, err := url.Parse(dsn)
	r.NoError(err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := Open(ctx, u.String())
	r.NoError(err)
	t.Cleanup(store.Close)
	return store
}

func aClip(key string) Clip {
	return Clip{
		Key: key, Text: "Brake later.", Model: ModelSonic36, VoiceID: "voice-1", Language: "en",
		Container: "mp3", SampleRate: 24000, Characters: 12, Audio: []byte("ID3audio"),
	}
}

func TestAClipIsKeptAndServedAgain(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	store := plugged(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "nothing")
	r.ErrorIs(err, ErrNoRows)

	r.NoError(store.Put(ctx, aClip("k1")))
	got, err := store.Get(ctx, "k1")
	r.NoError(err)
	r.Equal([]byte("ID3audio"), got.Audio)
	r.Equal(1, got.Hits, "serving a clip is counted")
	got, err = store.Get(ctx, "k1")
	r.NoError(err)
	r.Equal(2, got.Hits)

	// The same key again keeps the first; the first is already being served.
	second := aClip("k1")
	second.Audio = []byte("other")
	r.NoError(store.Put(ctx, second))
	got, err = store.Get(ctx, "k1")
	r.NoError(err)
	r.Equal([]byte("ID3audio"), got.Audio)

	r.NoError(store.Put(ctx, aClip("k2")))
	recent, err := store.Recent(ctx, 10)
	r.NoError(err)
	r.Len(recent, 2)
	recent, err = store.Recent(ctx, 0)
	r.NoError(err)
	r.Len(recent, 2, "a limit nobody set is bounded, not zero")

	totals, err := store.Totals(ctx)
	r.NoError(err)
	r.Equal(Totals{Clips: 2, Characters: 24, Hits: 3}, totals)
}

func TestPruneKeepsWhatIsStillAskedFor(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	store := plugged(t)
	ctx := context.Background()

	old := aClip("old")
	old.CreatedAt = time.Now().Add(-60 * 24 * time.Hour)
	r.NoError(store.Put(ctx, old))
	r.NoError(store.Put(ctx, aClip("fresh")))

	removed, err := store.Prune(ctx, 0)
	r.NoError(err)
	r.Zero(removed, "zero days keeps everything")
	removed, err = store.Prune(ctx, DefaultKeepDays)
	r.NoError(err)
	r.EqualValues(1, removed)
	_, err = store.Get(ctx, "old")
	r.ErrorIs(err, ErrNoRows)

	// Serving a clip keeps it: last_used_at moves, not created_at.
	older := aClip("used")
	older.CreatedAt = time.Now().Add(-60 * 24 * time.Hour)
	r.NoError(store.Put(ctx, older))
	_, err = store.Get(ctx, "used")
	r.NoError(err)
	removed, err = store.Prune(ctx, DefaultKeepDays)
	r.NoError(err)
	r.Zero(removed, "a clip just served was removed")
}

func TestAStoreThatIsNotThere(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	ctx := context.Background()

	var none *Store
	_, err := none.Get(ctx, "k")
	r.Error(err)
	r.Error(none.Put(ctx, aClip("k")))
	_, err = none.Recent(ctx, 5)
	r.Error(err)
	_, err = none.Totals(ctx)
	r.Error(err)
	removed, err := none.Prune(ctx, 30)
	r.NoError(err)
	r.Zero(removed)
	r.NotPanics(none.Close)

	_, err = Open(ctx, "this is not a connection string")
	r.Error(err)
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = Open(short, "postgres://nobody:nothing@127.0.0.1:1/pacenote?sslmode=disable&connect_timeout=1")
	r.Error(err)
}

// The whole plugin: the same line asked twice is spoken once and served twice,
// by a plugin and by a client alike, and the page says so.
func TestTheSameLineIsPaidForOnce(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	store := plugged(t)

	v, _, calls := vendorSaying(t, http.StatusOK, []byte("ID3audio"))
	v.Store = store

	first, err := ask(t, v, `{"text":"Best lap of the stint, nice work."}`)
	r.NoError(err)
	r.EqualValues(33, first.Usage.InputTokens)
	r.False(first.Usage.Cached)

	second, err := ask(t, v, `{"text":"Best lap of the stint, nice work."}`)
	r.NoError(err)
	r.True(second.Usage.Cached, "the second time was paid for")
	r.Zero(second.Usage.Total())
	var out SpeakResponse
	r.NoError(json.Unmarshal(second.Payload, &out))
	r.True(out.Cached)
	r.Equal([]byte("ID3audio"), out.Audio)
	r.Equal("audio/mpeg", out.ContentType)

	// The client asking for the same words gets the same clip, for nothing.
	res := post(t, v, `{"text":"Best lap of the stint, nice work."}`)
	r.Equal(http.StatusOK, res.Status)
	r.Equal("true", res.Header.Get("X-Voice-Cached"))
	r.Equal(1, *calls, "the vendor was asked more than once for the same words")

	// Different words are a different clip.
	res = post(t, v, `{"text":"Half a second off, keep it tidy."}`)
	r.Equal(http.StatusOK, res.Status)
	r.Equal(2, *calls)

	page, err := v.ServeHTTP(t.Context(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.NoError(err)
	r.Contains(string(page.Body), "Best lap of the stint, nice work.")
	r.Contains(string(page.Body), "served 2 more time(s)")
	r.Contains(string(page.Body), "<td class=\"num\">2</td>", "two lines kept")
}

// A cache that cannot be read is a line paid for, not a line lost.
func TestACacheThatWentAwayStillSpeaks(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	store := plugged(t)

	v, out, _ := vendorSaying(t, http.StatusOK, []byte("audio"))
	v.Store = store
	store.Close()

	res, err := ask(t, v, `{"text":"Brake later."}`)
	r.NoError(err, "a dead cache failed the line")
	r.EqualValues(12, res.Usage.InputTokens)
	r.Contains(out.String(), "could not be read")
	r.Contains(out.String(), "could not be kept")

	// And the page says so rather than showing an empty cache.
	page, err := v.ServeHTTP(t.Context(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.NoError(err)
	r.Equal(http.StatusBadGateway, page.Status)
	r.Contains(string(page.Body), "could not be read just now")
}

// An empty cache is a page that says nothing has been spoken yet.
func TestTheOperatorsPageWithNothingSpoken(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	v := New(plugged(t), nil)
	page, err := v.ServeHTTP(t.Context(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.NoError(err)
	r.Equal(http.StatusOK, page.Status)
	r.Contains(string(page.Body), "Nothing yet")
	r.Contains(string(page.Body), "<td class=\"num\">0</td>")
}
