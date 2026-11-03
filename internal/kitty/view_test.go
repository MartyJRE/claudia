package kitty

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

// keys hands out one key press per Read, then EOF.
type keys []string

func (k *keys) Read(p []byte) (int, error) {
	if len(*k) == 0 {
		return 0, io.EOF
	}
	n := copy(p, (*k)[0])
	*k = (*k)[1:]
	return n, nil
}

// screen records output and fails writes of image data when broken.
type screen struct {
	bytes.Buffer
	broken bool
}

func (s *screen) Write(p []byte) (int, error) {
	if s.broken && bytes.Contains(p, []byte("a=T")) {
		return 0, errors.New("broken pipe")
	}
	return s.Buffer.Write(p)
}

func (s *screen) WriteString(str string) (int, error) { return s.Write([]byte(str)) }

var headerRe = regexp.MustCompile(` (\S+)\x1b\[0m\x1b\[38;2;92;99;112m  (\d+/\d+)  ·`)

func headers(out string) []string {
	var got []string
	for _, m := range headerRe.FindAllStringSubmatch(out, -1) {
		got = append(got, m[1]+" "+m[2])
	}
	return got
}

// fakeTerminal stubs the terminal size and records what `open` was asked to show.
func fakeTerminal(t *testing.T) *[][]string {
	t.Helper()
	t.Setenv("TMUX", "")
	noWinsize(t)
	oldSize, oldExec := getSize, execCommand
	t.Cleanup(func() { getSize, execCommand = oldSize, oldExec })
	getSize = func(uintptr) (int, int, error) { return 42, 23, nil }
	var ran [][]string
	execCommand = func(name string, args ...string) *exec.Cmd {
		ran = append(ran, append([]string{name}, args...))
		return exec.Command("true")
	}
	return &ran
}

func images(t *testing.T) (string, string) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.png")
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	os.WriteFile(good, buf.Bytes(), 0o600)
	bad := filepath.Join(dir, "b.txt")
	os.WriteFile(bad, []byte("text"), 0o600)
	return good, bad
}

func TestBrowse(t *testing.T) {
	ran := fakeTerminal(t)
	good, bad := images(t)
	in := keys{"n", "o", "p", "x", "\x1b[C", "\x1b[A", "q"}
	var out screen
	if err := browse(&in, &out, []string{good, bad}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	want := []string{"a.png 1/2", "b.txt 2/2", "b.txt 2/2", "a.png 1/2", "a.png 1/2", "b.txt 2/2", "a.png 1/2"}
	if h := headers(got); !slices.Equal(h, want) {
		t.Errorf("frames %v want %v", h, want)
	}
	if !strings.Contains(got, "b.txt: image: unknown format") {
		t.Error("decode error not shown")
	}
	if !strings.Contains(got, "a=T,f=100,q=2,c=4,r=2,m=0") {
		t.Errorf("image not placed: %q", got)
	}
	if !strings.HasPrefix(got, "\x1b[?25l") || !strings.HasSuffix(got, "\x1b[2J\x1b[H\x1b[?25h") {
		t.Errorf("cursor not hidden and restored: %q", got)
	}
	if len(*ran) != 1 || !slices.Equal((*ran)[0], []string{"open", bad}) {
		t.Errorf("opened %v", *ran)
	}
}

func TestBrowseEndsWithInput(t *testing.T) {
	fakeTerminal(t)
	good, _ := images(t)
	in := keys{}
	out := screen{broken: true}
	if err := browse(&in, &out, []string{good}); err != io.EOF {
		t.Errorf("got %v", err)
	}
	if !strings.Contains(out.String(), "  broken pipe") {
		t.Errorf("draw error not shown: %q", out.String())
	}
}

func TestView(t *testing.T) {
	fakeTerminal(t)
	good, _ := images(t)
	oldRaw, oldRestore := makeRaw, restore
	t.Cleanup(func() { makeRaw, restore = oldRaw, oldRestore })
	if err := View(nil); err != nil {
		t.Errorf("view of nothing: %v", err)
	}

	makeRaw = func(uintptr) (*term.State, error) { return nil, errors.New("not a terminal") }
	if err := View([]string{good}); err == nil || err.Error() != "not a terminal" {
		t.Errorf("got %v", err)
	}

	r, w, _ := os.Pipe()
	w.WriteString("q")
	w.Close()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- string(b)
	}()
	state := &term.State{}
	var rawFd, restoredFd uintptr
	var restored *term.State
	makeRaw = func(fd uintptr) (*term.State, error) { rawFd = fd; return state, nil }
	restore = func(fd uintptr, s *term.State) error { restoredFd, restored = fd, s; return nil }
	err := View([]string{good})
	os.Stdin, os.Stdout = oldIn, oldOut
	outW.Close()
	out := <-done
	if err != nil {
		t.Fatal(err)
	}
	if rawFd != r.Fd() || restoredFd != rawFd || restored != state {
		t.Errorf("raw mode on %d, restored %d %p", rawFd, restoredFd, restored)
	}
	if h := headers(out); !slices.Equal(h, []string{"a.png 1/1"}) {
		t.Errorf("frames %v", h)
	}
}
