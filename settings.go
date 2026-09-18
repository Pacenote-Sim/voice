package voice

import (
	"strconv"

	"github.com/pacenote-sim/plugin"
)

// What the operator fills in: whose key is being spent, which voice, in which
// language, and what the audio is shaped like. Everything about how a line is
// spoken is here, because every one of them is a taste and a taste is the
// operator's.
const (
	SettingAPIKey     = "api_key"
	SettingModel      = "model"
	SettingVoice      = "voice_id"
	SettingLanguage   = "language"
	SettingContainer  = "container"
	SettingSampleRate = "sample_rate"
	SettingBitRate    = "bit_rate"
	SettingSpeed      = "speed"
	SettingKeepDays   = "keep_days"
)

// The models offered. A fixed list rather than a text field: a model id typed
// by hand fails at the vendor with a message the operator cannot act on.
const (
	ModelSonic36 = "sonic-3.6"
	ModelSonic3  = "sonic-3"
	ModelSonic2  = "sonic-2"
)

// DefaultKeepDays is how long a spoken line is kept so that the same line is
// not paid for twice. A month covers a season's worth of "best lap of the
// stint" without keeping every line for ever.
const DefaultKeepDays = 30

// Settings is what the panel renders.
func Settings() []plugin.Setting {
	return []plugin.Setting{
		{
			Name:     SettingAPIKey,
			Label:    "Cartesia key",
			Help:     "Yours, from play.cartesia.ai. It is sealed with this server's data key and lent to this plugin one call at a time.",
			Kind:     plugin.KindSecret,
			Required: true,
		},
		{
			Name:        SettingVoice,
			Label:       "Voice",
			Help:        "The id of a voice from play.cartesia.ai → Voices. Every line is spoken in it until a caller asks for another.",
			Kind:        plugin.KindText,
			Required:    true,
			Placeholder: "a0e99841-438c-4a64-b679-ae501e7d6091",
		},
		{
			Name:  SettingModel,
			Label: "Model",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: ModelSonic36, Label: "Sonic 3.6", Note: "the current one; follows the vendor's stable releases"},
				{Value: ModelSonic3, Label: "Sonic 3"},
				{Value: ModelSonic2, Label: "Sonic 2"},
			},
			Default: ModelSonic36,
		},
		{
			Name:  SettingLanguage,
			Label: "Language",
			Help:  "The language the lines are written in. The coach writes English unless its persona says otherwise.",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "en", Label: "English"},
				{Value: "es", Label: "Spanish"},
				{Value: "de", Label: "German"},
				{Value: "fr", Label: "French"},
				{Value: "it", Label: "Italian"},
				{Value: "pt", Label: "Portuguese"},
				{Value: "nl", Label: "Dutch"},
				{Value: "pl", Label: "Polish"},
			},
			Default: "en",
		},
		{
			Name:  SettingContainer,
			Label: "Audio format",
			Help:  "MP3 is a tenth of the size and what a client fetching three lines a lap wants; WAV needs no decoder.",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "mp3", Label: "MP3", Note: "about 30 kilobytes a line"},
				{Value: "wav", Label: "WAV", Note: "16-bit PCM; about 200 kilobytes a line"},
			},
			Default: "mp3",
		},
		{
			Name:  SettingSampleRate,
			Label: "Sample rate",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "16000", Label: "16 kHz", Note: "speech-grade, smallest"},
				{Value: "22050", Label: "22.05 kHz"},
				{Value: "24000", Label: "24 kHz", Note: "the vendor's native rate"},
				{Value: "44100", Label: "44.1 kHz"},
			},
			Default: "24000",
		},
		{
			Name:  SettingBitRate,
			Label: "MP3 bit rate",
			Help:  "Ignored for WAV.",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "32000", Label: "32 kbit/s"},
				{Value: "64000", Label: "64 kbit/s", Note: "plenty for a voice"},
				{Value: "96000", Label: "96 kbit/s"},
				{Value: "128000", Label: "128 kbit/s"},
			},
			Default: "64000",
		},
		{
			Name:  SettingSpeed,
			Label: "Speed",
			Help:  "A line for the corner ahead is heard at 200 kilometres per hour; a little quicker than a narrator is right.",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "0.8", Label: "Slower"},
				{Value: "0.9", Label: "A little slower"},
				{Value: "1.0", Label: "Normal"},
				{Value: "1.1", Label: "A little quicker"},
				{Value: "1.2", Label: "Quicker"},
			},
			Default: "1.0",
		},
		{
			Name:        SettingKeepDays,
			Label:       "Keep spoken lines for",
			Help:        "Days. A line already spoken is served again for nothing; this is how long it is kept. Zero keeps them for ever.",
			Kind:        plugin.KindNumber,
			Default:     "30",
			Placeholder: "30",
		},
	}
}

// config is the settings as this plugin uses them, read once per call from
// what the host lends it.
type config struct {
	key        plugin.Secret
	model      string
	voice      string
	language   string
	container  string
	sampleRate int
	bitRate    int
	speed      float64
	keepDays   int
}

// configOf reads a call's settings. A value the host did not send falls back
// to the declared default, which [plugin.ValidateValues] has already applied;
// the defaults are repeated here rather than left as zero for a host that sent
// nothing.
func configOf(values plugin.Values, secrets plugin.Secrets) config {
	c := config{
		key:       secrets[SettingAPIKey],
		model:     orDefault(values.String(SettingModel), ModelSonic36),
		voice:     values.String(SettingVoice),
		language:  orDefault(values.String(SettingLanguage), "en"),
		container: orDefault(values.String(SettingContainer), "mp3"),
		keepDays:  DefaultKeepDays,
	}
	c.sampleRate = intOr(values, SettingSampleRate, 24000)
	c.bitRate = intOr(values, SettingBitRate, 64000)
	if f, err := strconv.ParseFloat(values.String(SettingSpeed), 64); err == nil && f >= 0.6 && f <= 1.5 {
		c.speed = f
	} else {
		c.speed = 1.0
	}
	if days, ok := values.Int(SettingKeepDays); ok {
		c.keepDays = days
	}
	return c
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func intOr(values plugin.Values, name string, def int) int {
	if n, ok := values.Int(name); ok && n > 0 {
		return n
	}
	return def
}

// configured reports whether this plugin can speak at all: a key, and a voice
// to speak in. Either missing is [plugin.ErrNotConfigured], which a caller
// falls back from — the line is still there as text.
func (c config) configured() bool { return !c.key.Empty() && c.voice != "" }
