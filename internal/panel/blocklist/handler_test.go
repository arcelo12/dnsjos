package blocklist

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/config"
	"github.com/billyriantono/dnsjos/internal/panel/db/dbtest"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// newHandlerSvc spins up a Service and an authenticated admin HTTP server on its own
// database, returning the service, the pool and a call helper. It mirrors the tests in
// build_test.go but keeps its own isolated DB so the scoped handlers can be exercised at
// the HTTP layer (the JSON contract the web client depends on).
func newHandlerSvc(t *testing.T) (*Service, *pgxpool.Pool, func(method, path, body string, want int, out any)) {
	t.Helper()
	pool := dbtest.New(t, "blocklist")
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &app.Deps{Pool: pool, Log: log, Live: app.NewLiveStore(),
		Settings: app.NewSettings(pool, "http://x"), Cfg: config.Config{DataDir: t.TempDir()}}
	if err := d.Settings.Load(ctx); err != nil {
		t.Fatal(err)
	}
	// The seeded TrustPositif sources point at the internet and litter the listings; drop
	// them so a test's own sources are the only rows.
	exec(t, pool, `DELETE FROM blocklist_sources`)

	s := newService(d)
	tok := app.NewToken()
	exec(t, pool, `WITH u AS (INSERT INTO users (email, password_hash, role) VALUES ('h@x', 'x', 'admin') RETURNING id)
		INSERT INTO sessions (id_hash, user_id, expires_at) SELECT $1, id, now() + interval '1 hour' FROM u`, app.HashToken(tok))
	r := app.NewRouter(d)
	s.routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	call := func(method, path, body string, want int, out any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: app.SessionCookie, Value: tok})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, resp.StatusCode, want, b)
		}
		if out != nil {
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s, pool, call
}

// The builds endpoint must accept every profile_id form: absent (all scopes), empty
// (global only) and a profile uuid — and it must count the same rows it lists. A missing
// placeholder in the count query used to 500 every scoped request.
func TestListBuildsScopedHTTP(t *testing.T) {
	s, pool, call := newHandlerSvc(t)
	a := newProfile(t, pool, "A")

	addSource(t, pool, "g", "manual_domains", "global.com", "")
	addSource(t, pool, "a", "manual_domains", "a.com", a)
	buildScope(t, s, "")
	buildScope(t, s, a)

	var all, global, scoped api.List[api.BlocklistBuild]
	call("GET", "/api/v1/blocklist/builds", "", 200, &all)
	call("GET", "/api/v1/blocklist/builds?profile_id=", "", 200, &global)
	call("GET", "/api/v1/blocklist/builds?profile_id="+a, "", 200, &scoped)

	if all.Total != 2 || global.Total != 1 || scoped.Total != 1 {
		t.Fatalf("all=%d global=%d scoped=%d, want 2/1/1", all.Total, global.Total, scoped.Total)
	}
	if global.Total != len(global.Items) || scoped.Total != len(scoped.Items) {
		t.Fatalf("total != items: global %d/%d scoped %d/%d",
			global.Total, len(global.Items), scoped.Total, len(scoped.Items))
	}
	// Every global row is the global scope; every scoped row is A.
	for _, b := range global.Items {
		if b.ProfileID != "" {
			t.Fatalf("global listing returned profile %q", b.ProfileID)
		}
	}
	if len(scoped.Items) != 1 || scoped.Items[0].ProfileID != a {
		t.Fatalf("scoped listing = %+v", scoped)
	}
}

// listSources distinguishes absent (all scopes) from present-but-empty (global). The UI's
// Global/All selector relies on it; both must return 200 and the right scope.
func TestListSourcesScopeHTTP(t *testing.T) {
	_, pool, call := newHandlerSvc(t)
	a := newProfile(t, pool, "A")

	addSource(t, pool, "g", "manual_domains", "global.com", "")
	addSource(t, pool, "a", "manual_domains", "a.com", a)

	var all, global, scoped api.List[api.BlocklistSource]
	call("GET", "/api/v1/blocklist/sources", "", 200, &all)
	call("GET", "/api/v1/blocklist/sources?profile_id=", "", 200, &global)
	call("GET", "/api/v1/blocklist/sources?profile_id="+a, "", 200, &scoped)

	if all.Total != 2 || global.Total != 1 || scoped.Total != 1 {
		t.Fatalf("all=%d global=%d scoped=%d, want 2/1/1", all.Total, global.Total, scoped.Total)
	}
	if global.Items[0].ProfileID != "" || scoped.Items[0].ProfileID != a {
		t.Fatalf("scope leak: global %q scoped %q", global.Items[0].ProfileID, scoped.Items[0].ProfileID)
	}
}

// listProfiles reports, per profile, whether it has a build of its own or falls back to
// the global one — exactly what the "Served" badge reads.
func TestListProfilesServedHTTP(t *testing.T) {
	s, pool, call := newHandlerSvc(t)
	own := newProfile(t, pool, "Own")
	fallback := newProfile(t, pool, "Fallback")

	addSource(t, pool, "g", "manual_domains", "global.com", "")
	addSource(t, pool, "o", "manual_domains", "own.com", own)
	buildScope(t, s, "")
	ob := buildScope(t, s, own)

	var list api.List[api.BlocklistProfile]
	call("GET", "/api/v1/blocklist/profiles", "", 200, &list)
	byID := map[string]api.BlocklistProfile{}
	for _, p := range list.Items {
		byID[p.ID] = p
	}
	op, fp := byID[own], byID[fallback]
	if op.OwnBuild == nil || op.UsesGlobal || op.EffectiveBuild == nil || op.EffectiveBuild.SHA256 != ob.SHA256 {
		t.Fatalf("own profile %+v", op)
	}
	if fp.OwnBuild != nil || !fp.UsesGlobal || fp.EffectiveBuild == nil || fp.EffectiveBuild.ProfileID != "" {
		t.Fatalf("fallback profile %+v", fp)
	}
}

// Patching a manual source must not 400 when the dialog sends its full shape (url:"").
// A profile move carries the row to the new scope.
func TestPatchManualSourceAndMoveHTTP(t *testing.T) {
	_, pool, call := newHandlerSvc(t)
	a := newProfile(t, pool, "A")
	id := addSource(t, pool, "m", "manual_domains", "m.com", "")

	var src api.BlocklistSource
	// The exact body the React dialog submits for a manual source in a profile scope:
	// a present-but-empty url must be ignored, and the profile move must succeed.
	call("PATCH", "/api/v1/blocklist/sources/"+id, `{"name":"m","url":"","content":"m.com","enabled":true,"profile_id":"`+a+`"}`, 200, &src)
	if src.ProfileID != a || src.Kind != "manual_domains" {
		t.Fatalf("patched %+v", src)
	}
	// Move it back to global with the same full shape: still 200, now global.
	call("PATCH", "/api/v1/blocklist/sources/"+id, `{"name":"m","url":"","content":"m2.com","enabled":true,"profile_id":""}`, 200, &src)
	if src.ProfileID != "" {
		t.Fatalf("move to global left profile %q", src.ProfileID)
	}
}
