// Command seabird-rcon-plugin adds an mc command to seabird for checking on
// and restarting Minecraft servers over RCON. Seabird connection settings
// come from environment variables and servers from a TOML file; see the
// README.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/belak/x/slogx"
	"github.com/joho/godotenv"
	"github.com/seabird-chat/seabird-go"

	"github.com/seabird-chat/seabird-rcon-plugin/internal/config"
	"github.com/seabird-chat/seabird-rcon-plugin/internal/plugin"
	"github.com/seabird-chat/seabird-rcon-plugin/internal/rcon"
)

func main() {
	// A .env file is optional.
	_ = godotenv.Load()

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var format slogx.Format
	if err := format.Set(envDefault("LOG_FORMAT", "json")); err != nil {
		return fmt.Errorf("LOG_FORMAT: %w", err)
	}
	var level slogx.Level
	if err := level.Set(envDefault("LOG_LEVEL", "info")); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	logger := slogx.New(format, level)

	host := os.Getenv("SEABIRD_HOST")
	token := os.Getenv("SEABIRD_TOKEN")
	if host == "" || token == "" {
		return errors.New("SEABIRD_HOST and SEABIRD_TOKEN are required")
	}

	cfgs, err := config.Load(envDefault("RCON_CONFIG", "config.toml"))
	if err != nil {
		return err
	}

	servers := make([]plugin.Server, 0, len(cfgs))
	for _, cfg := range cfgs {
		servers = append(servers, plugin.Server{
			Server:  cfg,
			Console: &rcon.Client{Address: cfg.Address, Password: cfg.Password},
		})
	}

	// Build the plugin first so config problems show up before connecting.
	chat := &plugin.SeabirdChat{}
	p, err := plugin.New(chat, logger, servers)
	if err != nil {
		return err
	}

	client, err := seabird.NewClient(host, token)
	if err != nil {
		return fmt.Errorf("seabird: %w", err)
	}
	defer client.Close()
	chat.Client = client

	events, err := client.StreamEvents(p.Commands())
	if err != nil {
		return fmt.Errorf("seabird: %w", err)
	}
	defer events.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting", slogx.Int("servers", len(servers)))
	err = p.Run(ctx, events.C)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func envDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
