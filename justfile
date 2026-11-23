# seabird-rcon-plugin task runner.

# Show the list of recipes
default:
    @just --list

# Build the binary into tmp/
[group('build')]
build:
    mkdir -p tmp
    CGO_ENABLED=0 go build -o tmp/seabird-rcon-plugin ./cmd/seabird-rcon-plugin

# Remove build artifacts
[group('build')]
clean:
    rm -rf tmp/

# Run all tests
[group('check')]
test:
    CGO_ENABLED=0 go test ./...

# Run linting checks
[group('check')]
lint:
    golangci-lint run

# Format all code (pass --ci to check only)
[group('check')]
fmt *args:
    treefmt {{ args }}

# Run CI's checks before committing
[group('check')]
check: (fmt "--ci") lint test

# Run the plugin from source; reads .env and config.toml if present
[group('dev')]
run *args:
    go run ./cmd/seabird-rcon-plugin {{ args }}
