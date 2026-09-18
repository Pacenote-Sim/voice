package voice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This plugin's one table, in its own schema: the lines it has spoken.
//
// The audio is in the row. A clip is tens of kilobytes, there are a few per
// lap, and a month of them is a few hundred megabytes at the very most; a
// second place to keep bytes would be a second thing to back up and lose.

// Clip is one spoken line, as kept.
type Clip struct {
	// Key is everything that changes the audio, hashed: the same words in the
	// same voice and shape are one clip.
	Key        string
	Text       string
	Model      string
	VoiceID    string
	Language   string
	Container  string
	SampleRate int
	Characters int
	Audio      []byte
	CreatedAt  time.Time
	LastUsedAt time.Time
	// Hits is how many times it was served again for nothing.
	Hits int
}

// Store is this plugin's database.
type Store struct{ pool *pgxpool.Pool }

// Open connects to the database the host handed over.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("voice: the connection string is not one: %w", err)
	}
	// Four is enough: a lookup and a write a line, a page now and then.
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("voice: the database would not open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("voice: the database would not answer: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close returns every connection.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

var errNoStore = errors.New("voice: there is no database")

func (s *Store) ready() error {
	if s == nil || s.pool == nil {
		return errNoStore
	}
	return nil
}

// ErrNoRows is a lookup that found nothing.
var ErrNoRows = pgx.ErrNoRows

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Get is the clip for a key, or [ErrNoRows], and marks it served.
func (s *Store) Get(ctx context.Context, key string) (Clip, error) {
	if err := s.ready(); err != nil {
		return Clip{}, err
	}
	var c Clip
	err := s.pool.QueryRow(ctx, `
		UPDATE clips SET hits = hits + 1, last_used_at = now()
		WHERE key = $1
		RETURNING key, text, model, voice_id, language, container, sample_rate, characters, audio, created_at, last_used_at, hits`, key).
		Scan(&c.Key, &c.Text, &c.Model, &c.VoiceID, &c.Language, &c.Container, &c.SampleRate, &c.Characters, &c.Audio, &c.CreatedAt, &c.LastUsedAt, &c.Hits)
	if err != nil {
		if isNoRows(err) {
			return Clip{}, ErrNoRows
		}
		return Clip{}, fmt.Errorf("voice: the clip could not be read: %w", err)
	}
	return c, nil
}

// Put keeps a clip. The same key twice keeps the first: the audio is the same
// words in the same voice, and the first one is already being served.
func (s *Store) Put(ctx context.Context, c Clip) error {
	if err := s.ready(); err != nil {
		return err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO clips (key, text, model, voice_id, language, container, sample_rate, characters, audio, created_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (key) DO NOTHING`,
		c.Key, c.Text, c.Model, c.VoiceID, c.Language, c.Container, c.SampleRate, c.Characters, c.Audio, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("voice: the clip could not be kept: %w", err)
	}
	return nil
}

// Recent is the last clips spoken, newest first, for the operator's page.
func (s *Store) Recent(ctx context.Context, limit int) ([]Clip, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT key, text, model, voice_id, language, container, sample_rate, characters, audio, created_at, last_used_at, hits
		FROM clips ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("voice: the clips could not be read: %w", err)
	}
	defer rows.Close()
	var out []Clip
	for rows.Next() {
		var c Clip
		if err := rows.Scan(&c.Key, &c.Text, &c.Model, &c.VoiceID, &c.Language, &c.Container, &c.SampleRate,
			&c.Characters, &c.Audio, &c.CreatedAt, &c.LastUsedAt, &c.Hits); err != nil {
			return nil, fmt.Errorf("voice: a clip could not be read: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("voice: the clips could not be read: %w", err)
	}
	return out, nil
}

// Totals is what the cache holds: how many clips, how many characters were
// paid for, and how many times a clip was served again for nothing.
type Totals struct {
	Clips, Characters, Hits int64
}

// Totals counts the cache.
func (s *Store) Totals(ctx context.Context) (Totals, error) {
	if err := s.ready(); err != nil {
		return Totals{}, err
	}
	var t Totals
	err := s.pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(characters), 0), coalesce(sum(hits), 0) FROM clips`).
		Scan(&t.Clips, &t.Characters, &t.Hits)
	if err != nil {
		return Totals{}, fmt.Errorf("voice: the clips could not be counted: %w", err)
	}
	return t, nil
}

// Prune removes clips not served for longer than the operator asked to keep
// them. Zero days keeps everything.
func (s *Store) Prune(ctx context.Context, keepDays int) (int64, error) {
	if s == nil || s.pool == nil || keepDays <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM clips WHERE last_used_at < now() - make_interval(days => $1)`, keepDays)
	if err != nil {
		return 0, fmt.Errorf("voice: old clips could not be removed: %w", err)
	}
	return tag.RowsAffected(), nil
}
