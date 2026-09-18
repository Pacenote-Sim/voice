package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// The plugin is tested against a vendor that answers whatever the test says.
// What is tested is everything around it: what it is asked, what comes back
// to a caller, what is refused, and what is never paid for twice.

// vendorSaying is a plugin whose vendor answers with these bytes, counting
// the calls.
func vendorSaying(t *testing.T, status int, audio []byte) (*Voice, *logs, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(status)
		_, _ = w.Write(audio)
	}))
	t.Cleanup(srv.Close)
	out := &logs{}
	v := New(nil, slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	v.BaseURL = srv.URL
	v.now = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }
	return v, out, &calls
}

type logs struct{ buf bytes.Buffer }

func (l *logs) Write(p []byte) (int, error) { return l.buf.Write(p) }
func (l *logs) String() string              { return l.buf.String() }

func settings() plugin.Values {
	return plugin.Values{
		SettingModel: ModelSonic36, SettingVoice: "voice-1", SettingLanguage: "en", SettingContainer: "mp3",
		SettingSampleRate: "24000", SettingBitRate: "64000", SettingSpeed: "1.1", SettingKeepDays: "30",
	}
}

func secrets() plugin.Secrets {
	return plugin.Secrets{SettingAPIKey: plugin.NewSecret("sk_car_notarealkey")}
}

// ask is another plugin asking, through the host.
func ask(t *testing.T, v *Voice, payload string, change ...func(*plugin.Request)) (plugin.Response, error) {
	t.Helper()
	r := plugin.Request{
		ID: "req-1", Kind: KindSpeak, From: "engineer", Deadline: time.Now().Add(5 * time.Second),
		Payload: json.RawMessage(payload), Settings: settings(), Secrets: secrets(),
	}
	for _, c := range change {
		c(&r)
	}
	return v.Answer(t.Context(), r)
}

func TestALineIsSpokenForAnotherPlugin(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	v, _, calls := vendorSaying(t, http.StatusOK, []byte("ID3audio"))
	res, err := ask(t, v, `{"text":"Brake twenty metres later into Turn 4."}`)
	r.NoError(err)
	r.Equal(KindSpeak, res.Kind)

	var out SpeakResponse
	r.NoError(json.Unmarshal(res.Payload, &out))
	r.Equal([]byte("ID3audio"), out.Audio)
	r.Equal("audio/mpeg", out.ContentType)
	r.Equal("mp3", out.Container)
	r.Equal(24000, out.SampleRate)
	r.Equal(38, out.Characters)
	r.False(out.Cached)
	r.Contains(string(res.Payload), `"audio":"SUQzYXVkaW8="`, "the audio is not base64 in the JSON")

	// The cost: the vendor bills characters, reported as input tokens under
	// the job and model.
	r.Equal("speak", res.Usage.Job)
	r.Equal(ModelSonic36, res.Usage.Model)
	r.EqualValues(38, res.Usage.InputTokens)
	r.Equal(1, *calls)
}

func TestWhatIsNotAQuestionForThisPlugin(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	v, _, calls := vendorSaying(t, http.StatusOK, []byte("audio"))

	_, err := ask(t, v, `{"text":"x"}`, func(q *plugin.Request) { q.Kind = "voice.sing" })
	r.ErrorIs(err, plugin.ErrUnsupported)

	_, err = ask(t, v, `{"text":"x"}`, func(q *plugin.Request) { q.Secrets = nil })
	r.ErrorIs(err, plugin.ErrNotConfigured, "no key")
	_, err = ask(t, v, `{"text":"x"}`, func(q *plugin.Request) { q.Settings = plugin.Values{} })
	r.ErrorIs(err, plugin.ErrNotConfigured, "no voice")

	_, err = ask(t, v, `{not json`)
	r.ErrorIs(err, plugin.ErrInvalid)
	_, err = ask(t, v, `{"text":"   "}`)
	r.ErrorIs(err, plugin.ErrInvalid)
	_, err = ask(t, v, `{"text":"`+strings.Repeat("a ", MaxTextRunes)+`"}`)
	r.ErrorIs(err, plugin.ErrInvalid)

	// None of it reached the vendor.
	r.Zero(*calls)

	// And a payload of nothing at all is an empty request, refused for its text.
	_, err = ask(t, v, ``)
	r.ErrorIs(err, plugin.ErrInvalid)
}

