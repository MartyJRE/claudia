package ui

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/config"
)

// settingsPage edits config.json: simple fields in place, maps and lists as
// JSON in $EDITOR. Saving restarts claudia, since the config is read once.
type settingsPage struct {
	orig, draft config.File
	cur         int // index into config.Fields
	offset      int // first row shown
	editing     bool
	input       textinput.Model
	area        textarea.Model
	discard     bool // esc was pressed once on unsaved changes
}

type settingEditedMsg struct {
	key, path string
	wrote     []byte
	err       error
}

func (m *Model) openSettings() tea.Cmd {
	f, err := config.Raw()
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	area := textarea.New()
	area.ShowLineNumbers = false
	area.Prompt = ""
	area.SetHeight(6)
	area.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	m.settings = &settingsPage{orig: f, draft: f, input: newInput("", ""), area: area}
	m.flash = ""
	return nil
}

func (s *settingsPage) field() config.Field { return config.Fields[s.cur] }

func (s *settingsPage) dirty(key string) bool {
	return !sameJSON(s.orig.Value(key), s.draft.Value(key))
}

func (s *settingsPage) changes() int {
	n := 0
	for _, f := range config.Fields {
		if s.dirty(f.Key) {
			n++
		}
	}
	return n
}

func sameJSON(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// put sets key in the draft, unless the file would no longer read as meant.
func (s *settingsPage) put(key string, raw json.RawMessage) error {
	d, err := s.draft.With(key, raw)
	if err == nil {
		err = config.Check(d)
	}
	if err != nil {
		return err
	}
	s.draft = d
	return nil
}

func (s *settingsPage) unset(key string) {
	if s.draft.Has(key) {
		s.draft = s.draft.Without(key, s.orig)
	}
}

func (s *settingsPage) str(key string) (string, bool) {
	var v string
	raw := s.draft.Value(key)
	return v, raw != nil && json.Unmarshal(raw, &v) == nil
}

// keyProblems are newKeymap's complaints about the draft's key bindings.
func (s *settingsPage) keyProblems() []string {
	var over map[string]config.KeyList
	if raw := s.draft.Value("keys"); raw != nil {
		if err := json.Unmarshal(raw, &over); err != nil {
			return []string{err.Error()}
		}
	}
	_, problems := newKeymap(over)
	return problems
}

func (m *Model) closeSettings() (tea.Model, tea.Cmd) {
	m.settings = nil
	m.flash = ""
	return m, nil
}

func (m *Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	if s.editing {
		return m.updateSettingInput(msg)
	}
	k := msg.String()
	discard := s.discard
	s.discard = false
	switch k {
	case "esc", "q":
		if s.changes() > 0 && !discard {
			s.discard = true
			m.setFlash("unsaved changes: esc again to drop them, ctrl+s to save", true)
			return m, nil
		}
		return m.closeSettings()
	case "j", "down", "ctrl+n":
		s.cur = min(s.cur+1, len(config.Fields)-1)
	case "k", "up", "ctrl+p":
		s.cur = max(s.cur-1, 0)
	case "g", "home":
		s.cur = 0
	case "G", "end":
		s.cur = len(config.Fields) - 1
	case "enter", "e", " ":
		return m, m.editSetting()
	case "d", "backspace":
		f := s.field()
		if f.EnvValue() != "" {
			m.setFlash(f.Env+" sets this; unset it to change it here", true)
			return m, nil
		}
		s.unset(f.Key)
	case "ctrl+s":
		return m.saveSettings()
	}
	return m, nil
}

func (m *Model) editSetting() tea.Cmd {
	s := m.settings
	f := s.field()
	if f.EnvValue() != "" {
		m.setFlash(f.Env+" sets this; unset it to change it here", true)
		return nil
	}
	switch f.Kind {
	case config.KindBool:
		on := f.Default == "on"
		if raw := s.draft.Value(f.Key); raw != nil {
			_ = json.Unmarshal(raw, &on)
		}
		raw, _ := json.Marshal(!on)
		if err := s.put(f.Key, raw); err != nil {
			m.setFlash(err.Error(), true)
		}
		return nil
	case config.KindJSON:
		return m.editSettingJSON(f)
	case config.KindPrompt:
		v, ok := s.str(f.Key)
		if !ok {
			v = f.Default
		}
		s.area.SetValue(v)
		s.editing = true
		return s.area.Focus()
	}
	v, _ := s.str(f.Key)
	s.input.SetValue(v)
	s.input.CursorEnd()
	s.input.Placeholder = f.Default
	s.editing = true
	return s.input.Focus()
}

func (m *Model) updateSettingInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	f := s.field()
	switch msg.String() {
	case "esc":
		s.editing = false
		s.input.Blur()
		s.area.Blur()
		return m, nil
	case "enter":
		v := strings.TrimSpace(s.input.Value())
		if f.Kind == config.KindPrompt {
			v = strings.TrimSpace(s.area.Value())
		}
		switch {
		case v == "", f.Kind == config.KindPrompt && v == f.Default && !s.orig.Has(f.Key):
			s.unset(f.Key)
		default:
			if f.Kind == config.KindDuration {
				if err := config.CheckBriefEvery(v); err != nil {
					m.setFlash(err.Error(), true)
					return m, nil
				}
			}
			raw, _ := config.Encode(v, "")
			if err := s.put(f.Key, raw); err != nil {
				m.setFlash(err.Error(), true)
				return m, nil
			}
		}
		s.editing = false
		s.input.Blur()
		s.area.Blur()
		m.flash = ""
		return m, nil
	}
	var cmd tea.Cmd
	if f.Kind == config.KindPrompt {
		s.area, cmd = s.area.Update(msg)
	} else {
		s.input, cmd = s.input.Update(msg)
	}
	return m, cmd
}

