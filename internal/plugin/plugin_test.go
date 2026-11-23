package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belak/x/slogx"
	"github.com/seabird-chat/seabird-go/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/seabird-chat/seabird-rcon-plugin/internal/config"
)

const (
	cozyChannel = "discord://seabird/cozy"
	otherChan   = "discord://seabird/other"
	admin       = "discord://seabird/admin"
	operator    = "discord://seabird/operator"
	stranger    = "discord://seabird/stranger"
)

var errDown = errors.New("connection refused")

// fakeConsole stands in for a Minecraft server. After stop it stays down for
// downFor list calls, then comes back up.
type fakeConsole struct {
	mu       sync.Mutex
	up       bool
	players  []string
	downFor  int
	commands []string
}

func (f *fakeConsole) Exec(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.commands = append(f.commands, cmd)

	if !f.up {
		if cmd != "list" || f.downFor > 0 {
			f.downFor--
			return "", errDown
		}
		f.up = true
	}

	switch {
	case cmd == "list":
		return fmt.Sprintf("There are %d of a max of 20 players online: %s",
			len(f.players), strings.Join(f.players, ", ")), nil
	case cmd == "stop":
		f.up = false
		return "Stopping the server", nil
	case strings.HasPrefix(cmd, "whitelist add "):
		return "Added " + strings.TrimPrefix(cmd, "whitelist add ") + " to the whitelist", nil
	default:
		return "", nil
	}
}

func (f *fakeConsole) setPlayers(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players = names
}

func (f *fakeConsole) ran(cmd string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if c == cmd {
			return true
		}
	}
	return false
}

func (f *fakeConsole) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.commands {
		if msg, ok := strings.CutPrefix(c, "say "); ok {
			out = append(out, msg)
		}
	}
	return out
}

type fakeChat struct {
	mu       sync.Mutex
	messages []string
}

func (c *fakeChat) Send(_ context.Context, channelID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, channelID+": "+text)
	return nil
}

func (c *fakeChat) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.messages...)
}

// waitFor blocks until a message containing substr shows up.
func (c *fakeChat) waitFor(t *testing.T, substr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, m := range c.all() {
			if strings.Contains(m, substr) {
				return true
			}
		}
		return false
	}, 2*time.Second, time.Millisecond, "no message containing %q, got %q", substr, c.all())
}

type harness struct {
	p    *Plugin
	chat *fakeChat
	cozy *fakeConsole
	test *fakeConsole
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		chat: &fakeChat{},
		cozy: &fakeConsole{up: true, downFor: 2},
		test: &fakeConsole{up: true},
	}

	p, err := New(h.chat, slogx.New(slogx.FormatText, slogx.LevelError), []Server{
		{
			Server: config.Server{
				Name:             "cozy-calzones",
				MapURL:           "https://map.example",
				Channels:         []string{cozyChannel},
				OperatorChannels: []string{cozyChannel},
				Operators:        []string{operator},
				Admins:           []string{admin},
			},
			Console: h.cozy,
		},
		{
			Server:  config.Server{Name: "test", Admins: []string{admin}},
			Console: h.test,
		},
	})
	require.NoError(t, err)

	p.timing = timing{
		playerPoll:  5 * time.Millisecond,
		statePoll:   5 * time.Millisecond,
		downTimeout: time.Second,
		upTimeout:   time.Second,
	}
	h.p = p

	return h
}

// run handles one command and waits for everything it started, including
// restarts.
func (h *harness) run(t *testing.T, channel, user, arg string) []string {
	t.Helper()
	h.start(channel, user, arg)
	h.p.wg.Wait()
	return h.chat.all()
}

func (h *harness) start(channel, user, arg string) {
	src := &pb.ChannelSource{
		ChannelId: channel,
		User:      &pb.User{Id: user, DisplayName: strings.TrimPrefix(user, "discord://seabird/")},
	}
	h.p.handle(context.Background(), src, arg)
}

func TestNewRejectsReservedNames(t *testing.T) {
	_, err := New(&fakeChat{}, nil, []Server{{Server: config.Server{Name: "status"}}})
	assert.ErrorContains(t, err, "reserved")
}

