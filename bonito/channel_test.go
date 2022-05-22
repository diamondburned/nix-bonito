package bonito

import (
	"net/http"
	"testing"

	"github.com/hexops/autogold"
)

func TestResolveGit(t *testing.T) {
	do := func(inURL string, want autogold.Value) {
		t.Run(want.Name(), func(t *testing.T) {
			t.Parallel()

			input, err := ParseChannelInput(inURL)
			if err != nil {
				t.Fatal("cannot parse channel input:", err)
			}

			resolvedURL, err := input.Resolve()
			if err != nil {
				t.Fatalf("cannot resolve %q: %v", input.URL, err)
			}

			want.Equal(t, resolvedURL)

			if !testing.Short() {
				r, err := http.Get(resolvedURL)
				if err != nil {
					t.Fatal("cannot GET resolved URL:", err)
				}
				r.Body.Close()

				if r.StatusCode != 200 {
					t.Fatalf("unexpected status %d while GET %q", r.StatusCode, resolvedURL)
				}
			}
		})
	}

	do("https://github.com/NixOS/nixpkgs/archive/1ffba9f.tar.gz",
		autogold.Want("https", "https://github.com/NixOS/nixpkgs/archive/1ffba9f.tar.gz"))
	do("git://github.com/NixOS/nixpkgs 1ffba9f",
		autogold.Want("github-short-rev", "https://github.com/NixOS/nixpkgs/archive/1ffba9f.tar.gz"))
	do("git://github.com/NixOS/nixpkgs 1ffba9f2f683063c2b14c9f4d12c55ad5f4ed887",
		autogold.Want("github-long-rev", "https://github.com/NixOS/nixpkgs/archive/1ffba9f2f683063c2b14c9f4d12c55ad5f4ed887.tar.gz"))
	do("github:NixOS/nixpkgs 1ffba9f",
		autogold.Want("github-short-rev-2", "https://github.com/NixOS/nixpkgs/archive/1ffba9f.tar.gz"))
	do("git://gitlab.com/diamondburned/dotfiles a9bb5c0",
		autogold.Want("gitlab-short-rev", "https://gitlab.com/diamondburned/dotfiles/-/archive/a9bb5c0/dotfiles-a9bb5c0.tar.gz"))
	do("gitlab:diamondburned/dotfiles a9bb5c0",
		autogold.Want("gitlab-short-rev-2", "https://gitlab.com/diamondburned/dotfiles/-/archive/a9bb5c0/dotfiles-a9bb5c0.tar.gz"))
}
