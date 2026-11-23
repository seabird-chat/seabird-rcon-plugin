{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };
  };

  outputs =
    inputs@{
      flake-parts,
      nixpkgs,
      ...
    }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = nixpkgs.lib.systems.flakeExposed;

      perSystem =
        { pkgs, ... }:
        let
          seabird-rcon-plugin = pkgs.buildGoModule rec {
            pname = "seabird-rcon-plugin";
            version = "0.1.1-dev";
            src = ./.;
            vendorHash = "sha256-OXhq9WQ/US+o6v/cXrZWgmnIpvdhnO7PHJf+cCJT/VI=";
            subPackages = [ "cmd/${pname}" ];
            ldflags = [
              "-s"
              "-w"
            ];
            env.CGO_ENABLED = 0;
          };
        in
        {
          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
              golangci-lint
              just
              treefmt
              nixfmt
            ];
          };

          packages = {
            default = seabird-rcon-plugin;
            inherit seabird-rcon-plugin;
          };
        };
    };
}