func TestNewRejectsChannelBoundTwice(t *testing.T) {
	_, err := New(&fakeChat{}, nil, []Server{
		{Server: config.Server{Name: "a", Channels: []string{"c"}}},
		{Server: config.Server{Name: "b", Channels: []string{"c"}}},
	})
	assert.ErrorContains(t, err, `channel c is already bound to "a"`)
}

func TestStatus(t *testing.T) {
	t.Run("bound channel", func(t *testing.T) {
		h := newHarness(t)
		h.cozy.setPlayers("belak")
		got := h.run(t, cozyChannel, stranger, "")
		assert.Equal(t, []string{
			cozyChannel + ": cozy-calzones is up with 1/20 players online: belak. Map: https://map.example",
		}, got)
	})

	t.Run("explicit server", func(t *testing.T) {
		h := newHarness(t)
		got := h.run(t, cozyChannel, stranger, "status test")
		assert.Equal(t, []string{cozyChannel + ": test is up with 0/20 players online."}, got)
	})

	t.Run("unbound channel lists all", func(t *testing.T) {
		h := newHarness(t)
		h.test.up, h.test.downFor = false, 1<<30
		got := h.run(t, otherChan, stranger, "status")
		assert.Equal(t, []string{
			otherChan + ": cozy-calzones is up with 0/20 players online. Map: https://map.example",
			otherChan + ": test is down or not answering.",
		}, got)
	})
}

func TestPlayers(t *testing.T) {
	h := newHarness(t)
	h.cozy.setPlayers("belak", "jsvana")
	assert.Equal(t, []string{cozyChannel + ": 2/20 online on cozy-calzones: belak, jsvana"},
		h.run(t, cozyChannel, stranger, "players"))

	h = newHarness(t)
	assert.Equal(t, []string{otherChan + ": Which server? cozy-calzones, test"},
		h.run(t, otherChan, stranger, "players"))
}

func TestPermissions(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		user    string
		arg     string
		want    string
	}{
		{"stranger outside channel", otherChan, stranger, "restart cozy-calzones", "need to be an operator"},
		{"operator channel", cozyChannel, stranger, "whitelist add belak", "Added belak"},
		{"operator user", otherChan, operator, "whitelist cozy-calzones add belak", "Added belak"},
		{"admin is operator", otherChan, admin, "whitelist cozy-calzones add belak", "Added belak"},
		{"operator can't rcon", cozyChannel, operator, "rcon time set day", "need to be an admin"},
		{"admin rcon", cozyChannel, admin, "rcon /time set day", "cozy-calzones: (no output)"},
		{"operators are per server", otherChan, operator, "whitelist test add belak", "need to be an operator on test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			got := h.run(t, tt.channel, tt.user, tt.arg)
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tt.want)
		})
	}
}

func TestRconStripsSlash(t *testing.T) {
	h := newHarness(t)
	h.run(t, cozyChannel, admin, "rcon /time set day")
	assert.True(t, h.cozy.ran("time set day"))
}

func TestWhitelistRejectsBadNames(t *testing.T) {
	h := newHarness(t)
	got := h.run(t, cozyChannel, operator, "whitelist add bad;name")
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "Usage")
	assert.False(t, h.cozy.ran("whitelist add bad;name"))
}

func TestRestartEmptyServerIsImmediate(t *testing.T) {
	h := newHarness(t)
	got := h.run(t, cozyChannel, operator, "restart")

	assert.True(t, h.cozy.ran("stop"))
	assert.Empty(t, h.cozy.said())
	require.Len(t, got, 2)
	assert.Equal(t, cozyChannel+": Nobody is on cozy-calzones, restarting now.", got[0])
	assert.Contains(t, got[1], "cozy-calzones is back up after")
}

func TestRestartNow(t *testing.T) {
	h := newHarness(t)
	h.cozy.setPlayers("belak")
	got := h.run(t, cozyChannel, operator, "restart now")

	assert.True(t, h.cozy.ran("stop"))
	assert.Equal(t, cozyChannel+": Restarting cozy-calzones now.", got[0])
	assert.Contains(t, got[len(got)-1], "back up")
}

