package kitty

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

func TestWrap(t *testing.T) {
	t.Setenv("TMUX", "")
	if got := wrap("\x1b_Ga=d\x1b\\"); got != "\x1b_Ga=d\x1b\\" {
		t.Errorf("outside tmux %q", got)
	}
	t.Setenv("TMUX", "/tmp/x,1,0")
	if got := wrap("\x1b_Ga=d\x1b\\"); got != "\x1bPtmux;\x1b\x1b_Ga=d\x1b\x1b\\\x1b\\" {
		t.Errorf("inside tmux %q", got)
	}
}

func noisy(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	return img
}

var chunkRe = regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")

func TestDraw(t *testing.T) {
	t.Setenv("TMUX", "")
	img := noisy(64, 64)
	var out bytes.Buffer
	if err := draw(&out, img, 10, 5); err != nil {
		t.Fatal(err)
	}
	chunks := chunkRe.FindAllStringSubmatch(out.String(), -1)
	if len(chunks) < 3 {
		t.Fatalf("a noisy 64×64 PNG should span several chunks, got %d", len(chunks))
	}
	if chunks[0][1] != "a=T,f=100,q=2,c=10,r=5,m=1" {
		t.Errorf("first control %q", chunks[0][1])
	}
	var data strings.Builder
	for i, c := range chunks {
		want := "m=1"
		if i == len(chunks)-1 {
			want = "m=0"
		}
		if i > 0 && c[1] != want {
			t.Errorf("chunk %d control %q", i, c[1])
		}
		if len(c[2]) > 4096 {
			t.Errorf("chunk %d is %d bytes", i, len(c[2]))
		}
		data.WriteString(c[2])
	}
	raw, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil {
		t.Fatal(err)
	}
	back, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if back.Bounds() != img.Bounds() || color.RGBAModel.Convert(back.At(5, 7)) != img.At(5, 7) {
		t.Error("image didn't survive the round trip")
	}

	out.Reset()
	if err := draw(&out, image.NewRGBA(image.Rect(0, 0, 1, 1)), 1, 1); err != nil {
		t.Fatal(err)
	}
	if c := chunkRe.FindAllStringSubmatch(out.String(), -1); len(c) != 1 || c[0][1] != "a=T,f=100,q=2,c=1,r=1,m=0" {
		t.Errorf("single chunk %q", out.String())
	}
}

func TestClear(t *testing.T) {
	t.Setenv("TMUX", "")
	var out bytes.Buffer
	clear(&out)
	if out.String() != "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[2J\x1b[H" {
		t.Errorf("got %q", out.String())
	}
}

func TestFit(t *testing.T) {
	if term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("cell size comes from the terminal")
	}
	for _, c := range []struct {
		w, h, cols, rows int
		wc, wr           int
	}{
		{1000, 500, 40, 40, 40, 10},
		{500, 1000, 100, 20, 20, 20},
		{20, 20, 100, 100, 20, 10},
		{1, 1000, 10, 10, 1, 10},
	} {
		img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		if gc, gr := fit(img, c.cols, c.rows); gc != c.wc || gr != c.wr {
			t.Errorf("%d×%d in %d×%d: got %d×%d want %d×%d", c.w, c.h, c.cols, c.rows, gc, gr, c.wc, c.wr)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	os.WriteFile(p, buf.Bytes(), 0o600)
	img, err := load(p)
	if err != nil || img.Bounds().Dx() != 3 {
		t.Errorf("load: %v", err)
	}
	bad := filepath.Join(dir, "b.png")
	os.WriteFile(bad, []byte("not an image"), 0o600)
	if _, err := load(bad); err == nil || !strings.HasPrefix(err.Error(), "b.png: ") {
		t.Errorf("bad image: %v", err)
	}
	if _, err := load(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("missing file")
	}
	if err := View(nil); err != nil {
		t.Errorf("view of nothing: %v", err)
	}
}
