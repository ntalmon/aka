package llm

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// baseURLEnv redirects provider requests to an alternate host.
//
// Only loopback destinations are honoured. This variable influences where the
// API key is transmitted, so an unrestricted version would be a credential
// exfiltration primitive for anything able to set an environment variable.
const baseURLEnv = "AKA_LLM_BASE_URL"

// replaceBase returns defaultURL with its scheme and host replaced by those of
// base. The path of defaultURL is preserved, so a single base serves every
// provider: Anthropic keeps /v1/messages, the OpenAI-compatible providers keep
// their own paths. Any path in base is ignored.
func replaceBase(defaultURL, base string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", base, err)
	}
	if b.Scheme == "" || b.Host == "" {
		return "", fmt.Errorf("base URL %q must be absolute (scheme and host required)", base)
	}
	d, err := url.Parse(defaultURL)
	if err != nil {
		return "", fmt.Errorf("parse default URL %q: %w", defaultURL, err)
	}
	d.Scheme = b.Scheme
	d.Host = b.Host
	return d.String(), nil
}

// resolveBaseURL applies $AKA_LLM_BASE_URL to defaultURL when it names a
// loopback host, and returns defaultURL unchanged otherwise. Refusals are
// reported on stderr rather than returned, so a bad value degrades to normal
// behaviour instead of breaking the command.
func resolveBaseURL(defaultURL string) string {
	override := strings.TrimSpace(os.Getenv(baseURLEnv))
	if override == "" {
		return defaultURL
	}

	u, err := url.Parse(override)
	if err != nil || u.Scheme == "" || u.Host == "" {
		fmt.Fprintf(os.Stderr, "warning: ignoring %s=%q — not an absolute URL\n", baseURLEnv, override)
		return defaultURL
	}
	if !isLoopbackHost(u.Hostname()) {
		fmt.Fprintf(os.Stderr,
			"warning: ignoring %s=%q — only loopback hosts are allowed, because this "+
				"setting controls where your API key is sent\n", baseURLEnv, override)
		return defaultURL
	}

	resolved, err := replaceBase(defaultURL, override)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring %s=%q — %v\n", baseURLEnv, override, err)
		return defaultURL
	}
	return resolved
}

// isLoopbackHost reports whether host is a loopback literal.
//
// No DNS resolution is performed. A name that merely resolves to 127.0.0.1 is
// refused, because resolution depends on state an attacker may control and
// honouring it would defeat the restriction.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
