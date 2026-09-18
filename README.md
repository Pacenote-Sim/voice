# voice

A Pacenote server plugin that turns a line of text into speech, using [Cartesia](https://cartesia.ai)
with the operator's own key. It exists so that what other plugins write can be heard: engineer asks
it to speak every line it writes, and a client with words of its own posts them and gets the audio
back. It knows nothing about racing. A line already spoken in the same voice is kept and served
again without a second call to the vendor.

It runs inside a Pacenote server as a separate program; the server is not changed for it.

## Installing it

```
make dist
cp -R dist/voice <the server's>/pacenote-data/plugins/voice
```

The folder must be called `voice` — that is the name in `voice.speak`, and an ElevenLabs build of
this plugin would keep it so that nothing asking would change. In the panel: *Look for new plugins*,
enable, then set a key and a voice.

## Configuring it

| Setting | |
|---|---|
| **Cartesia key** | Required. From play.cartesia.ai. Sealed by the server, lent one call at a time. |
| **Voice** | Required. A voice id from play.cartesia.ai → Voices. |
| **Model** | Sonic 3.6 (default), 3 or 2. |
| **Language** | English by default. |
| **Audio format** | MP3 (default, ~30 KB a line) or WAV (16-bit PCM, ~200 KB). |
| **Sample rate** | 24 kHz by default. |
| **MP3 bit rate** | 64 kbit/s by default. |
| **Speed** | 0.8–1.2×. A line for the corner ahead is heard at speed; a little quicker than a narrator is right. |
| **Keep spoken lines for** | Days a clip is kept after it was last served. 30 by default; zero keeps them for ever. |

Without a key or a voice the plugin says it is not configured and every caller falls back to text.

## Asking it, as a plugin

Declare `"asks": ["voice"]` and ask `voice.speak` through the host:

```json
{"text": "Brake twenty metres later into Turn 4.",
 "voice_id": "optional, the operator's by default",
 "language": "optional, the operator's by default — engineer sends the language it wrote in"}
```

The answer:

```json
{"audio": "<base64>", "content_type": "audio/mpeg", "container": "mp3", "sample_rate": 24000,
 "characters": 38, "cached": false}
```

`ErrNotConfigured` when there is no key or voice; `ErrInvalid` for text that cannot be spoken (empty,
or over 600 characters); any other error is the vendor refusing or unreachable — the asker has the
words and decides what to do without the audio. The cost is charged to this plugin: Cartesia bills
characters, reported as `input_tokens` under the job `speak`; a cached line costs nothing.

## Asking it, as a client

```
POST /plugin/voice/speak
authorization: Bearer <device token>
{"text": "Pit this lap."}
```

The answer is the audio itself — `Content-Type: audio/mpeg` or `audio/wav` — with `X-Voice-Cached`,
`X-Voice-Container` and `X-Voice-Sample-Rate` headers. `400` for text that cannot be spoken, `401`
without a driver, `503` when the operator has set no key or voice, `502` when the vendor would not
answer: speak your own fallback.

## Its page

`/plugin/voice/` (an administrator): every line kept, what it cost in characters, and how many times
each was served again for nothing.

## Testing it

`make` runs what CI runs; `make test-postgres` and `make cover` need `PACENOTE_TEST_DATABASE_URL`.
The vendor is a fake in every test; what is tested is what it is asked, what a caller gets back, what
is refused, and that the same words are never paid for twice.

## Licence

GNU General Public License, version 3 — see `LICENSE`. Like the server and engineer. The plugin
contract it is built on (`github.com/pacenote-sim/plugin`) stays Apache-2.0.
