package config

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRawSaveRoundTrip(t *testing.T) {
	fresh(t, `{
		"work_dir": "Job",
		"vault": "~/Notes",
		"future_key": {"keep": [1, 2.50]},
		"teams": {"tenant": "t", "extra": true}
	}`)
	f, err := Raw()
	if err != nil {
		t.Fatal(err)
	}
	if f, err = f.With("work_dir", []byte(`"Corp"`)); err != nil {
		t.Fatal(err)
	}
	if f, err = f.With("teams.mail.direct", []byte(`false`)); err != nil {
		t.Fatal(err)
	}
	if f, err = f.With("review_prompt", []byte(`"a < b & c"`)); err != nil {
		t.Fatal(err)
	}
	if err := Save(f); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	s := string(b)
	for _, want := range []string{`"work_dir": "Corp"`, `"vault": "~/Notes"`, `"keep": [`, `2.50`, `"extra": true`, `"tenant": "t"`, `"direct": false`, `"a < b & c"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	for _, bad := range []string{"code_root", "gitlab_host", "01 - Projects", "2h"} {
		if strings.Contains(s, bad) {
			t.Errorf("a default was written (%s):\n%s", bad, s)
		}
	}
	if _, err := os.Stat(Path() + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
	again, err := Raw()
	if err != nil || string(again.Value("teams.mail.direct")) != "false" || strings.Join(strings.Fields(string(again.Value("future_key.keep"))), "") != "[1,2.50]" {
		t.Errorf("read back: %v %s %s", err, again.Value("teams.mail.direct"), again.Value("future_key.keep"))
	}
}

func TestRawMissingAndBroken(t *testing.T) {
	fresh(t, "")
	if f, err := Raw(); err != nil || f.Has("work_dir") {
		t.Errorf("missing file: %v", err)
	}
	if err := Save(File{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path()); string(b) != "{}\n" {
		t.Errorf("empty save wrote %q", b)
	}
	fresh(t, `{not json`)
	if _, err := Raw(); err == nil {
		t.Error("a broken file must not read as empty, or saving would clobber it")
	}
}

func TestSaveRejects(t *testing.T) {
	for raw, want := range map[string]string{
		`{"brief_every": "daily"}`:           "brief_every",
		`{"brief_every": "-1h"}`:             "brief_every",
		`{"profiles": ["work"]}`:             "profiles",
		`{"rounded_pills": "yes"}`:           "rounded_pills",
		`{"teams": {"mail": {"rules": {}}}}`: "rules",
	} {
		fresh(t, raw)
		f, err := Raw()
		if err != nil {
			t.Fatal(err)
		}
		if err := Save(f); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", raw, err)
		}
		if b, _ := os.ReadFile(Path()); string(b) != raw {
			t.Errorf("%s: a rejected save still wrote %s", raw, b)
		}
	}
	for _, ok := range []string{"", "off", "0", "45m"} {
		if err := CheckBriefEvery(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

func TestWithout(t *testing.T) {
	fresh(t, `{"teams": {"tenant": "t"}}`)
	base, _ := Raw()
	f, _ := base.With("teams.mail.direct", []byte("false"))
	if got := f.Without("teams.mail.direct", base); got.Has("teams.mail") || !got.Has("teams.tenant") {
		t.Errorf("a mail object made only for direct goes with it: %s", got.Value("teams"))
	}
	fresh(t, `{"teams": {"mail": {"direct": false}}}`)
	base, _ = Raw()
	if got := base.Without("teams.mail.direct", base); string(got.Value("teams.mail")) != "{}" {
		t.Errorf("mail that was on stays on: %s", got.Value("teams"))
	}
}

func TestSource(t *testing.T) {
	fresh(t, `{"work_dir": "Job"}`)
	f, _ := Raw()
	field := func(k string) Field {
		return Fields[slices.IndexFunc(Fields, func(f Field) bool { return f.Key == k })]
	}
	if s := f.Source(field("work_dir")); s != "file" {
		t.Errorf("work_dir: %s", s)
	}
	if s := f.Source(field("vault")); s != "default" {
		t.Errorf("vault: %s", s)
	}
	t.Setenv("CLAUDIA_PREFER", "Corp")
	if s := f.Source(field("work_dir")); s != "env CLAUDIA_PREFER" {
		t.Errorf("work_dir under env: %s", s)
	}
}

// The settings page offers exactly the keys Config reads, so it can't drift from the struct.
func TestFieldsMatchConfig(t *testing.T) {
	var keys []string
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for i := range t.NumField() {
			tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			if tag == "" || tag == "-" {
				continue
			}
			ft := t.Field(i).Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				walk(ft, prefix+tag+".")
				continue
			}
			keys = append(keys, prefix+tag)
		}
	}
	walk(reflect.TypeOf(Config{}), "")
	var fields []string
	for _, f := range Fields {
		fields = append(fields, f.Key)
		if f.Kind == KindBool && f.Default != "on" && f.Default != "off" {
			t.Errorf("%s: a bool defaults to on or off", f.Key)
		}
	}
	slices.Sort(keys)
	slices.Sort(fields)
	if !slices.Equal(keys, fields) {
		t.Errorf("config keys %v\nfields      %v", keys, fields)
	}
}

func TestFieldEnvsOverrideGet(t *testing.T) {
	for _, f := range Fields {
		if f.Env == "" {
			continue
		}
		fresh(t, "")
		t.Setenv(f.Env, "ENVVAL")
		c := reflect.ValueOf(Get())
		found := false
		for i := range c.NumField() {
			v := c.Field(i)
			switch {
			case v.Kind() == reflect.String && v.String() == "ENVVAL":
				found = true
			case v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.String && v.Len() == 1 && v.Index(0).String() == "ENVVAL":
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %s doesn't override anything in Get", f.Key, f.Env)
		}
	}
}
