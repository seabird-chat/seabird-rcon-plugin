package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/belak/x/slogx"
	"github.com/seabird-chat/seabird-go/pb"
)

const defaultRestartDelay = time.Minute

// timing holds the restart flow's polling intervals and limits, so tests can
// shrink them.
type timing struct {
	// playerPoll is how often a countdown checks whether everyone left.
	playerPoll time.Duration

	// statePoll is how often to check whether the server went down or came
	// back.
	statePoll time.Duration

	downTimeout time.Duration
	upTimeout   time.Duration
}

var defaultTiming = timing{
	playerPoll:  5 * time.Second,
	statePoll:   10 * time.Second,
	downTimeout: 2 * time.Minute,
	upTimeout:   15 * time.Minute,
}

// announcements are the countdown points at which players get a warning.
var announcements = []time.Duration{
	30 * time.Minute, 15 * time.Minute, 10 * time.Minute, 5 * time.Minute,
	time.Minute, 30 * time.Second, 10 * time.Second, 5 * time.Second,
}

var errCancelled = errors.New("restart cancelled")

type pendingRestart struct {
	requester string
	cancel    context.CancelCauseFunc

	// stopping is set once stop has been sent and the restart can no longer
	// be cancelled.
	stopping bool
}

func (p *Plugin) restart(ctx context.Context, src *pb.ChannelSource, arg string) {
	s, rest, ok := p.pickServer(src, arg)
	if !ok {
		p.reply(ctx, src, "%s", p.whichServer())
		return
	}
	if !s.isOperator(src) {
		p.reply(ctx, src, "You need to be an operator on %s to do that.", s.Name)
		return
	}

	delay := defaultRestartDelay
	switch rest {
	case "":
	case "cancel":
		p.cancelRestart(ctx, src, s)
		return
	case "now":
		delay = 0
	default:
		d, err := time.ParseDuration(rest)
		if err != nil || d < 0 || d > maxRestartDelay {
			p.reply(ctx, src, "Usage: !mc restart [server] [now|<delay>|cancel], with a delay like 90s or 5m, up to %s.",
				formatDuration(maxRestartDelay))
			return
		}
		delay = d
	}

	requester := src.GetUser().GetDisplayName()

	s.mu.Lock()
	if s.restart != nil {
		pending := *s.restart
		s.mu.Unlock()
		if pending.stopping {
			p.reply(ctx, src, "%s is already restarting (requested by %s).", s.Name, pending.requester)
		} else {
			p.reply(ctx, src, "A restart of %s is already pending (requested by %s).", s.Name, pending.requester)
		}
		return
	}

	rctx, cancel := context.WithCancelCause(ctx)
	pending := &pendingRestart{requester: requester, cancel: cancel}
	s.restart = pending
	s.mu.Unlock()

	p.logger.Info("restart requested",
		slogx.String("server", s.Name),
		slogx.String("user", src.GetUser().GetId()),
		slogx.Duration("delay", delay))

	p.wg.Go(func() {
		defer func() {
			cancel(nil)
			s.mu.Lock()
			if s.restart == pending {
				s.restart = nil
			}
			s.mu.Unlock()
		}()

		p.runRestart(rctx, src, s, pending, delay)
	})
}

func (p *Plugin) cancelRestart(ctx context.Context, src *pb.ChannelSource, s *server) {
	s.mu.Lock()
	pending := s.restart
	switch {
	case pending == nil:
		s.mu.Unlock()
		p.reply(ctx, src, "No restart of %s is pending.", s.Name)
		return
	case pending.stopping:
		s.mu.Unlock()
		p.reply(ctx, src, "Too late, %s is already stopping.", s.Name)
		return
	}
	pending.cancel(errCancelled)
	s.restart = nil
	s.mu.Unlock()

	name := src.GetUser().GetDisplayName()
	p.logger.Info("restart cancelled",
		slogx.String("server", s.Name),
		slogx.String("user", src.GetUser().GetId()))

	p.say(ctx, s, fmt.Sprintf("Restart cancelled by %s", name))
	p.reply(ctx, src, "Cancelled the %s restart.", s.Name)
}

