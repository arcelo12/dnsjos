package nodes

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/billyriantono/dnsjos/internal/panel/db/dbtest"
)

// profileDB is a fresh database for the per-profile blocklist resolution tests. It reuses
// the package's dbtest() harness (see nodes_test.go) but on its own database name so it
// never collides with setup()/setupReview().
func profileDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.New(t, "nodes_blocklist")
}

// mkProfile creates (or returns) a config profile by name.
func mkProfile(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO config_profiles (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = excluded.name RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// mkNode inserts a node ("" profile = unset) and returns its id.
func mkNode(t *testing.T, pool *pgxpool.Pool, name, profileID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO nodes (name, profile_id) VALUES ($1, nullif($2, '')::uuid) RETURNING id::text`,
		name, profileID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// mkBuild inserts an ok build in a scope ("" = global) with the given sha/size.
func mkBuild(t *testing.T, pool *pgxpool.Pool, profileID, sha string, size int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO blocklist_builds (status, finished_at, sha256, size_bytes, profile_id)
		 VALUES ('ok', now(), $1, $2, nullif($3, '')::uuid) RETURNING id`,
		sha, size, profileID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A profile with no build of its own is served the global sha; a profile with its own
// build is served that one.
func TestCurrentBuildProfileFallback(t *testing.T) {
	pool := profileDB(t)
	ctx := context.Background()
	a := mkProfile(t, pool, "A")

	mkBuild(t, pool, "", "global-sha", 111)

	// No own build → global sha and size.
	if sha, size, err := currentBuild(ctx, pool, a); err != nil || sha != "global-sha" || size != 111 {
		t.Fatalf("profile with no build: sha %q size %d err %v, want global", sha, size, err)
	}
	// The global scope asks for the global build directly.
	if sha, size, err := currentBuild(ctx, pool, ""); err != nil || sha != "global-sha" || size != 111 {
		t.Fatalf("global scope: sha %q size %d err %v", sha, size, err)
	}

	// Own build → its own sha, never global.
	mkBuild(t, pool, a, "a-sha", 222)
	if sha, size, err := currentBuild(ctx, pool, a); err != nil || sha != "a-sha" || size != 222 {
		t.Fatalf("profile with own build: sha %q size %d err %v, want a-sha", sha, size, err)
	}
	// Another profile with no build keeps falling back to global.
	b := mkProfile(t, pool, "B")
	if sha, _, err := currentBuild(ctx, pool, b); err != nil || sha != "global-sha" {
		t.Fatalf("profile B: sha %q err %v, want global", sha, err)
	}
	// No build anywhere → empty sha, zero size, no error.
	pool2 := profileDB(t) // fresh database
	if sha, size, err := currentBuild(ctx, pool2, ""); err != nil || sha != "" || size != 0 {
		t.Fatalf("no build at all: sha %q size %d err %v, want empty", sha, size, err)
	}
}

// nodeProfileID resolves a node's effective profile: its own, else the "default" profile.
func TestNodeProfileID(t *testing.T) {
	pool := profileDB(t)
	ctx := context.Background()
	def := mkProfile(t, pool, "default")
	a := mkProfile(t, pool, "A")

	unset := mkNode(t, pool, "n-unset", "")
	pinned := mkNode(t, pool, "n-pinned", a)

	if got, err := nodeProfileID(ctx, pool, unset); err != nil || got != def {
		t.Fatalf("unset node → %q err %v, want default %q", got, err, def)
	}
	if got, err := nodeProfileID(ctx, pool, pinned); err != nil || got != a {
		t.Fatalf("pinned node → %q err %v, want A %q", got, err, a)
	}
	// Unknown node: no profile, no error.
	if got, err := nodeProfileID(ctx, pool, "00000000-0000-0000-0000-000000000000"); err != nil || got != "" {
		t.Fatalf("unknown node → %q err %v, want empty", got, err)
	}
}

// desiredBlocklists maps the global scope and every profile with a build to the newest sha
// of that scope.
func TestDesiredBlocklists(t *testing.T) {
	pool := profileDB(t)
	ctx := context.Background()
	a := mkProfile(t, pool, "A")
	b := mkProfile(t, pool, "B")

	// Two global builds: the newest wins. One A build and one B build.
	mkBuild(t, pool, "", "g-old", 1)
	mkBuild(t, pool, "", "g-new", 2)
	mkBuild(t, pool, a, "a-sha", 3)
	mkBuild(t, pool, b, "b-sha", 4)

	got, err := desiredBlocklists(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"": "g-new", a: "a-sha", b: "b-sha"}
	if len(got) != len(want) {
		t.Fatalf("desiredBlocklists = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("scope %q → %q, want %q (all: %v)", k, got[k], v, got)
		}
	}

	// A newer A build replaces A's sha without disturbing the others.
	mkBuild(t, pool, a, "a-new", 5)
	got, err = desiredBlocklists(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if got[a] != "a-new" || got[""] != "g-new" || got[b] != "b-sha" {
		t.Fatalf("after newer A build: %v", got)
	}

	// A fresh database with no builds has an empty map.
	if m, err := desiredBlocklists(ctx, profileDB(t)); err != nil || len(m) != 0 {
		t.Fatalf("no builds: %v err %v, want empty", m, err)
	}
}
