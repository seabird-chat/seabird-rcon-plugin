// Package plugin implements the mc command on top of RCON.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/belak/x/slogx"
	"github.com/seabird-chat/seabird-go/pb"

	"github.com/seabird-chat/seabird-rcon-plugin/internal/config"
)

// Console runs commands on a Minecraft server.
type Console interface {
	Exec(ctx context.Context, command string) (string, error)
}

// Chat sends messages to seabird channels.
type Chat interface {
	Send(ctx context.Context, channelID, text string) error
}

// Server pairs a configured server with the console used to reach it.
type Server struct {
	config.Server
	Console Console
}

// reserved holds words the parser treats as subcommands or arguments, so a
// server can't be named after one.
var reserved = []string{
	"status", "players", "restart", "whitelist", "rcon",
	"now", "cancel", "add", "remove", "list",
}

const (
	commandName = "mc"
	usage       = "Usage: !mc [status|players|restart|whitelist|rcon] [server] ..."

	maxRestartDelay = 30 * time.Minute
	maxOutputLen    = 1000
)

// Plugin handles the mc command.
type Plugin struct {
	chat    Chat
	logger  *slogx.Logger
	timing  timing
	servers []*server

	byName    map[string]*server
	byChannel map[string]*server

	wg sync.WaitGroup
}

type server struct {
	Server

	mu      sync.Mutex
	restart *pendingRestart
}

// New validates the servers and builds a plugin. Servers keep the order
// given.
func New(chat Chat, logger *slogx.Logger, servers []Server) (*Plugin, error) {
	p := &Plugin{
		chat:      chat,
		logger:    logger,
		timing:    defaultTiming,
		byName:    map[string]*server{},
		byChannel: map[string]*server{},
	}

	var errs []error
	for _, cfg := range servers {
		if slices.Contains(reserved, cfg.Name) {
			errs = append(errs, fmt.Errorf("server %q: name is reserved for a subcommand", cfg.Name))
			continue
		}
		if _, ok := p.byName[cfg.Name]; ok {
			errs = append(errs, fmt.Errorf("server %q: configured twice", cfg.Name))
			continue
		}

		s := &server{Server: cfg}
		p.servers = append(p.servers, s)
		p.byName[cfg.Name] = s
		for _, ch := range cfg.Channels {
			if other, ok := p.byChannel[ch]; ok {
				errs = append(errs, fmt.Errorf("server %q: channel %s is already bound to %q", cfg.Name, ch, other.Name))
				continue
			}
			p.byChannel[ch] = s
		}
	}

	if len(p.servers) == 0 && len(errs) == 0 {
		errs = append(errs, errors.New("no servers configured"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return p, nil
}

// Commands returns the metadata to register with seabird core.
func (p *Plugin) Commands() map[string]*pb.CommandMetadata {
	return map[string]*pb.CommandMetadata{
		commandName: {
			Name:      commandName,
			ShortHelp: "[status|players|restart|whitelist|rcon] [server] ...",
			FullHelp: "Minecraft server controls. status and players work for anyone; " +
				"restart [now|<delay>|cancel] and whitelist add|remove|list need an operator, " +
				"rcon <command> needs an admin. The server defaults to the one bound to the channel.",
		},
	}
}

// Run handles commands from events until ctx is done or events closes. It
// waits for in-flight commands and restarts to finish before returning.
func (p *Plugin) Run(ctx context.Context, events <-chan *pb.Event) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		p.wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return errors.New("event stream closed")
			}

			cmd := event.GetCommand()
			if cmd == nil || cmd.GetCommand() != commandName {
				continue
			}

			p.wg.Go(func() { p.handle(ctx, cmd.GetSource(), cmd.GetArg()) })
		}
	}
}

func (p *Plugin) handle(ctx context.Context, src *pb.ChannelSource, arg string) {
	sub, rest := nextWord(arg)
	if sub == "" {
		sub = "status"
	}

	switch sub {
	case "status":
		p.status(ctx, src, rest)
	case "players":
		p.players(ctx, src, rest)
	case "restart":
		p.restart(ctx, src, rest)
	case "whitelist":
		p.whitelist(ctx, src, rest)
	case "rcon":
		p.rcon(ctx, src, rest)
	default:
		p.reply(ctx, src, "%s", usage)
	}
}

func (p *Plugin) status(ctx context.Context, src *pb.ChannelSource, arg string) {
	s, _, ok := p.pickServer(src, arg)
	if !ok {
		// Status is harmless, so without a clear server it reports them all.
		for _, s := range p.servers {
			p.reply(ctx, src, "%s", p.statusLine(ctx, s))
		}
		return
	}

	p.reply(ctx, src, "%s", p.statusLine(ctx, s))
}

func (p *Plugin) statusLine(ctx context.Context, s *server) string {
	s.mu.Lock()
	pending := s.restart
	var requester string
	var stopping bool
	if pending != nil {
		requester, stopping = pending.requester, pending.stopping
	}
	s.mu.Unlock()

	if stopping {
		return fmt.Sprintf("%s is restarting (requested by %s).", s.Name, requester)
	}

	var b strings.Builder
	players, err := p.listPlayers(ctx, s)
	switch {
	case errors.Is(err, errUnparsed):
		fmt.Fprintf(&b, "%s is up.", s.Name)
	case err != nil:
		fmt.Fprintf(&b, "%s is down or not answering.", s.Name)
	default:
		fmt.Fprintf(&b, "%s is up with %d/%d players online", s.Name, players.Online, players.Max)
		if len(players.Names) > 0 {
			fmt.Fprintf(&b, ": %s", strings.Join(players.Names, ", "))
		}
		b.WriteString(".")
	}

	if pending != nil {
		fmt.Fprintf(&b, " A restart is pending (requested by %s).", requester)
	}
	if s.MapURL != "" {
		fmt.Fprintf(&b, " Map: %s", s.MapURL)
	}

	return b.String()
}

