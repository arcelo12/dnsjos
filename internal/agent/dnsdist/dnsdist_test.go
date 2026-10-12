package dnsdist

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin writes a shell script named "dnsdist" into a temp dir and points PATH
// at it, so Console exercises the real process plumbing without needing dnsdist.
func fakeBin(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dnsdist")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestConsoleExitZeroFailures pins the bug fixed in Console: dnsdist exits 0 while
// still printing a failure, so the transcript itself must decide success.
func TestConsoleExitZeroFailures(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		wantErrSub string
	}{
		{
			name:       "invalid console key",
			out:        "The currently configured console key is not valid, please configure a valid key using the setKey() directive\n",
			wantErrSub: "console key is not valid",
		},
		{
			name:       "missing config",
			out:        "Unable to read configuration from '/etc/dnsdist/dnsdist.conf'\n",
			wantErrSub: "Unable to read configuration from",
		},
		{
			name:       "lua error in command",
			out:        "Error: [string \"cgkSeen()\"]:1: attempt to call global 'cgkSeen' (a nil value)\nstack traceback:\n",
			wantErrSub: "Error:",
		},
		{
			name:       "fatal error",
			out:        "Fatal error: connecting socket to 127.0.0.1:5199: Connection refused\n",
			wantErrSub: "Fatal error:",
		},
		{
			name:       "success",
			out:        "dnsdist 1.9.16\n",
			wantErrSub: "",
		},
		{
			name:       "empty output is not a failure signature",
			out:        "",
			wantErrSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Print the canned transcript to stdout and exit 0, mimicking the
			// dnsdist behaviours that motivated the fix.
			fakeBin(t, "#!/bin/sh\nprintf '%s' "+shellQuote(tc.out)+"\nexit 0\n")
			got, err := Console(context.Background(), "/etc/dnsdist/dnsdist.conf", "showVersion()")
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("Console err = %v, want nil (out=%q)", err, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("Console err = nil, want error containing %q (out=%q)", tc.wantErrSub, got)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("Console err = %q, want it to contain %q", err, tc.wantErrSub)
			}
		})
	}
}

// TestConsoleNonZeroExitStillErrors guards the pre-existing exit-status path.
func TestConsoleNonZeroExitStillErrors(t *testing.T) {
	fakeBin(t, "#!/bin/sh\necho 'Fatal error: boom' 1>&2\nexit 1\n")
	if _, err := Console(context.Background(), "/etc/dnsdist/dnsdist.conf", "showVersion()"); err == nil {
		t.Fatal("Console err = nil, want error for non-zero exit")
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