// runRestart warns players, stops the server, and reports when it's back.
// systemd brings the server back up after stop.
func (p *Plugin) runRestart(ctx context.Context, src *pb.ChannelSource, s *server, pending *pendingRestart, delay time.Duration) {
	players, err := p.listPlayers(ctx, s)
	switch {
	case errors.Is(err, errUnparsed):
		// The server answered, so assume someone might be on.
		players.Online = -1
	case err != nil:
		p.reply(ctx, src, "%s isn't answering RCON, so I can't restart it.", s.Name)
		return
	}

	switch {
	case delay == 0:
		p.reply(ctx, src, "Restarting %s now.", s.Name)
	case players.Online == 0:
		p.reply(ctx, src, "Nobody is on %s, restarting now.", s.Name)
	default:
		p.reply(ctx, src, "Restarting %s in %s. Use !mc restart cancel to abort.", s.Name, formatDuration(delay))
		if !p.countdown(ctx, src, s, pending.requester, delay) {
			return
		}
	}

	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	pending.stopping = true
	s.mu.Unlock()

	start := time.Now()

	// The server can drop the connection before answering stop, so an error
	// here doesn't mean it stayed up. Waiting for it to go down tells.
	if _, err := s.Console.Exec(ctx, "stop"); err != nil {
		p.logger.Debug("stop returned an error", slogx.String("server", s.Name), slogx.Err(err))
	}

	if !p.waitFor(ctx, s, false, p.timing.downTimeout) {
		if ctx.Err() == nil {
			p.reply(ctx, src, "Sent stop to %s, but it's still answering after %s.", s.Name, formatElapsed(time.Since(start)))
		}
		return
	}

	if !p.waitFor(ctx, s, true, p.timing.upTimeout) {
		if ctx.Err() == nil {
			p.reply(ctx, src, "%s hasn't come back after %s. Someone should check on it.", s.Name, formatElapsed(time.Since(start)))
		}
		return
	}

	p.logger.Info("restart finished", slogx.String("server", s.Name), slogx.Duration("elapsed", time.Since(start)))
	p.reply(ctx, src, "%s is back up after %s.", s.Name, formatElapsed(time.Since(start)))
}

// countdown warns players until the delay runs out, and cuts it short if
// everyone leaves. It returns false if the restart was cancelled.
func (p *Plugin) countdown(ctx context.Context, src *pb.ChannelSource, s *server, requester string, delay time.Duration) bool {
	deadline := time.Now().Add(delay)
	p.say(ctx, s, fmt.Sprintf("Server restarting in %s (requested by %s)", formatDuration(delay), requester))

	poll := time.NewTicker(p.timing.playerPoll)
	defer poll.Stop()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true
		}

		next := nextAnnouncement(remaining)
		timer := time.NewTimer(remaining - next)

		select {
		case <-ctx.Done():
			timer.Stop()
			return false

		case <-poll.C:
			timer.Stop()
			players, err := p.listPlayers(ctx, s)
			if err == nil && players.Online == 0 {
				p.reply(ctx, src, "Everyone left %s, restarting now.", s.Name)
				return ctx.Err() == nil
			}

		case <-timer.C:
			if next > 0 {
				p.say(ctx, s, fmt.Sprintf("Server restarting in %s", formatDuration(next)))
			}
		}
	}
}

// nextAnnouncement returns the next warning point below remaining, or 0 if
// there isn't one.
func nextAnnouncement(remaining time.Duration) time.Duration {
	for _, a := range announcements {
		if a < remaining {
			return a
		}
	}
	return 0
}

// waitFor polls until the server's RCON answers (up) or stops answering
// (down), and reports whether that happened before the timeout.
func (p *Plugin) waitFor(ctx context.Context, s *server, up bool, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(p.timing.statePoll)
	defer ticker.Stop()

	for {
		_, err := s.Console.Exec(ctx, "list")
		if (err == nil) == up && ctx.Err() == nil {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (p *Plugin) say(ctx context.Context, s *server, msg string) {
	if _, err := s.Console.Exec(ctx, "say "+msg); err != nil {
		p.logger.Warn("failed to warn players", slogx.String("server", s.Name), slogx.Err(err))
	}
}
