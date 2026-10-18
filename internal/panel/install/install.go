// Package install embeds the node and recursor installer templates and the agent binaries
// (bin/dnsjos-agent-linux-<arch> + .sha256 and bin/dnsjos-agent.version, produced by
// `make agent`). The HTTP handlers live in the nodes package.
package install

import (
	"embed"
	"io/fs"
	"net/url"
	"strings"
)

//go:embed all:bin install.sh.tmpl install-recursor.sh.tmpl
var assets embed.FS

// Assets exposes install.sh.tmpl, install-recursor.sh.tmpl and bin/*.
func Assets() fs.FS { return assets }

// AgentVersion is the version the embedded agent binaries were built with (the
// -X main.version of `make agent`), or "" when no agent is embedded.
func AgentVersion() string {
	b, _ := fs.ReadFile(assets, "bin/dnsjos-agent.version")
	return strings.TrimSpace(string(b))
}

// SafePanelURL reports whether the panel URL can be templated into an installer: it
// lands inside single quotes in sh, so anything that could break out is refused.
func SafePanelURL(pu string) bool {
	u, err := url.Parse(pu)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		!strings.ContainsAny(pu, "'\"`$\\ \t\r\n;&|<>")
}
