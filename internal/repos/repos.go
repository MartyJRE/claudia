package repos

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

func Root() string { return config.Get().CodeRoot }

type Repo struct {
	Dir     string
	Branch  string
	Project string // origin path, e.g. group/backend/payments
	Touched time.Time
}

// Find lists git repos under root, the same discovery `tp` does. Branch and origin
// are read straight from .git so scanning ~300 repos stays instant.
func Find(root string) []Repo {
	out, err := exec.Command("fd", "-H", "-t", "d", "^\\.git$", root, "--format", "{//}").Output()
	if err != nil {
		return nil
	}
	var repos []Repo
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if l == "" {
			continue
		}
		r := Repo{Dir: l}
		git := filepath.Join(l, ".git")
		if b, err := os.ReadFile(filepath.Join(git, "HEAD")); err == nil {
			r.Branch = strings.TrimPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
		}
		if st, err := os.Stat(filepath.Join(git, "index")); err == nil {
			r.Touched = st.ModTime()
		}
		r.Project = originProject(filepath.Join(git, "config"))
		repos = append(repos, r)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Dir < repos[j].Dir })
	return repos
}

func originProject(config string) string {
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") {
			inOrigin = l == `[remote "origin"]`
			continue
		}
		if inOrigin && strings.HasPrefix(l, "url") {
			_, u, _ := strings.Cut(l, "=")
			return ProjectFromURL(strings.TrimSpace(u))
		}
	}
	return ""
}

// ProjectFromURL handles both git@host:group/repo.git and https://host/group/repo.git.
func ProjectFromURL(u string) string {
	u = strings.TrimSuffix(u, ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.IndexByte(u, '/'); j >= 0 {
			return u[j+1:]
		}
		return ""
	}
	if _, p, ok := strings.Cut(u, ":"); ok {
		return p
	}
	return ""
}

// Clones returns the local clones of a GitLab project, the one on branch first.
func Clones(all []Repo, project, branch string) []Repo {
	var out []Repo
	for _, r := range all {
		if r.Project == project {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Branch == branch && out[j].Branch != branch })
	return out
}

// ByPreference puts repos under the configured work folder first, most recently
// used first within each group.
func ByPreference(all []Repo, root string) []Repo {
	prefix := filepath.Join(root, config.Get().WorkDir) + "/"
	out := slices.Clone(all)
	slices.SortStableFunc(out, func(a, b Repo) int {
		pa, pb := strings.HasPrefix(a.Dir, prefix), strings.HasPrefix(b.Dir, prefix)
		if pa != pb {
			if pa {
				return -1
			}
			return 1
		}
		return b.Touched.Compare(a.Touched)
	})
	return out
}
