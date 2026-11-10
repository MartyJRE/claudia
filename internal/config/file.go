package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// File is config.json as written: no env overrides, no defaults, ~ left as
// is, and keys claudia doesn't know kept for the next Save. Fields are
// addressed by dotted keys, e.g. "teams.mail.rules".
type File struct {
	top map[string]json.RawMessage
}

// Raw reads config.json; a missing or empty file is an empty File.
func Raw() (File, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(b)) == 0) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f.top); err != nil {
		return File{}, fmt.Errorf("%s: %w", Path(), err)
	}
	return f, nil
}

// Value is the key's JSON in the file, or nil when it isn't set.
func (f File) Value(key string) json.RawMessage {
	m := f.top
	parts := strings.Split(key, ".")
	for i, p := range parts {
		v, ok := m[p]
		if !ok {
			return nil
		}
		if i == len(parts)-1 {
			return v
		}
		m = nil
		if json.Unmarshal(v, &m) != nil {
			return nil
		}
	}
	return nil
}

func (f File) Has(key string) bool { return f.Value(key) != nil }

// With is a copy of f with key set to v, creating the objects above it.
func (f File) With(key string, v json.RawMessage) (File, error) {
	top, err := set(f.top, strings.Split(key, "."), v)
	return File{top}, err
}

// Without is a copy of f without key. Objects above it left empty are dropped
// too, unless base has them: an empty "mail" still turns mail on.
func (f File) Without(key string, base File) File {
	top, _ := set(f.top, strings.Split(key, "."), nil)
	out := File{top}
	parts := strings.Split(key, ".")
	for i := len(parts) - 1; i > 0; i-- {
		parent := strings.Join(parts[:i], ".")
		if base.Has(parent) || string(out.Value(parent)) != "{}" {
			break
		}
		out.top, _ = set(out.top, strings.Split(parent, "."), nil)
	}
	return out
}

func set(m map[string]json.RawMessage, path []string, v json.RawMessage) (map[string]json.RawMessage, error) {
	out := maps.Clone(m)
	if out == nil {
		out = map[string]json.RawMessage{}
	}
	if len(path) == 1 {
		if v == nil {
			delete(out, path[0])
		} else {
			out[path[0]] = v
		}
		return out, nil
	}
	var sub map[string]json.RawMessage
	if raw, ok := out[path[0]]; ok {
		if err := json.Unmarshal(raw, &sub); err != nil {
			return nil, fmt.Errorf("%s isn't an object", path[0])
		}
	}
	sub, err := set(sub, path[1:], v)
	if err != nil {
		return nil, err
	}
	b, err := Encode(sub, "")
	if err != nil {
		return nil, err
	}
	out[path[0]] = b
	return out, nil
}

// Encode is JSON without the HTML escaping that would turn a prompt's < and &
// into < and &.
func Encode(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", indent)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Check reports whether claudia would read the file as meant: every known
// field of the right type, brief_every a duration or "off" and workers a
// known place.
func Check(f File) error {
	b, err := Encode(f.top, "")
	if err != nil {
		return err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return fmt.Errorf("%s: want %s, not %s", te.Field, te.Type, te.Value)
		}
		return err
	}
	if err := CheckBriefEvery(c.BriefEveryRaw); err != nil {
		return err
	}
	if err := CheckAutopilot(c.Autopilot); err != nil {
		return err
	}
	return CheckWorkers(c.WorkersRaw)
}

func CheckWorkers(v string) error {
	if v == "" || slices.Contains(WorkerPlaces, strings.ToLower(strings.TrimSpace(v))) {
		return nil
	}
	return fmt.Errorf("workers: %q isn't one of %s", v, strings.Join(WorkerPlaces, ", "))
}

func CheckBriefEvery(v string) error { return CheckDuration("brief_every", v) }

// CheckDuration reads a duration field: like 2h or 30m, or off.
func CheckDuration(key, v string) error {
	if v == "" || v == "off" || v == "0" {
		return nil
	}
	if d, err := time.ParseDuration(v); err != nil || d <= 0 {
		return fmt.Errorf("%s: %q isn't a duration like 2h or 30m, or off", key, v)
	}
	return nil
}

// Save checks f and writes it to config.json through a temp file.
func Save(f File) error {
	if err := Check(f); err != nil {
		return err
	}
	top := f.top
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	b, err := Encode(top, "  ")
	if err != nil {
		return err
	}
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
