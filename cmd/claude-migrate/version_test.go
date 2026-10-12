package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	bi := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	for _, tc := range []struct {
		name string
		v    string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"ldflags version is kept", "0.1.0", bi("v0.2.0"), true, "0.1.0"},
		{"go install build reports the module version", "dev", bi("v0.1.0"), true, "v0.1.0"},
		{"local build with (devel) stays dev", "dev", bi("(devel)"), true, "dev"},
		{"empty module version stays dev", "dev", bi(""), true, "dev"},
		{"no build info stays dev", "dev", nil, false, "dev"},
		{"ok false ignores build info", "dev", bi("v0.1.0"), false, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.v, tc.bi, tc.ok); got != tc.want {
				t.Fatalf("resolveVersion(%q, ..., %v) = %q, want %q", tc.v, tc.ok, got, tc.want)
			}
		})
	}
}
