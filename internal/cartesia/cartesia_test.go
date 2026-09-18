package cartesia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// vendor answers the way Cartesia does and records what it was asked.
func vendor(t *testing.T, status int, body []byte) (Client, *http.Header, *[]byte) {
	t.Helper()
	var header http.Header
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		sent, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/tts/bytes" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return Client{Key: "sk_car_notarealkey", BaseURL: srv.URL}, &header, &sent
}

func aRequest() Request {
	return Request{
		Model: "sonic-3.6", VoiceID: "voice-1", Language: "en", Text: "Brake twenty metres later into Turn 4.",
		Container: "mp3", SampleRate: 24000, BitRate: 64000, Speed: 1.1,
	}
}

func TestSpeakAsksTheVendorTheWayItDocuments(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	c, header, sent := vendor(t, http.StatusOK, []byte("ID3\x03audio"))
	audio, err := c.Speak(t.Context(), aRequest())
	r.NoError(err)
	r.Equal([]byte("ID3\x03audio"), audio)

	// The credential in the header, never the body; the version pinned.
	r.Equal("Bearer sk_car_notarealkey", header.Get("Authorization"))
	r.Equal(Version, header.Get("Cartesia-Version"))
	r.Equal("application/json", header.Get("Content-Type"))
	r.NotContains(string(*sent), "sk_car_")

	var req map[string]any
	r.NoError(json.Unmarshal(*sent, &req))
	r.Equal("sonic-3.6", req["model_id"])
	r.Equal("Brake twenty metres later into Turn 4.", req["transcript"])
	r.Equal("en", req["language"])
	r.InDelta(1.1, req["speed"], 0.001)
	voice, ok := req["voice"].(map[string]any)
	r.True(ok)
	r.Equal("id", voice["mode"])
	r.Equal("voice-1", voice["id"])
	format, ok := req["output_format"].(map[string]any)
	r.True(ok)
	r.Equal("mp3", format["container"])
	r.InDelta(24000, format["sample_rate"], 0)
	r.InDelta(64000, format["bit_rate"], 0)
	_, hasEncoding := format["encoding"]
	r.False(hasEncoding, "mp3 has no PCM encoding")
}

func TestWAVIsSixteenBitPCM(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	c, _, sent := vendor(t, http.StatusOK, []byte("RIFFwav"))
	req := aRequest()
	req.Container, req.Speed = "wav", 0
	_, err := c.Speak(t.Context(), req)
	r.NoError(err)
	r.Equal("audio/wav", req.ContentType())
	r.Equal("audio/mpeg", aRequest().ContentType())

	var body map[string]any
	r.NoError(json.Unmarshal(*sent, &body))
	format, ok := body["output_format"].(map[string]any)
	r.True(ok)
	r.Equal("wav", format["container"])
	r.Equal("pcm_s16le", format["encoding"])
	_, hasBitRate := format["bit_rate"]
	r.False(hasBitRate, "WAV has no bit rate")
	_, hasSpeed := body["speed"]
	r.False(hasSpeed, "no speed asked for is the vendor's default, not zero")
}

func TestWhenTheVendorRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		status    int
		body      string
		retryable bool
		says      string
	}{
		{"a bad key", http.StatusUnauthorized, `{"error":"invalid api key"}`, false, "401 invalid api key"},
		{"a voice that does not exist", http.StatusNotFound, `{"message":"voice not found"}`, false, "404 voice not found"},
		{"too fast", http.StatusTooManyRequests, `not json`, true, "429 Too Many Requests"},
		{"the vendor broken", http.StatusInternalServerError, ``, true, "500 Internal Server Error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)
			c, _, _ := vendor(t, tc.status, []byte(tc.body))
			_, err := c.Speak(t.Context(), aRequest())
			r.ErrorIs(err, ErrRefused)
			r.Equal(tc.retryable, Retryable(err))
			r.ErrorContains(err, tc.says)
		})
	}
	require.False(t, Retryable(context.DeadlineExceeded), "only a refusal can be retryable")
}

func TestARepliyThatIsNotAudio(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	empty, _, _ := vendor(t, http.StatusOK, nil)
	_, err := empty.Speak(t.Context(), aRequest())
	r.ErrorContains(err, "no audio")

	huge, _, _ := vendor(t, http.StatusOK, []byte(strings.Repeat("x", maxAudioBytes+1)))
	_, err = huge.Speak(t.Context(), aRequest())
	r.ErrorContains(err, "larger than a line")
}

func TestTheDeadlineIsTheCallers(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := Client{Key: "k", BaseURL: srv.URL}.Speak(ctx, aRequest())
	r.ErrorIs(err, context.DeadlineExceeded)

	// And a vendor that cannot be reached at all.
	_, err = Client{Key: "k", BaseURL: "http://127.0.0.1:1"}.Speak(t.Context(), aRequest())
	r.Error(err)
	r.NotErrorIs(err, ErrRefused)
}
