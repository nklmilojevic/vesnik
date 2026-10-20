package version

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

func v(s string) *semver.Version {
	if s == "" {
		return nil
	}
	return semver.MustParse(s)
}

func TestNext(t *testing.T) {
	tests := []struct {
		name           string
		cur, lastFinal string
		bump           Bump
		opts           Options
		want           string
	}{
		{"first release patch", "", "", Patch, Options{}, "0.0.1"},
		{"patch", "1.2.3", "1.2.3", Patch, Options{}, "1.2.4"},
		{"minor", "1.2.3", "1.2.3", Minor, Options{}, "1.3.0"},
		{"major", "1.2.3", "1.2.3", Major, Options{}, "2.0.0"},
		{"pre-1.0 breaking bumps minor", "0.31.13", "0.31.13", Major, Options{BreakingBumpsMinor: true}, "0.32.0"},
		{"pre-1.0 breaking bumps major when disabled", "0.31.13", "0.31.13", Major, Options{}, "1.0.0"},
		{"post-1.0 breaking ignores the pre-1.0 rule", "1.0.0", "1.0.0", Major, Options{BreakingBumpsMinor: true}, "2.0.0"},
		{"start rc", "1.2.3", "1.2.3", Minor, Options{Pre: "rc"}, "1.3.0-rc.1"},
		{"next rc", "1.3.0-rc.1", "1.2.3", Minor, Options{Pre: "rc"}, "1.3.0-rc.2"},
		{"next rc with smaller bump stays on the line", "1.3.0-rc.2", "1.2.3", Patch, Options{Pre: "rc"}, "1.3.0-rc.3"},
		{"promote rc", "1.3.0-rc.2", "1.2.3", Patch, Options{}, "1.3.0"},
		{"beta to rc restarts the counter", "1.3.0-beta.4", "1.2.3", Minor, Options{Pre: "rc"}, "1.3.0-rc.1"},
		{"larger bump moves the rc line", "1.3.0-rc.2", "1.2.3", Major, Options{Pre: "rc"}, "2.0.0-rc.1"},
		{"larger bump on promotion", "1.3.0-rc.2", "1.2.3", Major, Options{}, "2.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Next(v(tt.cur), v(tt.lastFinal), tt.bump, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("Next = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNextRejectsBadPre(t *testing.T) {
	if _, err := Next(nil, nil, Patch, Options{Pre: "rc.1"}); err == nil {
		t.Fatal("want error for a dotted pre-release identifier")
	}
}

func TestTemplate(t *testing.T) {
	tmpl, err := ParseTemplate("api@v{version}")
	if err != nil {
		t.Fatal(err)
	}
	if got := tmpl.Render(v("1.2.3")); got != "api@v1.2.3" {
		t.Fatalf("Render = %s", got)
	}
	if got := tmpl.Glob(); got != "api@v*" {
		t.Fatalf("Glob = %s", got)
	}
	if _, ok := tmpl.Parse("web@v1.2.3"); ok {
		t.Fatal("parsed a tag of another package")
	}
	if _, ok := tmpl.Parse("api@v1.2"); ok {
		t.Fatal("parsed a non-strict version")
	}
	latest, final := tmpl.Latest([]string{"api@v1.2.0", "api@v1.10.0-rc.1", "api@v1.9.0", "web@v9.0.0", "junk"})
	if latest.Tag != "api@v1.10.0-rc.1" || final.Tag != "api@v1.9.0" {
		t.Fatalf("Latest = %s, %s", latest.Tag, final.Tag)
	}
	if _, err := ParseTemplate("v"); err == nil {
		t.Fatal("want error without placeholder")
	}
}

func TestFloor(t *testing.T) {
	tests := []struct {
		msgs []string
		want Bump
	}{
		{[]string{"fix: a", "docs: b"}, Patch},
		{[]string{"fix: a", "feat(ui): b"}, Minor},
		{[]string{"feat!: drop flag"}, Major},
		{[]string{"refactor(core)!: rename"}, Major},
		{[]string{"fix: a\n\nBREAKING CHANGE: config moved"}, Major},
		{[]string{"Merge something"}, Patch},
	}
	for _, tt := range tests {
		if got := Floor(tt.msgs); got != tt.want {
			t.Errorf("Floor(%q) = %s, want %s", tt.msgs, got, tt.want)
		}
	}
}

func TestBumpCycle(t *testing.T) {
	if Patch.Cycle() != Minor || Minor.Cycle() != Major || Major.Cycle() != Patch {
		t.Fatal("cycle order broken")
	}
	if _, err := ParseBump("huge"); err == nil {
		t.Fatal("want error")
	}
}
