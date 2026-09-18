package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pacenote-sim/plugin"

	"github.com/pacenote-sim/voice/internal/cartesia"
)

// The one thing this plugin does, and the contract it does it under.
//
// Two callers, one job. A plugin asks voice.speak through the host, the way
// engineer does for every line it writes; a client posts to /speak with the
// same body and gets the audio itself. Both get the same audio for the same
// words, because a line already spoken is served from the cache for nothing.

// KindSpeak is the request another plugin asks this one for.
const KindSpeak plugin.RequestKind = "voice.speak"

// jobSpeak is what a spoken line is metered as. The vendor bills characters;
// they are reported as input tokens, and the README says so.
const jobSpeak = "speak"

// MaxTextRunes bounds one line. A cue is a dozen words and a radio line
// twelve; a paragraph is not something anybody wants read into a helmet, and
// it is what a mistaken caller sends.
const MaxTextRunes = 600

// SpeakRequest is what a caller sends: the words, and optionally a voice other
// than the operator's default.
type SpeakRequest struct {
	Text    string `json:"text"`
	VoiceID string `json:"voice_id,omitempty"`
	// Language is what the words are in, when the caller knows better than
	// the operator's default — engineer does, because it wrote them. A code
	// like "es" or "en-GB"; anything else is ignored.
	Language string `json:"language,omitempty"`
}

// languageCode is what a language is spelled as: two lowercase letters, with
// an optional region.
var languageCode = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

// SpeakResponse is the audio, and what it is. Audio is base64 in JSON, as
// encoding/json encodes bytes.
type SpeakResponse struct {
	Audio       []byte `json:"audio"`
	ContentType string `json:"content_type"`
	Container   string `json:"container"`
	SampleRate  int    `json:"sample_rate"`
	// Characters is what the vendor was billed for, zero when cached.
	Characters int `json:"characters"`
	// Cached reports that this line had been spoken before, and cost nothing.
	Cached bool `json:"cached"`
}

// errBadText is a request with nothing sayable in it.
var errBadText = errors.New("voice: the text is not a line that can be spoken")

// cleanText is the words as they will be spoken: whitespace collapsed, control
// characters gone, bounded.
func cleanText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: it is not valid text", errBadText)
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	switch n := utf8.RuneCountInString(out); {
	case n == 0:
		return "", fmt.Errorf("%w: it is empty", errBadText)
	case n > MaxTextRunes:
		return "", fmt.Errorf("%w: %d characters, and a line is at most %d", errBadText, n, MaxTextRunes)
	}
	return out, nil
}

// speak says the line, from the cache when it has been said before in this
// voice and shape, from the vendor otherwise. The usage is what the vendor
// was paid, or a cache hit.
func (v *Voice) speak(ctx context.Context, cfg config, req SpeakRequest) (SpeakResponse, plugin.Usage, error) {
	text, err := cleanText(req.Text)
	if err != nil {
		return SpeakResponse{}, plugin.Usage{}, err
	}
	voiceID := cfg.voice
	if req.VoiceID != "" {
		voiceID = req.VoiceID
	}
	language := cfg.language
	if languageCode.MatchString(req.Language) {
		language = req.Language
	}
	r := cartesia.Request{
		Model: cfg.model, VoiceID: voiceID, Language: language, Text: text,
		Container: cfg.container, SampleRate: cfg.sampleRate, BitRate: cfg.bitRate, Speed: cfg.speed,
	}
	key := cacheKey(r)

	if v.Store != nil {
		if clip, getErr := v.Store.Get(ctx, key); getErr == nil {
			return SpeakResponse{
				Audio: clip.Audio, ContentType: r.ContentType(), Container: clip.Container,
				SampleRate: clip.SampleRate, Cached: true,
			}, plugin.Usage{Job: jobSpeak, Model: cfg.model, Cached: true}, nil
		} else if !isNoRows(getErr) {
			v.Log.LogAttrs(ctx, slog.LevelWarn, "the cache could not be read, so the line was paid for",
				slog.String("reason", getErr.Error()))
		}
	}

	audio, err := v.client(cfg).Speak(ctx, r)
	characters := utf8.RuneCountInString(text)
	use := plugin.Usage{Job: jobSpeak, Model: cfg.model, InputTokens: int64(characters)}
	if err != nil {
		return SpeakResponse{}, plugin.Usage{}, v.vendorError(ctx, err)
	}
	if v.Store != nil {
		if keepErr := v.Store.Put(ctx, Clip{
			Key: key, Text: text, Model: cfg.model, VoiceID: voiceID, Language: language,
			Container: cfg.container, SampleRate: cfg.sampleRate, Characters: characters, Audio: audio,
			CreatedAt: v.clock(),
		}); keepErr != nil {
			v.Log.LogAttrs(ctx, slog.LevelWarn, "the line was spoken but could not be kept",
				slog.String("reason", keepErr.Error()))
		}
	}
	return SpeakResponse{
		Audio: audio, ContentType: r.ContentType(), Container: cfg.container,
		SampleRate: cfg.sampleRate, Characters: characters,
	}, use, nil
}

