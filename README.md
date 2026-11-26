# seabird-rcon-plugin

Or: Have you tried turning the calzone off and on again?

A seabird plugin that adds an `mc` command for checking on and restarting
Minecraft servers over RCON, Minecraft's remote console protocol. One
instance handles every server, because seabird core only lets one plugin
register a given command.

## Commands

```
!mc [status] [server]                        anyone
!mc players [server]                         anyone
!mc restart [server] [now|<delay>|cancel]    operators
!mc whitelist [server] add|remove <player>   operators
!mc whitelist [server] list                  operators
!mc rcon [server] <command>                  admins
```

The server name is optional. Without one, the plugin uses the server bound
to the channel the command came from, then the only server if there's just
one. A bare `!mc status` in a channel with no bound server reports every
server, since it can't do any harm.

`restart` sends the server `stop`, and relies on the service manager to
start it again (`Restart=always` under systemd). If nobody is online, or
with `now`, that happens right away. Otherwise players get an in-game
countdown, one minute by default or the given delay (like `90s` or `5m`, up
to 30 minutes), and the restart goes ahead early if everyone leaves.
`restart cancel` stops a countdown. After `stop`, the plugin watches for
the server to come back and says how long it took. A server that's hung
badly enough that RCON doesn't answer can't be restarted this way.

`rcon` runs any console command, with or without the leading `/`, and
replies with the output.

## Permissions

Permissions are per server. Operators can restart and manage the
whitelist. Admins can do that and also run raw commands, which includes
things like `op` and `gamemode`, so admin is only ever granted to specific
users. Operator access can go to users or to everyone talking in a
channel.

Users and channels are listed by the IDs seabird gives them, like
`sbu_5k2q8mzx7v1rc` for a user. `seabird-core user list` and
`seabird-core channel list` show them. On IRC a user is whoever has the
nickname, and nothing stops someone from taking another person's nickname, so
don't list IRC users.

## Configuration

Seabird settings come from the environment. A `.env` file in the working
directory is loaded too, if one exists.

| Variable | Default | Notes |
| --- | --- | --- |
| `SEABIRD_HOST` | required | seabird-core URL |
| `SEABIRD_TOKEN` | required | |
| `RCON_CONFIG` | `config.toml` | Path to the server list |
| `LOG_FORMAT` | `json` | `json`, `pretty`, or `text` |
| `LOG_LEVEL` | `info` | |

Servers live in the TOML file:

```toml
[servers.cozy-calzones]
address = "freya.elwert.dev:25575"
password_file = "/run/credentials/seabird-rcon-plugin.service/cozy-calzones"
map_url = "https://mc-calzone.elwert.cloud"

# Commands here default to this server.
channels = ["sbc_9w4hb2kd8xq3n"]

# Anyone talking here is an operator.
operator_channels = ["sbc_9w4hb2kd8xq3n"]

operators = []
admins = ["sbu_5k2q8mzx7v1rc"]
```

Each server needs exactly one of `password_file` or `password`. Use
`password_file` for anything real, so the password stays out of the config;
`password` is there for testing against a local server. Server names are
lowercase letters, digits, and dashes, and can't be a subcommand word like
`status` or `now`. Unknown keys are an error, so a typo can't quietly drop
a permission list.

RCON sends the password in plain text, so keep the port on a trusted
network.

## Development

`nix develop` provides the toolchain. `just check` runs formatting, lint,
and tests, the same as CI. `just run` starts the plugin from source using
`.env` and `config.toml`.
