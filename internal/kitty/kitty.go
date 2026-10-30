// Package kitty shows images with the kitty graphics protocol, which Ghostty
// speaks. Inside tmux each escape goes through DCS passthrough, which needs
// `set -g allow-passthrough on`.
package kitty

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func wrap(seq string) string {
	if os.Getenv("TMUX") == "" {
		return seq
	}
	return "\x1bPtmux;" + string(bytes.ReplaceAll([]byte(seq), []byte("\x1b"), []byte("\x1b\x1b"))) + "\x1b\\"
}

// draw transmits and places the image scaled into cols×rows cells.
func draw(w io.Writer, img image.Image, cols, rows int) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	const chunk = 4096
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		var ctl string
		if i == 0 {
			ctl = fmt.Sprintf("a=T,f=100,q=2,c=%d,r=%d,m=%d", cols, rows, more)
		} else {
			ctl = fmt.Sprintf("m=%d", more)
		}
		if _, err := io.WriteString(w, wrap("\x1b_G"+ctl+";"+data[i:end]+"\x1b\\")); err != nil {
			return err
		}
	}
	return nil
}

func clear(w io.Writer) {
	io.WriteString(w, wrap("\x1b_Ga=d,d=A,q=2\x1b\\"))
	io.WriteString(w, "\x1b[2J\x1b[H")
}

// fit keeps the aspect ratio within the cell box, using the terminal's real cell
// size when it reports one (tmux doesn't; then assume cells twice as tall as wide).
func fit(img image.Image, cols, rows int) (int, int) {
	cw, ch := 1.0, 2.0
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Xpixel > 0 && ws.Col > 0 {
		cw = float64(ws.Xpixel) / float64(ws.Col)
		ch = float64(ws.Ypixel) / float64(ws.Row)
	}
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	// Never upscale past the image's own size.
	maxC := min(float64(cols), iw/cw)
	maxR := min(float64(rows), ih/ch)
	if (iw/cw)/(ih/ch) > maxC/maxR {
		c := maxC
		return max(1, int(c)), max(1, int(c*cw*ih/iw/ch))
	}
	r := maxR
	return max(1, int(r*ch*iw/ih/cw)), max(1, int(r))
}

// View pages through files until q/esc: n/space/→ next, p/← previous, o opens
// the file in Preview.
func View(files []string) error {
	if len(files) == 0 {
		return nil
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(uintptr(fd))
	if err != nil {
		return err
	}
	defer term.Restore(uintptr(fd), old)
	out := os.Stdout
	io.WriteString(out, "\x1b[?25l")
	defer io.WriteString(out, "\x1b[?25h")
	defer clear(out)

	i := 0
	key := make([]byte, 8)
	for {
		clear(out)
		cols, rows, _ := term.GetSize(uintptr(out.Fd()))
		f := files[i]
		fmt.Fprintf(out, "\x1b[1;38;2;198;120;221m %s\x1b[0m\x1b[38;2;92;99;112m  %d/%d  ·  n next  p prev  o open  q back\x1b[0m\r\n\r\n",
			filepath.Base(f), i+1, len(files))
		if img, err := load(f); err != nil {
			fmt.Fprintf(out, "  \x1b[38;2;224;108;117m%s\x1b[0m", err)
		} else {
			c, r := fit(img, cols-2, rows-3)
			fmt.Fprint(out, " ")
			if err := draw(out, img, c, r); err != nil {
				fmt.Fprintf(out, "  %s", err)
			}
		}
		n, err := os.Stdin.Read(key)
		if err != nil {
			return err
		}
		switch k := string(key[:n]); k {
		case "q", "\x1b", "\x03", "i":
			return nil
		case "n", " ", "j", "l", "\x1b[C", "\x1b[B":
			i = (i + 1) % len(files)
		case "p", "k", "h", "\x1b[D", "\x1b[A":
			i = (i + len(files) - 1) % len(files)
		case "o":
			_ = exec.Command("open", f).Start()
		}
	}
}

func load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return img, nil
}
