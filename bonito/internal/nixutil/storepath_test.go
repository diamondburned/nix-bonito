package nixutil

import (
	"testing"

	"github.com/hexops/autogold"
)

func TestParseStorePath(t *testing.T) {
	tests := []struct {
		root string
		path string
		want autogold.Value
	}{
		{
			root: "/nix/store",
			path: "/nix/store/4ch3bm9bx98jf68ri8jmx00k479mv8g6-nixos/nixos",
			want: autogold.Want("nixos/nixos", StorePath{Root: "/nix/store", Name: "nixos", Hash: StoreHash("4ch3bm9bx98jf68ri8jmx00k479mv8g6")}),
		},
	}

	for _, test := range tests {
		t.Run(test.want.Name(), func(t *testing.T) {
			p, err := ParseStorePathWithRoot(test.root, test.path)
			if err != nil {
				t.Fatalf("error parsing %q: %v", test.path, err)
			}
			test.want.Equal(t, p)
		})
	}
}
