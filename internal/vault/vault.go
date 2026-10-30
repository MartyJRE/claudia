// Package vault indexes an Obsidian vault: which notes belong to which ticket,
// either by living in the ticket's project folder or by mentioning its key.
package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Note struct {
	Path     string // absolute
	Rel      string // relative to the vault
	Title    string // file name without .md
	Modified time.Time
	Keys     []string // ticket keys it mentions or belongs to
}

func (n Note) Folder() string { return filepath.Dir(n.Rel) }

type Index struct {
	Root     string
	Projects string // folder whose subfolders are named after tickets
	keyRe    *regexp.Regexp

	mu    sync.Mutex
	notes map[string]*Note
}

// New indexes root; keyRe matches whole ticket keys.
func New(root, projects string, keyRe *regexp.Regexp) *Index {
	return &Index{Root: root, Projects: projects, keyRe: keyRe, notes: map[string]*Note{}}
}

// Refresh rescans the vault, rereading only notes whose mtime changed.
func (ix *Index) Refresh() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	seen := map[string]bool{}
	_ = filepath.WalkDir(ix.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") && path != ix.Root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[path] = true
		if n, ok := ix.notes[path]; ok && n.Modified.Equal(info.ModTime()) {
			return nil
		}
		rel, _ := filepath.Rel(ix.Root, path)
		n := &Note{Path: path, Rel: rel, Title: strings.TrimSuffix(name, ".md"), Modified: info.ModTime()}
		n.Keys = ix.keys(n)
		ix.notes[path] = n
		return nil
	})
	for p := range ix.notes {
		if !seen[p] {
			delete(ix.notes, p)
		}
	}
}

func (ix *Index) keys(n *Note) []string {
	set := map[string]bool{}
	// A note in "01 - Projects/LUT-123/…" belongs to LUT-123.
	if rest, ok := strings.CutPrefix(n.Rel, ix.Projects+string(filepath.Separator)); ok {
		folder, _, _ := strings.Cut(rest, string(filepath.Separator))
		if k := ix.keyRe.FindString(folder); k != "" {
			set[strings.ToUpper(k)] = true
		}
	}
	if b, err := os.ReadFile(n.Path); err == nil {
		for _, k := range ix.keyRe.FindAllString(n.Title+"\n"+string(b), -1) {
			set[strings.ToUpper(k)] = true
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// All returns every note, most recently edited first.
func (ix *Index) All() []Note {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var out []Note
	for _, n := range ix.notes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// For returns the notes about any of keys, those in a key's own project folder
// first, then the rest by recency.
func (ix *Index) For(keys ...string) []Note {
	want := map[string]bool{}
	for _, k := range keys {
		if k != "" {
			want[k] = true
		}
	}
	var own, other []Note
	for _, n := range ix.All() {
		hit := false
		for _, k := range n.Keys {
			hit = hit || want[k]
		}
		if !hit {
			continue
		}
		if ix.ownFolder(n, want) {
			own = append(own, n)
		} else {
			other = append(other, n)
		}
	}
	return append(own, other...)
}

func (ix *Index) ownFolder(n Note, want map[string]bool) bool {
	rest, ok := strings.CutPrefix(n.Rel, ix.Projects+string(filepath.Separator))
	if !ok {
		return false
	}
	folder, _, _ := strings.Cut(rest, string(filepath.Separator))
	k := ix.keyRe.FindString(folder)
	return k != "" && want[strings.ToUpper(k)]
}

// NewNote creates <projects>/<key>/<title>.md with frontmatter and returns its path.
// An existing note with that name is left alone and returned.
func (ix *Index) NewNote(key, title string, tags []string) (string, error) {
	title = strings.NewReplacer("/", "-", "\\", "-", ":", " -").Replace(strings.TrimSpace(title))
	dir := filepath.Join(ix.Root, ix.Projects, key)
	path := filepath.Join(dir, title+".md")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	body := "---\ndate: " + time.Now().Format("2006-01-02T15:04") + "\ntags: [" + strings.Join(tags, ", ") + "]\n---\n\n# " +
		title + "\n\n[[" + key + "]]\n\n"
	return path, os.WriteFile(path, []byte(body), 0o644)
}
