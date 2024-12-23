{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            go-tools
          ];
        };

        packages.default = pkgs.buildGoModule {
          pname = "nix-bonito";
          version = self.rev or "unknown";
          src = self;
          doCheck = false;
          meta.mainProgram = "bonito";

          vendorHash = "sha256-ZFYcxa85vvI6w1NMHfgyqfKdyiBtCUYUqU9c+APCFZ8=";
        };
      }
    );
}
