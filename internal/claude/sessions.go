package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Profile is one Claude Code config dir: ~/.claude (work) and, when it exists,
// ~/.claude-personal, selected with CLAUDE_CONFIG_DIR.
type Profile struct {
	Name string
	Dir  string
}

func DefaultProfiles() []Profile {
	home, _ := os.UserHomeDir()
	var out []Profile
	for _, p := range []Profile{
		{"work", filepath.Join(home, ".claude")},
		{"personal", filepath.Join(home, ".claude-personal")},
	} {
		if st, err := os.Stat(p.Dir); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

type Session struct {
	ID      string
	Profile Profile
	*Transcript
	Live *Live
}

func (s Session) Name() string {
	if s.Transcript != nil {
		if t := s.Transcript.Title(); t != "" {
			return t
		}
	}
	if s.Live != nil && s.Live.Name != "" {
		return s.Live.Name
	}
	return s.ID[:8]
}

func (s Session) Dir() string {
	if s.Live != nil && s.Live.Cwd != "" {
		return s.Live.Cwd
	}
	if s.Transcript != nil {
		return s.Transcript.Cwd
	}
	return ""
}

func (s Session) LastActive() time.Time {
	var t time.Time
	if s.Transcript != nil {
		t = s.Transcript.Updated
	}
	if s.Live != nil && s.Live.Since().After(t) {
		t = s.Live.Since()
	}
	return t
}

// Ticket follows the order branch → title → last ticket branch; the last one
// covers sessions that end back on devel after pushing.
func (s Session) Ticket() string {
	if s.Transcript == nil {
		return ""
	}
	if k := TicketFrom(s.Branch); k != "" {
		return k
	}
	if k := TicketFrom(s.Title()); k != "" {
		return k
	}
	return TicketFrom(s.TicketBranch)
}

type Store struct {
	Profiles []Profile
	Window   time.Duration
	cache    map[string]*Transcript
}

func NewStore(profiles []Profile, window time.Duration) *Store {
	return &Store{Profiles: profiles, Window: window, cache: map[string]*Transcript{}}
}

// Sessions returns live sessions plus every session touched within Window,
// most recently active first.
func (s *Store) Sessions() []Session {
	cutoff := time.Now().Add(-s.Window)
	var out []Session
	for _, p := range s.Profiles {
		live := map[string]*Live{}
		for _, l := range ReadRegistry(p.Dir) {
			l := l
			live[l.SessionID] = &l
		}
		files, _ := filepath.Glob(filepath.Join(p.Dir, "projects", "*", "*.jsonl"))
		seen := map[string]bool{}
		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			l := live[id]
			if l == nil {
				st, err := os.Stat(f)
				if err != nil || st.ModTime().Before(cutoff) {
					continue
				}
			}
			t := s.cache[f]
			if t == nil {
				t = &Transcript{ID: id, Path: f}
				s.cache[f] = t
			}
			_ = t.Feed()
			seen[id] = true
			out = append(out, Session{ID: id, Profile: p, Transcript: t, Live: l})
		}
		for id, l := range live {
			if !seen[id] {
				out = append(out, Session{ID: id, Profile: p, Live: l})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Live != nil) != (out[j].Live != nil) {
			return out[i].Live != nil
		}
		return out[i].LastActive().After(out[j].LastActive())
	})
	return out
}