// cacheKey is everything that changes the audio. Two callers asking for the
// same words in the same voice and shape get the same clip.
func cacheKey(r cartesia.Request) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%d|%.2f|%s",
		r.Model, r.VoiceID, r.Language, r.Container, r.SampleRate, r.BitRate, r.Speed, r.Text)))
	return hex.EncodeToString(sum[:])
}

// Answer is another plugin asking for a line. The payload is a [SpeakRequest],
// the answer a [SpeakResponse].
func (v *Voice) Answer(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	if r.Kind != KindSpeak {
		return plugin.Response{}, fmt.Errorf("%w: this plugin answers %s and nothing else", plugin.ErrUnsupported, KindSpeak)
	}
	cfg := configOf(r.Settings, r.Secrets)
	if !cfg.configured() {
		return plugin.Response{}, plugin.ErrNotConfigured
	}
	var req SpeakRequest
	if err := json.Unmarshal(orEmptyObject(r.Payload), &req); err != nil {
		return plugin.Response{}, fmt.Errorf("%w: the question is not a speak request: %w", plugin.ErrInvalid, err)
	}
	out, use, err := v.speak(ctx, cfg, req)
	if err != nil {
		if errors.Is(err, errBadText) {
			return plugin.Response{}, fmt.Errorf("%w: %w", plugin.ErrInvalid, err)
		}
		// The vendor refused or is down. It is not "nothing to say" — the
		// asker has the words — so it travels as the failure it is, and the
		// asker decides: text only, this lap.
		return plugin.Response{Usage: use}, err
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return plugin.Response{}, fmt.Errorf("voice: the answer could not be encoded: %w", err)
	}
	return plugin.Response{Kind: KindSpeak, Payload: payload, Usage: use}, nil
}

// vendorError separates, for the operator's log, the refusal they must fix
// from the busy vendor that will pass; the caller gets the error as it is.
func (v *Voice) vendorError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// The caller's deadline, and the caller already knows.
	case cartesia.Retryable(err):
		v.Log.LogAttrs(ctx, slog.LevelWarn, "the vendor was busy, so nothing was spoken", slog.String("reason", err.Error()))
	case errors.Is(err, cartesia.ErrRefused):
		v.Log.LogAttrs(ctx, slog.LevelError, "the vendor refused, and this will keep happening until it is fixed",
			slog.String("reason", err.Error()))
	default:
		v.Log.LogAttrs(ctx, slog.LevelWarn, "the line could not be spoken", slog.String("reason", err.Error()))
	}
	return fmt.Errorf("voice: %w", err)
}

func orEmptyObject(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// cartesiaRequest is the vendor's request, named here so that the cache key
// can be tested from this package without importing the vendor.
type cartesiaRequest = cartesia.Request
