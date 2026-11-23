package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(passwordFile, []byte("from-file\n"), 0o600))

	servers, err := Parse(`
[servers.zeta]
address = "localhost:25575"
password = "hunter2"

[servers.cozy-calzones]
address = "freya.elwert.dev:25575"
password_file = "` + passwordFile + `"
map_url = "https://mc-calzone.elwert.cloud"
channels = ["discord://seabird/1"]
operator_channels = ["discord://seabird/1"]
operators = ["discord://seabird/2"]
admins = ["discord://seabird/3"]
`)
	require.NoError(t, err)

	assert.Equal(t, []Server{
		{
			Name:             "cozy-calzones",
			Address:          "freya.elwert.dev:25575",
			Password:         "from-file",
			MapURL:           "https://mc-calzone.elwert.cloud",
			Channels:         []string{"discord://seabird/1"},
			OperatorChannels: []string{"discord://seabird/1"},
			Operators:        []string{"discord://seabird/2"},
			Admins:           []string{"discord://seabird/3"},
		},
		{
			Name:     "zeta",
			Address:  "localhost:25575",
			Password: "hunter2",
		},
	}, servers)
}

func TestParseErrors(t *testing.T) {
	emptyFile := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(emptyFile, []byte("\n"), 0o600))

	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "no servers",
			toml: ``,
			want: "no servers configured",
		},
		{
			name: "unknown key",
			toml: "[servers.a]\naddress = \"h:1\"\npassword = \"p\"\noperater = []",
			want: `unknown key "servers.a.operater"`,
		},
		{
			name: "both passwords",
			toml: "[servers.a]\naddress = \"h:1\"\npassword = \"p\"\npassword_file = \"/x\"",
			want: "set only one of password and password_file",
		},
		{
			name: "no password",
			toml: "[servers.a]\naddress = \"h:1\"",
			want: "one of password or password_file is required",
		},
		{
			name: "missing password file",
			toml: "[servers.a]\naddress = \"h:1\"\npassword_file = \"/does/not/exist\"",
			want: "password_file: open /does/not/exist",
		},
		{
			name: "empty password file",
			toml: "[servers.a]\naddress = \"h:1\"\npassword_file = \"" + emptyFile + "\"",
			want: "is empty",
		},
		{
			name: "address without port",
			toml: "[servers.a]\naddress = \"h\"\npassword = \"p\"",
			want: "address must be host:port",
		},
		{
			name: "bad name",
			toml: "[servers.Cozy]\naddress = \"h:1\"\npassword = \"p\"",
			want: "name must be lowercase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.toml)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}
