// Package recursors manages the recursive resolvers the nodes use as upstreams (SPEC §20):
// enrollment tokens and the installer, the recursor agent's config and heartbeat, and
// the admin API behind the Recursors pages.
package recursors

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/httpx"
	"github.com/billyriantono/dnsjos/internal/panel/install"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

type svc struct {
	d    *app.Deps
	tmpl *template.Template

	mu   sync.Mutex
	live map[string]liveEntry // recursor id → latest heartbeat and the rates it gave
}

// liveEntry: qps and cache hit ratio come from the deltas of the last two heartbeats.
type liveEntry struct {
	hb       api.RecursorHeartbeat
	at       time.Time
	qps, hit float64
}

// Register mounts the recursor admin, agent and installer routes and the status job.
func Register(r *app.Router, d *app.Deps) {
	s := &svc{d: d, live: map[string]liveEntry{},
		tmpl: template.Must(template.ParseFS(install.Assets(), "install-recursor.sh.tmpl"))}

	r.Viewer("GET /api/v1/recursor-tokens", s.listTokens)
	r.Admin("POST /api/v1/recursor-tokens", s.createToken)
	r.Admin("DELETE /api/v1/recursor-tokens/{id}", s.deleteToken)

	r.Viewer("GET /api/v1/recursors", s.list)
	r.Viewer("GET /api/v1/recursors/{id}", s.get)
	r.Admin("PATCH /api/v1/recursors/{id}", s.patch)
	r.Admin("DELETE /api/v1/recursors/{id}", s.remove)
	r.Viewer("GET /api/v1/recursors/{id}/live", s.liveGet)
	r.Admin("POST /api/v1/recursors/{id}/commands", s.command)

	r.Public("POST /agent/v1/recursor/enroll", s.enroll)
	r.Recursor("GET /agent/v1/recursor/config", s.config)
	r.Recursor("POST /agent/v1/recursor/heartbeat", s.heartbeat)

	r.Public("GET /install-recursor.sh", s.installScript)

	if d.Jobs != nil {
		d.Jobs.Every("recursor-status", 15*time.Second, s.statusTick)
	}
}

// installScript serves the recursor installer with the panel's public URL templated in.
func (s *svc) installScript(w http.ResponseWriter, r *http.Request) {
	pu := strings.TrimRight(s.d.Settings.PublicURL(), "/")
	if !install.SafePanelURL(pu) {
		httpx.WriteError(w, http.StatusInternalServerError, "bad_public_url",
			"the panel public URL is not a valid http(s) URL; fix it in Settings")
		return
	}
	var buf bytes.Buffer
	if err := s.tmpl.Execute(&buf, struct{ PanelURL string }{pu}); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