// editSettingJSON hands the field alone to $EDITOR, starting from its example
// when it isn't set.
func (m *Model) editSettingJSON(f config.Field) tea.Cmd {
	raw := m.settings.draft.Value(f.Key)
	if raw == nil {
		raw = json.RawMessage(f.Example)
	}
	var body bytes.Buffer
	if json.Indent(&body, raw, "", "  ") != nil {
		body.Reset()
		body.Write(raw)
	}
	body.WriteByte('\n')
	tmp, err := os.CreateTemp("", "claudia-"+strings.ReplaceAll(f.Key, ".", "-")+"-*.json")
	if err == nil {
		_, err = tmp.Write(body.Bytes())
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	path, wrote := tmp.Name(), body.Bytes()
	return execProcess(editorCmd(path), func(err error) tea.Msg { return settingEditedMsg{f.Key, path, wrote, err} })
}

func (m *Model) settingEdited(msg settingEditedMsg) tea.Cmd {
	b, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	s := m.settings
	if s == nil {
		return nil
	}
	if err := cmp.Or(msg.err, rerr); err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	if bytes.Equal(b, msg.wrote) {
		return nil
	}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || string(t) == "null" {
		s.unset(msg.key)
		return nil
	}
	var v any
	if err := json.Unmarshal(t, &v); err != nil {
		m.setFlash(msg.key+" isn't valid JSON, so it wasn't changed: "+err.Error(), true)
		return nil
	}
	var c bytes.Buffer
	_ = json.Compact(&c, t)
	if err := s.put(msg.key, c.Bytes()); err != nil {
		m.setFlash(err.Error()+", so it wasn't changed", true)
		return nil
	}
	m.flash = ""
	return nil
}

func (m *Model) saveSettings() (tea.Model, tea.Cmd) {
	s := m.settings
	if s.changes() == 0 {
		m.setFlash("nothing to save", false)
		return m, nil
	}
	if s.dirty("keys") {
		if p := s.keyProblems(); len(p) > 0 {
			m.setFlash("not saved: "+p[0], true)
			return m, nil
		}
	}
	if err := config.Save(s.draft); err != nil {
		m.setFlash("not saved: "+err.Error(), true)
		return m, nil
	}
	// The watcher reads brief_every once; the restarted claudia starts a fresh one.
	stopWatcher()
	m.Result.Restart = true
	return m, tea.Quit
}

var (
	sSetMark = lipgloss.NewStyle().Foreground(purple)
	sSetEnv  = lipgloss.NewStyle().Foreground(yellow)
)

// settingValue is how a field's value reads in its row.
func (m *Model) settingValue(f config.Field) string {
	s := m.settings
	if v := f.EnvValue(); v != "" {
		return sText.Render(v)
	}
	raw := s.draft.Value(f.Key)
	if raw == nil {
		if f.Default == "" {
			return sDim.Render("not set")
		}
		return sDim.Render("default: " + oneLine(f.Default))
	}
	var str string
	var on bool
	switch {
	case f.Kind == config.KindBool && json.Unmarshal(raw, &on) == nil:
		if on {
			return sText.Render("on")
		}
		return sText.Render("off")
	case f.Kind != config.KindJSON && json.Unmarshal(raw, &str) == nil:
		return sText.Render(oneLine(str))
	}
	var c bytes.Buffer
	if json.Compact(&c, raw) != nil {
		return sText.Render(oneLine(string(raw)))
	}
	return sText.Render(c.String())
}

// settingsRows lays the fields out under their groups; at says which field
// each row belongs to, a heading to the field under it.
func (m *Model) settingsRows(w int) (rows []string, at []int) {
	s := m.settings
	add := func(r string, i int) { rows, at = append(rows, r), append(at, i) }
	group := ""
	for i, f := range config.Fields {
		if f.Group != group {
			if group != "" {
				add("", i)
			}
			group = f.Group
			add("  "+planHead1.Render(group), i)
		}
		bar, mark := "  ", "  "
		if i == s.cur {
			bar = planRail.Bold(true).Render("▌") + " "
		}
		if s.dirty(f.Key) {
			mark = sSetMark.Render("●") + " "
		}
		label := sText.Render(fmt.Sprintf("%-16s", f.Label))
		if i == s.cur {
			label = sBold.Render(fmt.Sprintf("%-16s", f.Label))
		}
		src := s.draft.Source(f)
		badge := sDim.Render(src)
		if f.EnvValue() != "" {
			badge = sSetEnv.Render(src)
		}
		left := " " + bar + mark + label + " "
		valW := max(8, w-lipgloss.Width(left)-lipgloss.Width(badge)-2)
		value := m.settingValue(f)
		if i == s.cur && s.editing && f.Kind != config.KindPrompt {
			s.input.Width = max(8, valW-2)
			value = s.input.View()
		}
		value = ansi.Truncate(value, valW, "…")
		gap := strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(value)-lipgloss.Width(badge)-1))
		add(left+value+gap+badge, i)
		if i == s.cur && s.editing && f.Kind == config.KindPrompt {
			for _, r := range strings.Split(m.settingsPromptBox(f, w-6), "\n") {
				add("    "+r, i)
			}
		}
		if f.Key == "keys" {
			for _, p := range s.keyProblems() {
				add("      "+sErr.Render("✗ "+p), i)
			}
		}
	}
	return rows, at
}

