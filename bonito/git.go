package bonito

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/gitutil"
	"github.com/pkg/errors"
)

// TODO: figure out a better name.
var opaqueExpanders = map[string]func(*url.URL) error{
	"github":  commonOpaqueExpander("github.com"),
	"gitlab":  commonOpaqueExpander("gitlab.com"),
	"gitsrht": commonOpaqueExpander("git.sr.ht"),
}

// commonOpaqueExpander handles "x:user/repo" and "x:service.com/user/repo".
func commonOpaqueExpander(host string) func(*url.URL) error {
	return func(u *url.URL) error {
		parts := strings.Split(u.Opaque, "/")
		switch len(parts) {
		case 2:
			u.Host = host
		case 3:
			u.Host = parts[0]
			parts = parts[1:]
		default:
			return fmt.Errorf("invalid opaque %q", u.Opaque)
		}
		u.Path = strings.Join(parts, "/")
		return nil
	}
}

func resolveGit(ctx context.Context, in ChannelInput) (string, error) {
	u, err := in.URL.Parse()
	if err != nil {
		return "", err
	}

	var host string

	switch u.Scheme {
	case "git":
		host = u.Host
	case "github":
		host = "github.com"
	case "gitlab":
		host = "gitlab.com"
	case "gitsrht":
		host = "git.sr.ht"
	case "gitea":
		host = "gitea.com"
	default:
		return "", fmt.Errorf("unknown git service %q, consider using https://", u.Host)
	}

	if u.Opaque != "" {
		parts := strings.Split(u.Opaque, "/")
		switch len(parts) {
		case 2:
			u.Host = host
		case 3:
			u.Host = parts[0]
			parts = parts[1:]
		default:
			return "", fmt.Errorf("invalid opaque %q", u.Opaque)
		}

		u.Path = strings.Join(parts, "/")
		u.Host = host
		u.Opaque = ""
	}

	u.Scheme = "https"

	commit, err := gitutil.RefCommit(ctx, u.String(), in.Version)
	if err != nil {
		return "", errors.Wrap(err, "cannot get version")
	}

	if commit != "" {
		if strings.HasPrefix(commit, in.Version) {
			// If the version is part of the resolved commit hash, then we're
			// not updating anything. Warn about this.
			slog.Warn(
				"not updating git input as a commit is being used",
				"input", in)
		}

		// Found a commit associated to a ref. Use that as the version for our
		// URL.
		in.Version = commit
	}

	switch host {
	case "github.com":
		u.Path += "/archive/" + in.Version + ".tar.gz"
	case "gitlab.com":
		u.Path += fmt.Sprintf("/-/archive/%[1]s/%[2]s-%[1]s.tar.gz", in.Version, path.Base(u.Path))
	case "git.sr.ht":
		u.Path += "/archive/" + in.Version + ".tar.gz"
	case "gitea.com":
		u.Path += "/archive/" + in.Version + ".tar.gz"
	default:
		return "", fmt.Errorf("unknown git service %q, consider using https://", u.Host)
	}

	return u.String(), nil
}

func popHost(opaque string) (string, string) {
	parts := strings.SplitN(opaque, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}
