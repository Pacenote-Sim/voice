package voice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"

	"github.com/pacenote-sim/voice"
)

// The manifest, loaded the way the host loads it.
func TestTheHostCanLoadThisManifest(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	m, err := plugin.LoadManifest(".")
	r.NoError(err)
	r.NoError(m.Validate())
	r.Equal("voice", m.Name, "the kind is voice.speak, so the plugin is called voice — another vendor's build keeps the name")
	r.Equal("voice", m.Binary)
	r.Equal(plugin.InterfaceVersion, m.InterfaceVersion)

	r.Equal([]plugin.RequestKind{voice.KindSpeak}, m.Capabilities.Requests)
	r.Empty(m.Capabilities.Events, "it asks for no events")
	r.True(m.Capabilities.Network)
	r.Equal([]string{"api.cartesia.ai"}, m.Capabilities.Calls, "an operator is owed the name of what their data is sent to")
	r.True(m.Capabilities.Database, "it keeps spoken lines")
	r.Contains(m.Description, "Cartesia")

	for path, want := range map[string]plugin.Access{
		"/":      plugin.AccessAdmin,
		"/speak": plugin.AccessDriver,
	} {
		got, ok := m.Capabilities.HTTP.For(path)
		r.Truef(ok, "%s is served and not declared", path)
		r.Equalf(want, got, "%s is declared for the wrong caller", path)
	}
}
