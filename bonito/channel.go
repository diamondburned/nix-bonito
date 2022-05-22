package bonito

import (
	"context"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
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

// Resolve resolves the channel input using one of the ChannelResolvers.
func (in ChannelInput) Resolve() (string, error) {
	u, err := in.URL.Parse()
	if err != nil {
		return "", err
	}

	resolve, ok := ChannelResolvers[u.Scheme]
	if !ok {
		return "", fmt.Errorf("cannot resolve unknown %q", u.Scheme)
	}

	return resolve(in)
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
type ChannelResolver func(ChannelInput) (string, error)

// ChannelResolvers maps URL schemes to resolvers.
var ChannelResolvers = map[string]ChannelResolver{
	"git":     resolveGit,
	"github":  resolveGitHub,
	"gitlab":  resolveGitLab,
	"gitsrht": resolveSourcehut,
	"http":    useSameURL,
	"https":   useSameURL,
}

func useSameURL(in ChannelInput) (string, error) {
	return string(in.URL), nil
}

func resolveGit(in ChannelInput) (string, error) {
	u, err := in.URL.Parse()
	if err != nil {
		return "", err
	}

	// Note that we should clarify how not having a version defeats the point of
	// nix-bonito and is likely a terrible idea in general. It is not
	// reproducible at all and will just be annoying to the user.
	if in.Version == "" {
		return "", errors.New("git used without version, use http(s) instead")
	}

	switch u.Host {
	case "github.com":
		transGitHubURL(in, u)
	case "gitlab.com":
		transGitLabURL(in, u)
	case "git.sr.ht":
		transSourcehutURL(in, u)
	default:
		return "", fmt.Errorf("unknown git service %q, consider using https://", u.Host)
	}

	return u.String(), nil
}

func resolveGitHub(in ChannelInput) (string, error) {
	return resolveServiceURL(in, "github.com", transGitHubURL)
}

func transGitHubURL(in ChannelInput, u *url.URL) {
	u.Path += "/archive/" + in.Version + ".tar.gz"
	u.Scheme = "https"
}

func resolveGitLab(in ChannelInput) (string, error) {
	return resolveServiceURL(in, "gitlab.com", transGitLabURL)
}

func transGitLabURL(in ChannelInput, u *url.URL) {
	u.Path += fmt.Sprintf("/-/archive/%[1]s/%[2]s-%[1]s.tar.gz", in.Version, path.Base(u.Path))
	u.Scheme = "https"
}

func resolveSourcehut(in ChannelInput) (string, error) {
	return resolveServiceURL(in, "git.sr.ht", transSourcehutURL)
}

func transSourcehutURL(in ChannelInput, u *url.URL) {
	u.Path += "/archive/" + in.Version + ".tar.gz"
	u.Scheme = "https"
}

func resolveServiceURL(in ChannelInput, host string, transformer func(ChannelInput, *url.URL)) (string, error) {
	u, err := in.URL.Parse()
	if err != nil {
		return "", err
	}

	if u.Opaque != "" {
		u.Host = host
		u.Path = u.Opaque
		u.Opaque = ""
	}

	transformer(in, u)
	return u.String(), err
}

func popHost(opaque string) (string, string) {
	parts := strings.SplitN(opaque, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

type channelExecer struct {
	ctx     context.Context
	prefix  string
	updated int
}

func newChannelExecer(ctx context.Context, temp bool) *channelExecer {
	execer := channelExecer{ctx: ctx}
	if temp {
		execer.prefix = "bonito-tmp-"
	}
	return &execer
}

func mustHash(inputs ...interface{}) string {
	h := fnv.New128a()

	for _, in := range inputs {
		str, ok := in.(string)
		if !ok {
			str = fmt.Sprint(in)
		}
		h.Write([]byte(str))
	}

	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (e *channelExecer) isTemp() bool { return e.prefix != "" }

func (e *channelExecer) add(name, url string) (string, error) {
	name = e.prefix + name
	return name, e.exec("--add", url, name)
}

func (e *channelExecer) remove(name string) error {
	return e.exec("--remove", e.prefix+name)
}

func (e *channelExecer) update(names ...string) error {
	if err := e.exec(append([]string{"--update"}, names...)...); err != nil {
		return err
	}
	e.updated++
	return nil
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

		list[parts[0]] = parts[1]
	}

	return list, nil
}

func (e *channelExecer) rollback() error {
	return e.exec("--rollback")
}

// rollbackAll rolls back as much as possible that's done by us. It only works
// if temp is true.
func (e *channelExecer) rollbackAll() error {
	if !e.isTemp() {
		panic("rollbackAll erroneously called on not-temp channelExecer")
	}

	for e.updated > 0 {
		if err := e.rollback(); err != nil {
			return errors.Wrapf(err, "cannot rollback update %d", e.updated)
		}
		e.updated--
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
			return errors.Wrapf(err, "cannot rollback channel %q", name)
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
