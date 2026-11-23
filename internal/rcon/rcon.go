// Package rcon is a minimal client for the Minecraft RCON protocol.
package rcon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Packet types. Minecraft answers a command with typeResponse and a login
// with typeCommand, which the protocol reuses as the auth response type.
const (
	typeResponse = 0
	typeCommand  = 2
	typeLogin    = 3
)

const (
	idLogin   = 1
	idCommand = 2

	// maxCommandLen is the largest command body Minecraft accepts.
	maxCommandLen = 1446

	// chunkLen is the number of characters Minecraft puts in each response
	// packet. A full chunk may be followed by more.
	chunkLen = 4096

	// maxPacketLen bounds incoming packets, leaving room for a chunk of
	// multi-byte characters.
	maxPacketLen = 1 << 16

	// continuationWait bounds the wait for another chunk after a full one,
	// since a response that's an exact multiple of chunkLen has no marker
	// at the end.
	continuationWait = 500 * time.Millisecond

	defaultTimeout = 10 * time.Second
)

// ErrAuth is returned when the server rejects the password.
var ErrAuth = errors.New("rcon: authentication failed")

// Client runs commands on a Minecraft server over RCON. Each Exec opens its
// own connection, so a restarted server never leaves a stale one behind.
type Client struct {
	Address  string
	Password string

	// Timeout bounds a whole Exec call. Zero means 10 seconds.
	Timeout time.Duration
}

// Exec logs in, runs one command, and returns the server's response.
func (c *Client) Exec(ctx context.Context, command string) (string, error) {
	if len(command) > maxCommandLen {
		return "", fmt.Errorf("rcon: command is %d bytes, limit is %d", len(command), maxCommandLen)
	}

	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return "", fmt.Errorf("rcon: %w", err)
	}
	defer conn.Close()

	// Unblock any pending read or write once the context is done.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	r := bufio.NewReader(conn)

	if err := writePacket(conn, packet{ID: idLogin, Type: typeLogin, Body: c.Password}); err != nil {
		return "", wrapErr(ctx, "login", err)
	}

	for {
		p, err := readPacket(r)
		if err != nil {
			return "", wrapErr(ctx, "login", err)
		}
		if p.Type != typeCommand {
			continue
		}
		if p.ID == -1 {
			return "", ErrAuth
		}
		if p.ID != idLogin {
			return "", fmt.Errorf("rcon: login: unexpected response id %d", p.ID)
		}
		break
	}

	// Minecraft reads one packet per socket read and hangs up if two arrive
	// together, so nothing else can be sent until the response is in. That
	// rules out the usual trick of following a command with a marker packet.
	if err := writePacket(conn, packet{ID: idCommand, Type: typeCommand, Body: command}); err != nil {
		return "", wrapErr(ctx, "command", err)
	}

	var out strings.Builder
	for {
		p, err := readPacket(r)
		if err != nil {
			if out.Len() > 0 && errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() == nil {
				return out.String(), nil
			}
			return "", wrapErr(ctx, "command", err)
		}
		if p.ID != idCommand {
			return "", fmt.Errorf("rcon: command: unexpected response id %d", p.ID)
		}

		out.WriteString(p.Body)
		if utf8.RuneCountInString(p.Body) < chunkLen {
			return out.String(), nil
		}

		if err := conn.SetReadDeadline(time.Now().Add(continuationWait)); err != nil {
			return "", wrapErr(ctx, "command", err)
		}
	}
}

// wrapErr reports a context error in place of the deadline error it caused.
func wrapErr(ctx context.Context, stage string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return fmt.Errorf("rcon: %s: %w", stage, err)
}

type packet struct {
	ID   int32
	Type int32
	Body string
}

// writePacket writes a length-prefixed packet: id, type, null-terminated
// body, and one null of padding, all little-endian.
func writePacket(w io.Writer, p packet) error {
	buf := make([]byte, 0, 14+len(p.Body))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(10+len(p.Body)))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(p.ID))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(p.Type))
	buf = append(buf, p.Body...)
	buf = append(buf, 0, 0)

	_, err := w.Write(buf)
	return err
}

func readPacket(r io.Reader) (packet, error) {
	var length int32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return packet{}, err
	}
	if length < 10 || length > maxPacketLen {
		return packet{}, fmt.Errorf("invalid packet length %d", length)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return packet{}, err
	}

	return packet{
		ID:   int32(binary.LittleEndian.Uint32(buf[0:4])),
		Type: int32(binary.LittleEndian.Uint32(buf[4:8])),
		Body: string(buf[8 : length-2]),
	}, nil
}
