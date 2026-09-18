package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pacenote-sim/plugin"
)

// The two addresses: a client posts words and gets audio; an administrator
// reads what has been spoken and what it cost.

// The host asks whether a plugin serves by asserting this interface, so a
// method that drifts from it is every route answering "this plugin does not
// serve a route" at runtime rather than a compile error.
var _ plugin.Server = (*Voice)(nil)

// ServeHTTP answers the routes the manifest declares.
func (v *Voice) ServeHTTP(ctx context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	switch {
	case r.Path == "/speak" && r.Method == http.MethodPost:
		return v.postSpeak(ctx, r)
	case r.Path == "/" && r.Method == http.MethodGet:
		return v.pageLines(ctx)
	default:
		return plugin.Text(http.StatusNotFound, "There is nothing at that address."), nil
	}
}

// postSpeak is POST /speak: a [SpeakRequest] in, the audio itself out — not
// JSON, the bytes, with their content type — so a client plays what it gets.
// The cost is on the response.
func (v *Voice) postSpeak(ctx context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	if !r.Caller.SignedIn() {
		return plugin.Text(http.StatusUnauthorized, "Only a signed-in driver's client can ask for speech."), nil
	}
	cfg := configOf(r.Settings, r.Secrets)
	if !cfg.configured() {
		return plugin.Text(http.StatusServiceUnavailable, "The operator has not set a Cartesia key and a voice, so nothing can be spoken."), nil
	}
	var req SpeakRequest
	if err := json.Unmarshal(orEmptyObject(r.Body), &req); err != nil {
		return plugin.Text(http.StatusBadRequest, "That is not a speak request: "+err.Error()), nil
	}
	out, use, err := v.speak(ctx, cfg, req)
	switch {
	case errors.Is(err, errBadText):
		return plugin.Text(http.StatusBadRequest, err.Error()), nil
	case err != nil:
		res := plugin.Text(http.StatusBadGateway, "The line could not be spoken.")
		res.Usage = use
		return res, nil
	}
	return plugin.HTTPResponse{
		Status: http.StatusOK,
		Header: http.Header{
			"Content-Type":        {out.ContentType},
			"X-Voice-Cached":      {strconv.FormatBool(out.Cached)},
			"X-Voice-Container":   {out.Container},
			"X-Voice-Sample-Rate": {strconv.Itoa(out.SampleRate)},
		},
		Body:  out.Audio,
		Usage: use,
	}, nil
}

// pageLines is the operator's: what has been spoken lately, and how often each
// line was served again for nothing.
func (v *Voice) pageLines(ctx context.Context) (plugin.HTTPResponse, error) {
	if v.Store == nil {
		return plugin.HTML(http.StatusOK, page("Spoken lines",
			`  <div class="card"><p class="muted">This plugin has no database, so every line is paid for and nothing is kept.</p></div>`)), nil
	}
	clips, err := v.Store.Recent(ctx, 50)
	if err != nil {
		return plugin.HTML(http.StatusBadGateway, page("Spoken lines",
			`  <div class="card"><p class="warn">The spoken lines could not be read just now.</p></div>`)), nil
	}
	totals, err := v.Store.Totals(ctx)
	if err != nil {
		return plugin.HTML(http.StatusBadGateway, page("Spoken lines",
			`  <div class="card"><p class="warn">The spoken lines could not be counted just now.</p></div>`)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, `  <div class="card">
    <p class="muted">Every line this plugin has spoken and kept. A line asked for again in the same voice
is served from here and costs nothing.</p>
    <table class="facts rows">
      <tr><th>Lines kept</th><td class="num">%d</td></tr>
      <tr><th>Characters paid for</th><td class="num">%d</td></tr>
      <tr><th>Served again for nothing</th><td class="num">%d</td></tr>
    </table>
  </div>
`, totals.Clips, totals.Characters, totals.Hits)
	if len(clips) == 0 {
		b.WriteString(`  <div class="card">
    <h2>Nothing yet</h2>
    <p class="muted">A line is spoken when a plugin or a client asks for one.</p>
  </div>
`)
	} else {
		b.WriteString(`  <div class="card">
    <table class="facts rows">
`)
		for i := range clips {
			c := &clips[i]
			fmt.Fprintf(&b, `      <tr><th>%s</th><td>%s<div class="drill">%d characters · %s · %s · served %d more time(s) · %d KB</div></td></tr>
`,
				esc(c.CreatedAt.Format(time.RFC822)), esc(c.Text), c.Characters, esc(c.VoiceID), esc(c.Container), c.Hits, len(c.Audio)/1024)
		}
		b.WriteString(`    </table>
  </div>
`)
	}
	return plugin.HTML(http.StatusOK, page("Spoken lines", b.String())), nil
}

// page wraps a body in the server's own shell, the way engineer does: the
// host's stylesheet and classes, no navigation of its own.
func page(title, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s — voice</title>
<link rel="stylesheet" href="/assets/app.css">
<style>
 .drill { color: var(--muted); }
 .card + .card { margin-top: 14px; }
</style>
</head>
<body>
<div class="wrap wide">
  <header class="top">
    <span class="name">voice</span>
    <span class="wm">plugin</span>
    <span class="spacer"></span>
    <a class="ghost" href="/admin/plugins/voice">Back to the panel</a>
  </header>
  <h1>%s</h1>
`, esc(title), esc(title))
	b.WriteString(body)
	b.WriteString(`
  <footer class="foot">Served by the voice plugin, not by the server.</footer>
</div>
</body>
</html>`)
	return b.String()
}

// esc is the one rule about anything that came from outside this binary.
func esc(s string) string { return html.EscapeString(s) }