func (p *Plugin) players(ctx context.Context, src *pb.ChannelSource, arg string) {
	s, _, ok := p.pickServer(src, arg)
	if !ok {
		p.reply(ctx, src, "%s", p.whichServer())
		return
	}

	players, err := p.listPlayers(ctx, s)
	switch {
	case err != nil:
		p.reply(ctx, src, "%s is down or not answering.", s.Name)
	case players.Online == 0:
		p.reply(ctx, src, "Nobody is online on %s.", s.Name)
	default:
		p.reply(ctx, src, "%d/%d online on %s: %s", players.Online, players.Max, s.Name, strings.Join(players.Names, ", "))
	}
}

func (p *Plugin) whitelist(ctx context.Context, src *pb.ChannelSource, arg string) {
	s, rest, ok := p.pickServer(src, arg)
	if !ok {
		p.reply(ctx, src, "%s", p.whichServer())
		return
	}
	if !s.isOperator(src) {
		p.reply(ctx, src, "You need to be an operator on %s to do that.", s.Name)
		return
	}

	action, rest := nextWord(rest)
	player, rest := nextWord(rest)

	var cmd string
	switch {
	case action == "list" && player == "":
		cmd = "whitelist list"
	case (action == "add" || action == "remove") && playerNameRe.MatchString(player) && rest == "":
		cmd = "whitelist " + action + " " + player
	default:
		p.reply(ctx, src, "Usage: !mc whitelist [server] add|remove <player>, or !mc whitelist [server] list")
		return
	}

	p.logger.Info("running whitelist command",
		slogx.String("server", s.Name),
		slogx.String("user", src.GetUser().GetId()),
		slogx.String("command", cmd))

	p.execAndReply(ctx, src, s, cmd)
}

func (p *Plugin) rcon(ctx context.Context, src *pb.ChannelSource, arg string) {
	s, rest, ok := p.pickServer(src, arg)
	if !ok {
		p.reply(ctx, src, "%s", p.whichServer())
		return
	}
	if !s.isAdmin(src) {
		p.reply(ctx, src, "You need to be an admin on %s to do that.", s.Name)
		return
	}

	cmd := strings.TrimPrefix(rest, "/")
	if cmd == "" {
		p.reply(ctx, src, "Usage: !mc rcon [server] <command>")
		return
	}

	p.logger.Info("running raw command",
		slogx.String("server", s.Name),
		slogx.String("user", src.GetUser().GetId()),
		slogx.String("command", cmd))

	p.execAndReply(ctx, src, s, cmd)
}

func (p *Plugin) execAndReply(ctx context.Context, src *pb.ChannelSource, s *server, cmd string) {
	out, err := s.Console.Exec(ctx, cmd)
	if err != nil {
		p.logger.Warn("command failed", slogx.String("server", s.Name), slogx.Err(err))
		p.reply(ctx, src, "%s: command failed: %v", s.Name, err)
		return
	}

	out = strings.TrimSpace(stripFormatting(out))
	if out == "" {
		out = "(no output)"
	}
	if r := []rune(out); len(r) > maxOutputLen {
		out = string(r[:maxOutputLen]) + " (truncated)"
	}

	p.reply(ctx, src, "%s: %s", s.Name, out)
}

var errUnparsed = errors.New("unparsed list output")

func (p *Plugin) listPlayers(ctx context.Context, s *server) (Players, error) {
	out, err := s.Console.Exec(ctx, "list")
	if err != nil {
		p.logger.Debug("list failed", slogx.String("server", s.Name), slogx.Err(err))
		return Players{}, err
	}

	players, err := parseList(out)
	if err != nil {
		p.logger.Warn("could not parse list output", slogx.String("server", s.Name), slogx.Err(err))
		return Players{}, fmt.Errorf("%w: %w", errUnparsed, err)
	}

	return players, nil
}

// pickServer resolves the server for a command: an explicit name first, then
// the channel's bound server, then the only server configured. It returns
// the args left after any server name.
func (p *Plugin) pickServer(src *pb.ChannelSource, arg string) (*server, string, bool) {
	word, rest := nextWord(arg)
	if s, ok := p.byName[word]; ok {
		return s, rest, true
	}

	arg = strings.TrimSpace(arg)
	if s, ok := p.byChannel[src.GetChannelId()]; ok {
		return s, arg, true
	}
	if len(p.servers) == 1 {
		return p.servers[0], arg, true
	}

	return nil, arg, false
}

func (p *Plugin) whichServer() string {
	names := make([]string, 0, len(p.servers))
	for _, s := range p.servers {
		names = append(names, s.Name)
	}
	return "Which server? " + strings.Join(names, ", ")
}

func (p *Plugin) reply(ctx context.Context, src *pb.ChannelSource, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if err := p.chat.Send(ctx, src.GetChannelId(), text); err != nil {
		p.logger.Warn("failed to send reply",
			slogx.String("channel", src.GetChannelId()),
			slogx.Err(err))
	}
}

func (s *server) isAdmin(src *pb.ChannelSource) bool {
	return slices.Contains(s.Admins, src.GetUser().GetId())
}

func (s *server) isOperator(src *pb.ChannelSource) bool {
	return s.isAdmin(src) ||
		slices.Contains(s.Operators, src.GetUser().GetId()) ||
		slices.Contains(s.OperatorChannels, src.GetChannelId())
}

// nextWord splits off the first space-separated word and returns the trimmed
// remainder.
func nextWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	word, rest, _ := strings.Cut(s, " ")
	return word, strings.TrimSpace(rest)
}
