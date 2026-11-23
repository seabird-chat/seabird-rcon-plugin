package plugin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Players is the parsed output of the list command.
type Players struct {
	Online int
	Max    int
	Names  []string
}

var (
	// Modern servers say "There are 1 of a max of 20 players online: belak",
	// older ones "There are 1/20 players online:belak".
	listRe = regexp.MustCompile(`^There are (\d+)(?: of a max of |/)(\d+) players online:\s*(.*)$`)

	// formatRe matches Minecraft's section-sign formatting codes.
	formatRe = regexp.MustCompile(`§.`)

	playerNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
)

func parseList(out string) (Players, error) {
	m := listRe.FindStringSubmatch(strings.TrimSpace(stripFormatting(out)))
	if m == nil {
		return Players{}, fmt.Errorf("unexpected list output %q", out)
	}

	online, _ := strconv.Atoi(m[1])
	maxPlayers, _ := strconv.Atoi(m[2])

	var names []string
	for name := range strings.SplitSeq(m[3], ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}

	return Players{Online: online, Max: maxPlayers, Names: names}, nil
}

func stripFormatting(s string) string {
	return formatRe.ReplaceAllString(s, "")
}

// formatDuration renders whole minutes as minutes and everything else as
// seconds, which covers the delays people actually type.
func formatDuration(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return plural(int(d/time.Minute), "minute")
	}
	return plural(int(d.Round(time.Second)/time.Second), "second")
}

// formatElapsed renders a duration as minutes and seconds.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return fmt.Sprintf("%dm%02ds", d/time.Minute, (d%time.Minute)/time.Second)
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