func (m *Model) settingsPromptBox(f config.Field, w int) string {
	s := m.settings
	s.area.SetWidth(max(10, w-6))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(purple).Padding(0, 1).Width(max(12, w-4))
	hint := sDim.Render("enter save · alt+enter new line · esc cancel · empty for the default")
	return box.Render(planYou.Render(f.Label+" prompt") + "\n" + s.area.View() + "\n" + hint)
}

// settingsHeight is the rows for the fields: the screen less header, rule, help and footer.
func (m *Model) settingsHeight() int { return max(3, m.height-4) }

func (m *Model) viewSettings() string {
	s := m.settings
	w, h := m.width, m.settingsHeight()
	head := planYou.Render("settings") + sDim.Render(" · "+config.Path())
	n := ""
	if c := s.changes(); c > 0 {
		n = sSetMark.Render(fmt.Sprintf("● %d unsaved", c))
	}
	head = " " + head + strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(n)-2)) + n

	rows, at := m.settingsRows(w)
	first, last := -1, -1
	for i, f := range at {
		if f == s.cur {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < s.offset {
		s.offset = first
	}
	if last >= s.offset+h {
		s.offset = last - h + 1
	}
	s.offset = max(0, min(s.offset, len(rows)-h))
	body := rows[s.offset:min(len(rows), s.offset+h)]
	for len(body) < h {
		body = append(body, "")
	}
	for i := range body {
		body[i] = ansi.Truncate(body[i], w, "…")
	}

	f := s.field()
	help := f.Help
	if f.Kind == config.KindJSON {
		help += " · e.g. " + f.Example
	}
	if f.Kind == config.KindPath {
		help += " · ~ is your home"
	}
	keys := func(k, label string) string { return sKey.Render(k) + sDim.Render(" "+label) }
	foot := strings.Join([]string{keys("j/k", "move"), keys("enter", "edit"), keys("d", "default"), keys("ctrl+s", "save and restart"), keys("esc", "close")}, "  ")
	if s.editing {
		foot = strings.Join([]string{keys("enter", "done"), keys("esc", "cancel")}, "  ")
	}
	if m.flash != "" {
		st := sOK
		if m.flashErr {
			st = sErr
		}
		foot = st.Render(m.flash)
	}
	return head + "\n" + sRule.Render(strings.Repeat("─", w)) + "\n" + strings.Join(body, "\n") + "\n" +
		ansi.Truncate(" "+sDim.Render(help), w, "…") + "\n" + ansi.Truncate(" "+foot, w, "…")
}
