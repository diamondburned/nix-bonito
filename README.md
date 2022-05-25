# nix-bonito

`bonito` is a declarative Nix channels manager. It is written to replace some of
Flakes' features.

## Install

```sh
go install github.com/diamondburned/nix-bonito/cmd/bonito@latest
```

## Usage

`bonito` is meant to be used with a VCS such as Git. As a result, changes to the
configuration file and its checksum files should be committed so that they can
be rolled back using the VCS.

```sh
# Initialize and update with an existing config.
bonito # uses $HOSTNAME.toml, OR
bonito -c hackadoll3.toml

# Update channels that are referenced as branches (refs), such as
# "github:NixOS/nixpkgs nixos-unstable".
bonito -u
```

For an example configuration, see the [Example file](./example/hackadoll3.toml).

## Why not Flakes?

In case you don't know, Nix introduced an experimental feature named
[Flakes](https://nixos.wiki/wiki/Flakes), which effectively allows the user to
declaratively define all impure inputs in a `flake.nix` file, completely
replacing Nix channels. These impure inputs are then locked in a `flake.lock`
file.

The idea of locking input dependencies under a file isn't new: we know that Go
does it with `go.mod`, Rust does it with `Cargo.lock`, NodeJS does it with
`package-lock.json`, etc.

The reason why I wrote bonito instead of using Flakes is because Flakes tries
too hard to be a smart tool, which ends up making it very frustrating to use. A
list of quirks that I don't like about Flakes include:

- Flakes detects if you're in a Git repository and impose weird restrictions
  based on that. This makes using it a bit more frustrating.
- Flakes are still mostly experimental. At the time this was written, the CLI
  tools have lots of quirks, many of which makes it barely usable.
- Flakes caused an infinite recursion error on my config for very mysterious
  reasons. Writing `bonito` took me less time than debugging that error.
