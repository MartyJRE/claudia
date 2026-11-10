package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/update"
)

func releasesFixtureViews() []update.Release {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	return []update.Release{
		{Tag: "v0.3.0", Name: "v0.3.0", Body: "### ui\n- **rounded** pills", Published: day(10)},
		{Tag: "v0.2.0", Name: "Big one", Body: "### update\n- release notes page", Published: day(5)},
		{Tag: "v0.1.0", Published: day(1)},
	}
}

// openReleasesViews presses W and feeds back the load it starts.
func openReleasesViews(t *testing.T, m *Model, list []update.Release, err error) {
	t.Helper()
	var pages []int
	fake(t, &fetchReleases, func(p int) ([]update.Release, error) {
		pages = append(pages, p)
		return list, err
	})
	cmd := pressViews(m, "W")
	if cmd == nil || m.rel == nil {
		t.Fatal("W opens the release notes and loads them")
	}
	if s := screenViews(m); !strings.Contains(s, "asking GitHub for the releases…") {
		t.Errorf("loading screen:\n%s", s)
	}
	m.Update(cmd())
	if len(pages) != 1 || pages[0] != 1 {
		t.Errorf("a short first page is the only one: %v", pages)
	}
}

func TestReleasesStepThroughViews(t *testing.T) {
	m := newModelViews(t)
	fake(t, &currentVersion, func() (string, bool) { return "v0.2.0", true })
	openReleasesViews(t, m, releasesFixtureViews(), nil)

	s := screenViews(m)
	for _, want := range []string{"✻ v0.3.0 · 2026-11-10 · 1 of 3", "rounded pills", "[ older", "] newer", "esc back"} {
		if !strings.Contains(s, want) {
			t.Errorf("newest release lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "installed") || strings.Contains(s, "**") {
		t.Errorf("v0.3.0 isn't installed, and the notes are rendered:\n%s", s)
	}

	pressViews(m, "[")
	s = screenViews(m)
	for _, want := range []string{"✻ v0.2.0 · Big one · 2026-11-05 · 2 of 3", "● installed", "release notes page"} {
		if !strings.Contains(s, want) {
			t.Errorf("[ steps to the older release, lacks %q:\n%s", want, s)
		}
	}
	pressViews(m, "p", "[")
	if s := screenViews(m); !strings.Contains(s, "v0.1.0 · 2026-11-01 · 3 of 3") || !strings.Contains(s, "no notes for this release") {
		t.Errorf("p steps back too, and stops at the oldest:\n%s", s)
	}
	pressViews(m, "]")
	if m.rel.i != 1 {
		t.Errorf("] steps to the newer release: %d", m.rel.i)
	}
	pressViews(m, "n", "]")
	if m.rel.i != 0 {
		t.Errorf("n steps forward too, and stops at the newest: %d", m.rel.i)
	}

	if cmd := pressViews(m, "W"); m.openReleases() != nil || cmd != nil {
		t.Error("the page opens once")
	}
	pressViews(m, "esc")
	if m.rel != nil {
		t.Error("esc closes the page")
	}
	if _, cmd := m.Update(releasesMsg{}); cmd != nil || m.rel != nil {
		t.Error("a late load after closing is ignored")
	}
}

func TestReleasesScrollViews(t *testing.T) {
	m := newModelViews(t)
	var body []string
	for i := range 80 {
		body = append(body, fmt.Sprintf("- change %d", i))
	}
	openReleasesViews(t, m, []update.Release{{Tag: "v0.4.0", Body: strings.Join(body, "\n")}, {Tag: "v0.3.0"}}, nil)
	if s := screenViews(m); strings.Contains(s, "change 79") || strings.Count(s, "\n")+1 > m.height {
		t.Errorf("long notes scroll:\n%s", s)
	}
	pressViews(m, "G")
	if s := screenViews(m); !strings.Contains(s, "change 79") || !strings.Contains(s, "100%") {
		t.Errorf("G goes to the end:\n%s", s)
	}
	m.Update(tea.WindowSizeMsg{Width: m.width, Height: 30})
	if m.rel.vp.YOffset == 0 || m.rel.vp.Height != 26 {
		t.Errorf("a taller or shorter terminal keeps the place: %d", m.rel.vp.YOffset)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.rel.vp.YOffset != 0 || m.rel.vp.Width != 100 {
		t.Errorf("a narrower one rewraps from the top: %d", m.rel.vp.YOffset)
	}
	pressViews(m, "g")
	if m.rel.vp.YOffset != 0 {
		t.Error("g goes to the top")
	}
	pressViews(m, "j")
	if m.rel.vp.YOffset != 1 {
		t.Error("j scrolls")
	}
	pressViews(m, "[")
	if m.rel.vp.YOffset != 0 {
		t.Error("another release starts at its top")
	}
}

func TestReleasesEmptyAndFailingViews(t *testing.T) {
	m := newModelViews(t)
	openReleasesViews(t, m, nil, nil)
	if s := screenViews(m); !strings.Contains(s, "no releases yet") {
		t.Errorf("empty:\n%s", s)
	}
	pressViews(m, "[", "]")
	if m.rel.i != 0 {
		t.Errorf("nothing to step through: %d", m.rel.i)
	}
	pressViews(m, "q")

	openReleasesViews(t, m, nil, errors.New("github: 403 rate limited"))
	if s := screenViews(m); !strings.Contains(s, "couldn't load the releases: github: 403 rate limited") {
		t.Errorf("failure:\n%s", s)
	}
	pressViews(m, "esc")

	full := make([]update.Release, update.PerPage)
	for i := range full {
		full[i] = update.Release{Tag: fmt.Sprintf("v0.0.%d", i)}
	}
	fake(t, &fetchReleases, func(p int) ([]update.Release, error) {
		if p > 1 {
			return nil, errors.New("github: 500")
		}
		return full, nil
	})
	m.Update(m.openReleases()())
	if s := screenViews(m); !strings.Contains(s, "v0.0.0") || !strings.Contains(s, "older releases didn't load") {
		t.Errorf("a later page failing keeps what loaded:\n%s", s)
	}
}

func TestReleasesPagesViews(t *testing.T) {
	m := newModelViews(t)
	full := make([]update.Release, update.PerPage)
	for i := range full {
		full[i] = update.Release{Tag: fmt.Sprintf("v0.0.%d", i)}
	}
	var pages []int
	fake(t, &fetchReleases, func(p int) ([]update.Release, error) {
		pages = append(pages, p)
		return full, nil
	})
	m.Update(m.openReleases()())
	if len(pages) != maxReleasePages || pages[maxReleasePages-1] != maxReleasePages {
		t.Errorf("full pages load the next one, up to the cap: %v", pages)
	}
	if s := screenViews(m); !strings.Contains(s, fmt.Sprintf("1 of %d", update.PerPage*maxReleasePages)) {
		t.Errorf("header counts every release:\n%s", s)
	}
}
