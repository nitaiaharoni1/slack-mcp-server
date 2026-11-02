package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/rusq/slackdump/v3/auth"
	"go.uber.org/zap"
)

// DefaultAccountName names the single account configured by the legacy
// SLACK_MCP_XOXP_TOKEN / XOXB / XOXC+XOXD variables.
const DefaultAccountName = "default"

// Credentials are the tokens of one Slack account (one user in one workspace).
type Credentials struct {
	Name string
	XOXP string
	XOXB string
	XOXC string
	XOXD string
}

// IsDemo reports whether the credentials are the "demo" placeholders.
func (c Credentials) IsDemo() bool {
	return c.XOXP == "demo" || (c.XOXC == "demo" && c.XOXD == "demo")
}

func (c Credentials) authProvider(logger *zap.Logger) (auth.ValueAuth, error) {
	if c.XOXP != "" && c.XOXB != "" {
		logger.Warn("Both a user (xoxp) and a bot (xoxb) token are set; using the user token, the bot token is ignored.",
			zap.String("context", "console"))
	}
	switch {
	case c.XOXP != "":
		return auth.NewValueAuth(c.XOXP, "")
	case c.XOXB != "":
		logger.Info("Using Bot token authentication", zap.String("context", "console"), zap.String("token_type", "xoxb"))
		return auth.NewValueAuth(c.XOXB, "")
	case c.XOXC != "" && c.XOXD != "":
		return auth.NewValueAuth(c.XOXC, c.XOXD)
	}
	return auth.ValueAuth{}, fmt.Errorf("account %q: set an xoxp token, an xoxb token, or both xoxc and xoxd tokens", c.Name)
}

// LegacyCredentials reads the single-account SLACK_MCP_* token variables.
func LegacyCredentials() Credentials {
	return Credentials{
		Name: DefaultAccountName,
		XOXP: os.Getenv("SLACK_MCP_XOXP_TOKEN"),
		XOXB: os.Getenv("SLACK_MCP_XOXB_TOKEN"),
		XOXC: os.Getenv("SLACK_MCP_XOXC_TOKEN"),
		XOXD: os.Getenv("SLACK_MCP_XOXD_TOKEN"),
	}
}

var accountNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// AccountEnvPrefix is the variable prefix for one named account:
// account "vistral" reads SLACK_MCP_VISTRAL_XOXP_TOKEN and so on.
func AccountEnvPrefix(name string) string {
	return "SLACK_MCP_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

// CredentialsFromEnv returns every configured account.
//
// SLACK_MCP_ACCOUNTS="vistral,lighthouse" declares named accounts, each reading
// SLACK_MCP_<NAME>_XOXP_TOKEN (or _XOXB_TOKEN, or _XOXC_TOKEN + _XOXD_TOKEN).
// Without it, the legacy single-account variables make one account "default".
// The default account is SLACK_MCP_DEFAULT_ACCOUNT, else the first one listed.
func CredentialsFromEnv() (accounts []Credentials, defaultName string, err error) {
	list := strings.TrimSpace(os.Getenv("SLACK_MCP_ACCOUNTS"))
	if list == "" {
		return []Credentials{LegacyCredentials()}, DefaultAccountName, nil
	}

	seen := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !accountNamePattern.MatchString(name) {
			return nil, "", fmt.Errorf("SLACK_MCP_ACCOUNTS: invalid account name %q (use lowercase letters, digits, - and _)", name)
		}
		if seen[name] {
			return nil, "", fmt.Errorf("SLACK_MCP_ACCOUNTS: account %q is listed twice", name)
		}
		seen[name] = true
		prefix := AccountEnvPrefix(name)
		accounts = append(accounts, Credentials{
			Name: name,
			XOXP: os.Getenv(prefix + "XOXP_TOKEN"),
			XOXB: os.Getenv(prefix + "XOXB_TOKEN"),
			XOXC: os.Getenv(prefix + "XOXC_TOKEN"),
			XOXD: os.Getenv(prefix + "XOXD_TOKEN"),
		})
	}
	if len(accounts) == 0 {
		return nil, "", fmt.Errorf("SLACK_MCP_ACCOUNTS is set but names no account")
	}

	defaultName = strings.ToLower(strings.TrimSpace(os.Getenv("SLACK_MCP_DEFAULT_ACCOUNT")))
	if defaultName == "" {
		defaultName = accounts[0].Name
	} else if !seen[defaultName] {
		return nil, "", fmt.Errorf("SLACK_MCP_DEFAULT_ACCOUNT %q is not in SLACK_MCP_ACCOUNTS", defaultName)
	}
	return accounts, defaultName, nil
}

