package voice

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// post is the client asking, with its device token already turned into a
// driver by the host.
func post(t *testing.T, v *Voice, body string, change ...func(*plugin.HTTPRequest)) plugin.HTTPResponse {
	t.Helper()
	r := plugin.HTTPRequest{
		Method: http.MethodPost, Path: "/speak", Body: []byte(body),
		Caller: plugin.Caller{DriverSlug: "mihai"}, Settings: settings(), Secrets: secrets(),
	}
	for _, c := range change {
		c(&r)
	}
	res, err := v.ServeHTTP(context.Background(), r)
	require.NoError(t, err)
	return res
}

func TestAClientGetsTheAudioItself(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	v, _, _ := vendorSaying(t, http.StatusOK, []byte("ID3audio"))
	res := post(t, v, `{"text":"Brake twenty metres later."}`)
	r.Equal(http.StatusOK, res.Status, string(res.Body))
	r.Equal([]byte("ID3audio"), res.Body, "the bytes, not JSON: a client plays what it gets")
	r.Equal("audio/mpeg", res.Header.Get("Content-Type"))
	r.Equal("false", res.Header.Get("X-Voice-Cached"))
	r.Equal("mp3", res.Header.Get("X-Voice-Container"))
	r.Equal("24000", res.Header.Get("X-Voice-Sample-Rate"))
	r.Equal("speak", res.Usage.Job)
	r.EqualValues(26, res.Usage.InputTokens)
}

func TestWhoMayAskForSpeechAndWithWhat(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	v, _, calls := vendorSaying(t, http.StatusOK, []byte("audio"))

	r.Equal(http.StatusUnauthorized, post(t, v, `{"text":"x"}`, func(q *plugin.HTTPRequest) { q.Caller = plugin.Caller{} }).Status)
	r.Equal(http.StatusServiceUnavailable, post(t, v, `{"text":"x"}`, func(q *plugin.HTTPRequest) { q.Secrets = nil }).Status)
	r.Equal(http.StatusBadRequest, post(t, v, `{not json`).Status)
	r.Equal(http.StatusBadRequest, post(t, v, `{"text":""}`).Status)
	r.Contains(string(post(t, v, `{"text":""}`).Body), "empty")
	r.Zero(*calls, "a refused request reached the vendor")

	res, err := v.ServeHTTP(context.Background(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/speak"})
	r.NoError(err)
	r.Equal(http.StatusNotFound, res.Status)
	res, err = v.ServeHTTP(context.Background(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/nowhere"})
	r.NoError(err)
	r.Equal(http.StatusNotFound, res.Status)
}

func TestAVendorFailureIsTheClientsToFallBackFrom(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	v, out, _ := vendorSaying(t, http.StatusUnauthorized, []byte(`{"error":"invalid api key"}`))
	res := post(t, v, `{"text":"Brake later."}`)
	r.Equal(http.StatusBadGateway, res.Status)
	r.Contains(string(res.Body), "could not be spoken")
	r.NotContains(string(res.Body), "invalid api key", "the vendor's words belong in the log, not the client")
	r.Contains(out.String(), "the vendor refused")
}

func TestTheOperatorsPageWithoutADatabase(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := New(nil, nil).ServeHTTP(context.Background(), plugin.HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.NoError(err)
	r.Equal(http.StatusOK, res.Status)
	r.Contains(string(res.Body), "no database")
	r.Contains(string(res.Body), "Spoken lines")

	// Anything from outside this binary is escaped on the way to a page.
	r.Equal("&lt;b&gt;", esc("<b>"))
	r.NotContains(page("<script>", "<i>body</i>"), "<script>")
}
