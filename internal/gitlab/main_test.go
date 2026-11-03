package gitlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// `glab api --hostname H -X METHOD PATH`, answered from files named after
// "METHOD PATH" without the time-dependent updated_after.
const fakeGlabScript = `#!/bin/sh
d=$(dirname "$0")
echo "$3 $5 $6" >> "$d/calls.log"
k=$(printf '%s %s' "$5" "$6" | sed 's/[?&]updated_after=[^&]*//' | tr '/?&= %' '______')
[ -f "$d/r/$k" ] && exec cat "$d/r/$k"
echo "404 Not Found: $6" >&2
exit 1
`

var routeName = strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_", " ", "_", "%", "_")

// useFakeGlab puts a glab on PATH that answers from routes, and returns the
// file its calls are logged to.
func useFakeGlab(t *testing.T, routes map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "r"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range routes {
		if err := os.WriteFile(filepath.Join(dir, "r", routeName.Replace(k)), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(fakeGlabScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return filepath.Join(dir, "calls.log")
}
