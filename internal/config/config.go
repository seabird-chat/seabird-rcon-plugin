// Package config loads the plugin's TOML server list.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Server is one Minecraft server the plugin can reach over RCON.
type Server struct {
	Name     string
	Address  string
	Password string
	MapURL   string

	// Channels pick this server when a command doesn't name one.
	Channels []string

	// OperatorChannels grant operator access to anyone talking in them.
	OperatorChannels []string

	// Operators and Admins are seabird user IDs.
	Operators []string
	Admins    []string
}

type file struct {
	Servers map[string]serverFile `toml:"servers"`
}

type serverFile struct {
	Address          string   `toml:"address"`
	Password         string   `toml:"password"`
	PasswordFile     string   `toml:"password_file"`
	MapURL           string   `toml:"map_url"`
	Channels         []string `toml:"channels"`
	OperatorChannels []string `toml:"operator_channels"`
	Operators        []string `toml:"operators"`
	Admins           []string `toml:"admins"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Load reads and validates the config at path. Servers come back sorted by
// name.
func Load(path string) ([]Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return Parse(string(data))
}

// Parse validates a TOML config. Password files are read here, so a missing
// one fails at startup rather than on the first command.
func Parse(data string) ([]Server, error) {
	var f file
	md, err := toml.Decode(data, &f)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	var errs []error
	for _, key := range md.Undecoded() {
		errs = append(errs, fmt.Errorf("unknown key %q", key.String()))
	}

	if len(f.Servers) == 0 {
		errs = append(errs, errors.New("no servers configured"))
	}

	names := make([]string, 0, len(f.Servers))
	for name := range f.Servers {
		names = append(names, name)
	}
	slices.Sort(names)

	servers := make([]Server, 0, len(names))
	for _, name := range names {
		sf := f.Servers[name]
		fail := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("server %q: %s", name, fmt.Sprintf(format, args...)))
		}

		if !nameRe.MatchString(name) {
			fail("name must be lowercase letters, digits, and dashes")
		}

		if _, _, err := net.SplitHostPort(sf.Address); err != nil {
			fail("address must be host:port: %v", err)
		}

		password := sf.Password
		switch {
		case sf.Password != "" && sf.PasswordFile != "":
			fail("set only one of password and password_file")
		case sf.PasswordFile != "":
			raw, err := os.ReadFile(sf.PasswordFile)
			if err != nil {
				fail("password_file: %v", err)
			}
			password = strings.TrimSpace(string(raw))
			if err == nil && password == "" {
				fail("password_file %s is empty", sf.PasswordFile)
			}
		case sf.Password == "":
			fail("one of password or password_file is required")
		}

		servers = append(servers, Server{
			Name:             name,
			Address:          sf.Address,
			Password:         password,
			MapURL:           sf.MapURL,
			Channels:         sf.Channels,
			OperatorChannels: sf.OperatorChannels,
			Operators:        sf.Operators,
			Admins:           sf.Admins,
		})
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return servers, nil
}