func TestRestartWhenEveryoneLeaves(t *testing.T) {
	h := newHarness(t)
	h.cozy.setPlayers("belak")

	h.start(cozyChannel, operator, "restart")
	h.chat.waitFor(t, "Restarting cozy-calzones in 1 minute")
	assert.False(t, h.cozy.ran("stop"))

	h.cozy.setPlayers()
	h.p.wg.Wait()

	assert.True(t, h.cozy.ran("stop"))
	assert.Equal(t, []string{"Server restarting in 1 minute (requested by operator)"}, h.cozy.said())
	got := h.chat.all()
	assert.Contains(t, got, cozyChannel+": Everyone left cozy-calzones, restarting now.")
	assert.Contains(t, got[len(got)-1], "back up")
}

func TestRestartCountdownRunsOut(t *testing.T) {
	h := newHarness(t)
	h.cozy.setPlayers("belak")
	h.run(t, cozyChannel, operator, "restart cozy-calzones 6s")

	assert.True(t, h.cozy.ran("stop"))
	assert.Equal(t, []string{
		"Server restarting in 6 seconds (requested by operator)",
		"Server restarting in 5 seconds",
	}, h.cozy.said())
}

func TestRestartCancel(t *testing.T) {
	h := newHarness(t)
	h.cozy.setPlayers("belak")

	h.start(cozyChannel, operator, "restart")
	h.chat.waitFor(t, "Restarting cozy-calzones in")

	h.start(cozyChannel, stranger, "restart")
	h.chat.waitFor(t, "A restart of cozy-calzones is already pending (requested by operator).")

	h.start(cozyChannel, stranger, "restart cancel")
	h.p.wg.Wait()

	assert.False(t, h.cozy.ran("stop"))
	assert.Contains(t, h.cozy.said(), "Restart cancelled by stranger")
	assert.Contains(t, h.chat.all(), cozyChannel+": Cancelled the cozy-calzones restart.")

	got := h.run(t, cozyChannel, stranger, "restart cancel")
	assert.Equal(t, cozyChannel+": No restart of cozy-calzones is pending.", got[len(got)-1])
}

func TestRestartUnreachable(t *testing.T) {
	h := newHarness(t)
	h.cozy.up = false
	got := h.run(t, cozyChannel, operator, "restart")

	assert.Equal(t, []string{cozyChannel + ": cozy-calzones isn't answering RCON, so I can't restart it."}, got)
	assert.False(t, h.cozy.ran("stop"))
	assert.Nil(t, h.p.byName["cozy-calzones"].restart)
}

func TestRestartNeverComesBack(t *testing.T) {
	h := newHarness(t)
	h.cozy.downFor = 1 << 30
	h.p.timing.upTimeout = 20 * time.Millisecond

	got := h.run(t, cozyChannel, operator, "restart")
	assert.Contains(t, got[len(got)-1], "hasn't come back")
}

func TestRestartBadDelay(t *testing.T) {
	h := newHarness(t)
	got := h.run(t, cozyChannel, operator, "restart 2h")
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "Usage")
}

func TestRun(t *testing.T) {
	h := newHarness(t)
	events := make(chan *pb.Event, 2)
	events <- &pb.Event{Inner: &pb.Event_Command{Command: &pb.CommandEvent{
		Source:  &pb.ChannelSource{ChannelId: cozyChannel, User: &pb.User{Id: stranger}},
		Command: "other",
	}}}
	events <- &pb.Event{Inner: &pb.Event_Command{Command: &pb.CommandEvent{
		Source:  &pb.ChannelSource{ChannelId: cozyChannel, User: &pb.User{Id: stranger}},
		Command: "mc",
		Arg:     "players",
	}}}
	close(events)

	err := h.p.Run(context.Background(), events)
	assert.ErrorContains(t, err, "event stream closed")
	assert.Equal(t, []string{cozyChannel + ": Nobody is online on cozy-calzones."}, h.chat.all())
}
