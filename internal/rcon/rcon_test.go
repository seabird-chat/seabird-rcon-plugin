package rcon

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer speaks enough RCON to test the client. handler supplies the
// response for each command.
type fakeServer struct {
	password string
	handler  func(cmd string) string

	// silent makes the server accept connections and never answer.
	silent bool
}

func (s *fakeServer) start(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()

	return ln.Addr().String()
}

func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()

	r := bufio.NewReader(conn)
	authed := false
	for {
		p, err := readPacket(r)
		if err != nil {
			return
		}
		if s.silent {
			continue
		}

		switch {
		case p.Type == typeLogin:
			id := p.ID
			if p.Body != s.password {
				id = -1
			}
			authed = id != -1
			_ = writePacket(conn, packet{ID: id, Type: typeCommand})
		case !authed:
			return
		case p.Type == typeCommand:
			// Like Minecraft, this splits on characters and sends nothing
			// extra after the last chunk.
			resp := []rune(s.handler(p.Body))
			for {
				n := min(len(resp), chunkLen)
				_ = writePacket(conn, packet{ID: p.ID, Type: typeResponse, Body: string(resp[:n])})
				resp = resp[n:]
				if len(resp) == 0 {
					break
				}
			}
		default:
			// Minecraft drops the connection on a packet it can't parse.
			return
		}
	}
}

func TestExec(t *testing.T) {
	long := strings.Repeat("abcdéfghij", 1000)
	exact := strings.Repeat("a", 2*chunkLen)

	srv := &fakeServer{
		password: "hunter2",
		handler: func(cmd string) string {
			switch cmd {
			case "list":
				return "There are 0 of a max of 20 players online: "
			case "long":
				return long
			case "exact":
				return exact
			default:
				return ""
			}
		},
	}
	addr := srv.start(t)

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "single packet", command: "list", want: "There are 0 of a max of 20 players online: "},
		{name: "fragmented", command: "long", want: long},
		{name: "exact multiple of chunk", command: "exact", want: exact},
		{name: "empty", command: "say hi", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{Address: addr, Password: "hunter2"}
			got, err := c.Exec(context.Background(), tt.command)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExecBadPassword(t *testing.T) {
	srv := &fakeServer{password: "hunter2", handler: func(string) string { return "" }}
	addr := srv.start(t)

	c := &Client{Address: addr, Password: "wrong"}
	_, err := c.Exec(context.Background(), "list")
	assert.ErrorIs(t, err, ErrAuth)
}

func TestExecCommandTooLong(t *testing.T) {
	c := &Client{Address: "127.0.0.1:1"}
	_, err := c.Exec(context.Background(), strings.Repeat("a", maxCommandLen+1))
	assert.ErrorContains(t, err, "limit is")
}

func TestExecTimeout(t *testing.T) {
	srv := &fakeServer{silent: true}
	addr := srv.start(t)

	c := &Client{Address: addr, Password: "hunter2", Timeout: 50 * time.Millisecond}
	_, err := c.Exec(context.Background(), "list")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	c := &Client{Address: addr, Password: "hunter2"}
	_, err = c.Exec(context.Background(), "list")
	assert.Error(t, err)
}
