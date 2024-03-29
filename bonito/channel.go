package bonito

import (
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/diamondburned/nix-bonito/bonito/internal/executil"
	"github.com/pkg/errors"
)

// ChannelInput is the input declaration of a channel. It is marshaled to TOML
// as a string of two parts, the URL and the version, separated by a space.
type ChannelInput struct {
	// URL is the source URL of the channel.
	URL ChannelURL
	// Version is the respective version string corresponding to the VCS defined
	// in the channel URL. For example, if the VCS is Git, then the URL's scheme
	// might be git+https, and the version string would imply a branch name,
	// tag, or commit hash.
	//
	// If the Version string is empty, then it is not included in the marshaled
	// text at all.
	Version string
}

// ParseChannelInput parses the channel input string into ChannelInput.
func ParseChannelInput(chInput string) (ChannelInput, error) {
	var in ChannelInput
	if err := in.UnmarshalText([]byte(chInput)); err != nil {
		return ChannelInput{}, nil
	}
	return in, nil
}

// CanResolve returns true if the channel input's URL scheme is recognized.
func (in ChannelInput) CanResolve() bool {
	u, err := in.URL.Parse()
	if err != nil {
		return false
	}

	_, ok := ChannelResolvers[u.Scheme]
	return ok
}

// Resolve resolves the channel input using one of the ChannelResolvers.
func (in ChannelInput) Resolve(ctx context.Context) (string, error) {
	u, err := in.URL.Parse()
	if err != nil {
		return "", err
	}

	resolve, ok := ChannelResolvers[u.Scheme]
	if !ok {
		return "", fmt.Errorf("cannot resolve unknown scheme %q", u.Scheme)
	}

	return resolve(ctx, in)
}

// String returns the ChannelInput formatted as a string.
func (in ChannelInput) String() string {
	text := string(in.URL)
	if in.Version != "" {
		text += " " + in.Version
	}
	return text
}

var (
	_ encoding.TextMarshaler   = (*ChannelInput)(nil)
	_ encoding.TextUnmarshaler = (*ChannelInput)(nil)
)

func (in ChannelInput) MarshalText() ([]byte, error) {
	if err := in.URL.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid channel URL")
	}
	return []byte(in.String()), nil
}

func (in *ChannelInput) UnmarshalText(text []byte) error {
	parts := strings.SplitN(string(text), " ", 2)
	switch len(parts) {
	case 0:
		return errors.New("channel input string is empty")
	case 1:
		in.URL = ChannelURL(parts[0])
		in.Version = ""
	case 2:
		in.URL = ChannelURL(parts[0])
		in.Version = parts[1]
	}

	if err := in.URL.Validate(); err != nil {
		return errors.Wrap(err, "invalid channel URL")
	}

	return nil
}

var (
	_ encoding.TextMarshaler   = (*ChannelInput)(nil)
	_ encoding.TextUnmarshaler = (*ChannelInput)(nil)
)

func (in *ChannelInput) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	return in.UnmarshalText([]byte(str))
}

func (in ChannelInput) MarshalJSON() ([]byte, error) {
	s, err := in.MarshalText()
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// ChannelResolver is a function type that resolves a channel URL to the URL that's
// actually used for adding into nix-channel.
type ChannelResolver func(context.Context, ChannelInput) (string, error)

// ChannelResolvers maps URL schemes to resolvers.
var ChannelResolvers = map[string]ChannelResolver{
	"git":     resolveGit,
	"github":  resolveGit,
	"gitlab":  resolveGit,
	"gitsrht": resolveGit,
}

type channelExecer struct {
	ctx    context.Context
	prefix string
}

const channelPrefix = "bonito-"

func newChannelExecer(ctx context.Context, temp bool) *channelExecer {
	execer := channelExecer{ctx: ctx}
	if temp {
		execer.prefix = channelPrefix
	}
	return &execer
}

func (e *channelExecer) isTemp() bool { return e.prefix != "" }

func (e *channelExecer) withContext(ctx context.Context) *channelExecer {
	c := *e
	c.ctx = ctx
	return &c
}

func (e *channelExecer) add(name, url string) (string, error) {
	name = e.prefix + name
	return name, e.exec("--add", url, name)
}

func (e *channelExecer) remove(name string) error {
	return e.exec("--remove", e.prefix+name)
}

func (e *channelExecer) update(names ...string) error {
	return e.exec(append([]string{"--update"}, names...)...)
}

// list retrieves a map of channel name to URLs.
func (e *channelExecer) list() (map[string]string, error) {
	var out string
	if err := e.execOut(&out, "--list"); err != nil {
		return nil, err
	}

	lines := strings.Split(out, "\n")
	list := make(map[string]string, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("cannot parse line %q", line)
		}

		if e.isTemp() {
			// If we're listing temporary channels, only list those that start
			// with the prefix.
			if !strings.HasPrefix(parts[0], e.prefix) {
				continue
			}
		} else {
			// If we're listing all channels, skip temporary ones.
			if strings.HasPrefix(parts[0], channelPrefix) {
				continue
			}
		}

		list[parts[0]] = parts[1]
	}

	return list, nil
}

func (e *channelExecer) rollback() error {
	return e.exec("--rollback")
}

func (e *channelExecer) removeAll() error {
	if !e.isTemp() {
		panic("rollbackAll erroneously called on not-temp channelExecer")
	}

	list, err := e.list()
	if err != nil {
		return errors.Wrap(err, "cannot get channels list")
	}

	for name := range list {
		if !strings.HasPrefix(name, e.prefix) {
			continue
		}

		if err := e.exec("--remove", name); err != nil {
			return errors.Wrapf(err, "cannot remove channel %q", name)
		}
	}

	return nil
}

func (e *channelExecer) exec(args ...string) error {
	return e.execOut(nil, args...)
}

func (e *channelExecer) execOut(out *string, argv ...string) error {
	return executil.Exec(e.ctx, out, "nix-channel", argv...)
}