// Registry holds one provider per account. Each provider has its own client,
// caches and rate limiter, so calls for different accounts never share state.
type Registry struct {
	providers   map[string]*ApiProvider
	order       []string
	defaultName string
}

// NewRegistry builds a registry from already-constructed providers.
func NewRegistry(providers []*ApiProvider, defaultName string) (*Registry, error) {
	r := &Registry{providers: map[string]*ApiProvider{}, defaultName: defaultName}
	for _, p := range providers {
		r.providers[p.Name()] = p
		r.order = append(r.order, p.Name())
	}
	if _, ok := r.providers[defaultName]; !ok {
		return nil, fmt.Errorf("default account %q is not configured", defaultName)
	}
	return r, nil
}

// NewRegistryFromEnv authenticates every configured account.
func NewRegistryFromEnv(transport string, logger *zap.Logger) (*Registry, error) {
	accounts, defaultName, err := CredentialsFromEnv()
	if err != nil {
		return nil, err
	}
	single := len(accounts) == 1
	providers := make([]*ApiProvider, 0, len(accounts))
	for _, creds := range accounts {
		p, err := NewFromCredentials(transport, creds, single, logger)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return NewRegistry(providers, defaultName)
}

// Get returns the provider for an account; an empty name means the default.
func (r *Registry) Get(name string) (*ApiProvider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = r.defaultName
	}
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown Slack account %q; configured accounts: %s", name, strings.Join(r.order, ", "))
	}
	return p, nil
}

// Default returns the default account's provider.
func (r *Registry) Default() *ApiProvider {
	return r.providers[r.defaultName]
}

// DefaultName returns the default account's name.
func (r *Registry) DefaultName() string {
	return r.defaultName
}

// All returns every provider in configuration order.
func (r *Registry) All() []*ApiProvider {
	out := make([]*ApiProvider, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.providers[name])
	}
	return out
}

// Names returns the account names, sorted.
func (r *Registry) Names() []string {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

// IsMulti reports whether more than one account is configured.
func (r *Registry) IsMulti() bool {
	return len(r.order) > 1
}

// Any reports whether at least one account satisfies f.
func (r *Registry) Any(f func(*ApiProvider) bool) bool {
	for _, p := range r.providers {
		if f(p) {
			return true
		}
	}
	return false
}

// IsReady reports whether every account's caches are loaded.
func (r *Registry) IsReady() (bool, error) {
	for _, p := range r.All() {
		if ready, err := p.IsReady(); !ready {
			return false, fmt.Errorf("account %q: %w", p.Name(), err)
		}
	}
	return true, nil
}

// ServerTransport returns the transport all accounts were built for.
func (r *Registry) ServerTransport() string {
	return r.Default().ServerTransport()
}

type accountCtxKey struct{}

// WithAccount stores the provider resolved for the current tool call.
func WithAccount(ctx context.Context, p *ApiProvider) context.Context {
	return context.WithValue(ctx, accountCtxKey{}, p)
}

// FromContext returns the provider resolved for the current tool call, or the
// default account when the call carried none (resources, prompts).
func (r *Registry) FromContext(ctx context.Context) *ApiProvider {
	if p, ok := ctx.Value(accountCtxKey{}).(*ApiProvider); ok && p != nil {
		return p
	}
	return r.Default()
}
