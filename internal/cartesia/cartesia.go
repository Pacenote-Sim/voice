// Package cartesia is the one call this plugin makes outside the machine:
// text in, audio out.
//
// It is the whole of what this plugin knows about the vendor, kept apart so
// that the plugin's own code never sees a URL, a header name or a status code,
// and so that another vendor is another package and not another plugin.
package cartesia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultBaseURL is the vendor's API.
const DefaultBaseURL = "https://api.cartesia.ai"

// Version is the API version this package speaks, sent on every call. The
// vendor changes behaviour behind a new date, never behind the same one.
const Version = "2026-08-14"

// maxAudioBytes bounds one reply. A line of a dozen words is a few hundred
// kilobytes of WAV at most; a reply past this is not a line.
const maxAudioBytes = 8 << 20

// ErrRefused is the vendor refusing: a bad key, a quota, a rate limit, a voice
// that does not exist. An operator can act on it, and there is nothing to be
// gained from retrying it inside one call.
var ErrRefused = errors.New("cartesia: the request was refused")

// Client calls the text-to-speech endpoint.
type Client struct {
	// Key is the operator's own, lent for one call.
	Key string
	// BaseURL overrides [DefaultBaseURL] for tests.
	BaseURL string
	// HTTP is the client to use. A nil one is a default with no timeout of
	// its own, because every call carries the caller's deadline.
	HTTP *http.Client
}

// Request is one line to speak, and how.
type Request struct {
	Model    string
	VoiceID  string
	Language string
	Text     string
	// Container is "wav" or "mp3"; SampleRate is in hertz; BitRate is for mp3
	// only, in bits per second.
	Container  string
	SampleRate int
	BitRate    int
	// Speed is 0.6 to 1.5, or zero for the vendor's default.
	Speed float64
}

// ContentType is the media type of the audio a request produces.
func (r Request) ContentType() string {
	if r.Container == "mp3" {
		return "audio/mpeg"
	}
	return "audio/wav"
}

// The vendor's request body.
type body struct {
	ModelID      string       `json:"model_id"`
	Transcript   string       `json:"transcript"`
	Voice        voice        `json:"voice"`
	Language     string       `json:"language,omitempty"`
	OutputFormat outputFormat `json:"output_format"`
	Speed        float64      `json:"speed,omitempty"`
}

type voice struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

type outputFormat struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding,omitempty"`
	SampleRate int    `json:"sample_rate"`
	BitRate    int    `json:"bit_rate,omitempty"`
}

// Speak turns the text into audio bytes in the format asked for.
func (c Client) Speak(ctx context.Context, r Request) ([]byte, error) {
	format := outputFormat{Container: r.Container, SampleRate: r.SampleRate}
	if r.Container == "mp3" {
		format.BitRate = r.BitRate
	} else {
		format.Encoding = "pcm_s16le"
	}
	raw, err := json.Marshal(body{
		ModelID:      r.Model,
		Transcript:   r.Text,
		Voice:        voice{Mode: "id", ID: r.VoiceID},
		Language:     r.Language,
		OutputFormat: format,
		Speed:        r.Speed,
	})
	if err != nil {
		return nil, fmt.Errorf("cartesia: the request could not be encoded: %w", err)
	}

	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/tts/bytes", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("cartesia: the request could not be built: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cartesia-Version", Version)
	req.Header.Set("Authorization", "Bearer "+c.Key)

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cartesia: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()

	audio, err := io.ReadAll(io.LimitReader(res.Body, maxAudioBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cartesia: the reply could not be read: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, refusal(res.StatusCode, audio)
	}
	if len(audio) == 0 {
		return nil, errors.New("cartesia: the reply carried no audio")
	}
	if len(audio) > maxAudioBytes {
		return nil, errors.New("cartesia: the reply is larger than a line of speech")
	}
	return audio, nil
}

// refusal is what the vendor said, with the status, and without echoing a body
// that may quote the request.
func refusal(status int, raw []byte) error {
	var reply struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &reply)
	msg := reply.Error
	if msg == "" {
		msg = reply.Message
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &refusedError{status: status, message: msg}
}

type refusedError struct {
	status  int
	message string
}

func (e *refusedError) Error() string {
	return fmt.Sprintf("cartesia: %d %s", e.status, e.message)
}

func (e *refusedError) Is(target error) bool { return target == ErrRefused }

// Retryable reports whether waiting would plausibly help: the vendor being busy
// or briefly broken, as opposed to a key or a voice that is wrong. Nothing here
// retries — a driver is waiting — but the log line an operator reads should
// say which kind it was.
func Retryable(err error) bool {
	var r *refusedError
	if !errors.As(err, &r) {
		return false
	}
	return r.status == http.StatusTooManyRequests || r.status >= 500
}
