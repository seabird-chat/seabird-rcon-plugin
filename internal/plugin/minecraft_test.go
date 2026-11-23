package plugin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Players
	}{
		{
			name: "empty",
			in:   "There are 0 of a max of 20 players online: ",
			want: Players{Online: 0, Max: 20},
		},
		{
			name: "players",
			in:   "There are 2 of a max of 20 players online: belak, jsvana",
			want: Players{Online: 2, Max: 20, Names: []string{"belak", "jsvana"}},
		},
		{
			name: "old format",
			in:   "There are 1/10 players online:belak",
			want: Players{Online: 1, Max: 10, Names: []string{"belak"}},
		},
		{
			name: "formatting codes",
			in:   "There are §c1§r of a max of §c20§r players online: §6belak§r",
			want: Players{Online: 1, Max: 20, Names: []string{"belak"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseList(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	_, err := parseList("Unknown command")
	assert.Error(t, err)
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "1 minute", formatDuration(time.Minute))
	assert.Equal(t, "5 minutes", formatDuration(5*time.Minute))
	assert.Equal(t, "90 seconds", formatDuration(90*time.Second))
	assert.Equal(t, "1 second", formatDuration(time.Second))
}

func TestFormatElapsed(t *testing.T) {
	assert.Equal(t, "42s", formatElapsed(42*time.Second))
	assert.Equal(t, "3m05s", formatElapsed(3*time.Minute+5*time.Second))
}

func TestNextAnnouncement(t *testing.T) {
	assert.Equal(t, 30*time.Second, nextAnnouncement(time.Minute))
	assert.Equal(t, 10*time.Second, nextAnnouncement(30*time.Second))
	assert.Equal(t, time.Minute, nextAnnouncement(90*time.Second))
	assert.Equal(t, time.Duration(0), nextAnnouncement(5*time.Second))
}