func TestTheWordsAreCleanedBeforeTheyAreSpoken(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	got, err := cleanText("  Brake\tlater,\n\n  ease   to seventy\x00 percent. ")
	r.NoError(err)
	r.Equal("Brake later, ease to seventy percent.", got)

	_, err = cleanText("")
	r.ErrorIs(err, errBadText)
	_, err = cleanText("\x00\x01")
	r.ErrorIs(err, errBadText)
	_, err = cleanText(string([]byte{0xff, 0xfe}))
	r.ErrorIs(err, errBadText)
	long := strings.Repeat("word ", 130)
	_, err = cleanText(long)
	r.ErrorContains(err, "at most 600")

	ok, err := cleanText(strings.Repeat("é", MaxTextRunes))
	r.NoError(err, "the limit is runes, not bytes")
	r.Len([]rune(ok), MaxTextRunes)
}

func TestTheSameWordsInAnotherVoiceAreAnotherClip(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	v, _, _ := vendorSaying(t, http.StatusOK, []byte("audio"))
	cfg := configOf(settings(), secrets())
	base := requestFor(cfg, "Brake later.", "")
	r.NotEqual(cacheKey(base), cacheKey(requestFor(cfg, "Brake later", "")), "punctuation is spoken")
	r.NotEqual(cacheKey(base), cacheKey(requestFor(cfg, "Brake later.", "voice-2")))
	other := cfg
	other.speed = 0.9
	r.NotEqual(cacheKey(base), cacheKey(requestFor(other, "Brake later.", "")))
	other = cfg
	other.container = "wav"
	r.NotEqual(cacheKey(base), cacheKey(requestFor(other, "Brake later.", "")))
	r.Equal(cacheKey(base), cacheKey(requestFor(cfg, "  Brake later. ", "")), "whitespace is not part of the words")

	// A caller's own voice is used, and paid for the same way.
	res, err := ask(t, v, `{"text":"Brake later.","voice_id":"voice-2"}`)
	r.NoError(err)
	r.EqualValues(12, res.Usage.InputTokens)

	// A caller that knows what language its words are in says so, and a
	// language that is not one is ignored rather than sent.
	other = cfg
	other.language = "es"
	r.NotEqual(cacheKey(base), cacheKey(requestFor(other, "Brake later.", "")), "the language is part of the clip")
}

func TestTheCallerMaySayWhatLanguageTheWordsAreIn(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		sent = append(sent, string(raw))
		_, _ = w.Write([]byte("audio"))
	}))
	t.Cleanup(srv.Close)
	v := New(nil, nil)
	v.BaseURL = srv.URL

	_, err := ask(t, v, `{"text":"Frena más tarde.","language":"es"}`)
	r.NoError(err)
	r.Contains(sent[0], `"language":"es"`)
	_, err = ask(t, v, `{"text":"Brake later.","language":"en-GB"}`)
	r.NoError(err)
	r.Contains(sent[1], `"language":"en-GB"`)
	_, err = ask(t, v, `{"text":"Brake later.","language":"Spanish; DROP"}`)
	r.NoError(err)
	r.Contains(sent[2], `"language":"en"`, "a language that is not one was sent to the vendor")
}

func TestWhenTheVendorRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		logged string
	}{
		{"a bad key", http.StatusUnauthorized, "will keep happening until it is fixed"},
		{"too fast", http.StatusTooManyRequests, "the vendor was busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)
			v, out, _ := vendorSaying(t, tc.status, []byte(`{"error":"no"}`))
			_, err := ask(t, v, `{"text":"Brake later."}`)
			r.Error(err)
			r.NotErrorIs(err, plugin.ErrNoAnswer, "a refusal is not nothing to say; the asker has the words")
			r.NotErrorIs(err, plugin.ErrInvalid)
			r.Contains(out.String(), tc.logged)
		})
	}

	t.Run("a vendor that cannot be reached is logged as such", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		out := &logs{}
		v := New(nil, slog.New(slog.NewTextHandler(out, nil)))
		v.BaseURL = "http://127.0.0.1:1"
		_, err := v.Answer(t.Context(), plugin.Request{
			ID: "r", Kind: KindSpeak, From: "engineer", Payload: json.RawMessage(`{"text":"x"}`),
			Settings: settings(), Secrets: secrets(),
		})
		r.Error(err)
		r.Contains(out.String(), "the line could not be spoken")
	})

	t.Run("the deadline is quiet", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			_, _ = w.Write([]byte("late"))
		}))
		defer srv.Close()
		defer close(release)
		out := &logs{}
		v := New(nil, slog.New(slog.NewTextHandler(out, nil)))
		v.BaseURL = srv.URL
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := v.Answer(ctx, plugin.Request{
			ID: "r", Kind: KindSpeak, From: "engineer", Payload: json.RawMessage(`{"text":"x"}`),
			Settings: settings(), Secrets: secrets(),
		})
		r.ErrorIs(err, context.DeadlineExceeded)
		r.NotContains(out.String(), "level=WARN")
		r.NotContains(out.String(), "level=ERROR")
	})
}

func TestTheDeclaredSettingsAreValid(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	declared, err := New(nil, nil).Settings(context.Background())
	r.NoError(err)
	r.NoError(plugin.ValidateSettings(declared))

	_, err = plugin.ValidateValues(declared, plugin.Values{})
	r.Error(err, "the key and the voice are required")
	_, err = plugin.ValidateValues(declared, plugin.Values{SettingAPIKey: "k"})
	r.Error(err, "the voice is required")

	values, err := plugin.ValidateValues(declared, plugin.Values{SettingAPIKey: "k", SettingVoice: "voice-1"})
	r.NoError(err)
	cfg := configOf(values, secrets())
	r.True(cfg.configured())
	r.Equal(ModelSonic36, cfg.model)
	r.Equal("en", cfg.language)
	r.Equal("mp3", cfg.container)
	r.Equal(24000, cfg.sampleRate)
	r.Equal(64000, cfg.bitRate)
	r.InDelta(1.0, cfg.speed, 0.001)
	r.Equal(DefaultKeepDays, cfg.keepDays)

	// A host that sends nothing still produces a usable shape, and an
	// out-of-range speed is the vendor's default rather than a refusal.
	bare := configOf(plugin.Values{SettingSpeed: "9"}, nil)
	r.False(bare.configured())
	r.Equal(ModelSonic36, bare.model)
	r.InDelta(1.0, bare.speed, 0.001)
	r.Equal(24000, bare.sampleRate)

	// Events are not this plugin's; one that arrives is nothing to do.
	use, err := New(nil, nil).Notify(context.Background(), plugin.Event{Kind: plugin.EventLapCompleted})
	r.NoError(err)
	r.Zero(use.Total())
	v := New(nil, nil)
	v.now = nil
	r.WithinDuration(time.Now(), v.clock(), time.Minute)
}

// requestFor is what speak() builds, for the tests of the key.
func requestFor(cfg config, text, voiceID string) cartesiaRequest {
	if voiceID == "" {
		voiceID = cfg.voice
	}
	clean, _ := cleanText(text)
	return cartesiaRequest{
		Model: cfg.model, VoiceID: voiceID, Language: cfg.language, Text: clean,
		Container: cfg.container, SampleRate: cfg.sampleRate, BitRate: cfg.bitRate, Speed: cfg.speed,
	}
}
