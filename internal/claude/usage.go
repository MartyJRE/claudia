package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Price is USD per million tokens.
type Price struct {
	In, Out, Read, Write5m, Write1h float64
}

func price(in, out, read float64) Price {
	return Price{In: in, Out: out, Read: read, Write5m: in * 1.25, Write1h: in * 2}
}

// API list prices; the plan isn't billed per token, so this is what the same
// traffic would cost on the API, the way ccusage reports it.
var prices = map[string]Price{
	"claude-fable-5-1":  price(10, 50, 0.25),
	"claude-mythos-5-1": price(10, 50, 0.25),
	"claude-fable-5":    price(10, 50, 1),
	"claude-mythos-5":   price(10, 50, 1),
	"claude-opus-5-5":   price(4, 20, 0.20),
	"claude-opus-5":     price(5, 25, 0.50),
	"claude-opus-4-8":   price(5, 25, 0.50),
	"claude-opus-4-7":   price(5, 25, 0.50),
	"claude-opus-4-6":   price(5, 25, 0.50),
	"claude-sonnet-5-5": price(2, 10, 0.20),
	"claude-sonnet-5":   price(2, 10, 0.20),
	"claude-sonnet-4-6": price(3, 15, 0.30),
	"claude-haiku-5-5":  price(0.10, 0.50, 0.01),
	"claude-haiku-4-5":  price(1, 5, 0.10),
}

// PriceOf matches the longest known id the model starts with, so dated or
// suffixed ids ("…[1m]", "-20260401") still resolve.
func PriceOf(model string) (Price, bool) {
	best := ""
	for id := range prices {
		if strings.HasPrefix(model, id) && len(id) > len(best) {
			best = id
		}
	}
	p, ok := prices[best]
	return p, ok
}

type Tokens struct {
	Requests int
	Input    int
	Output   int
	Read     int
	Write5m  int
	Write1h  int
}

func (t *Tokens) Add(o Tokens) {
	t.Requests += o.Requests
	t.Input += o.Input
	t.Output += o.Output
	t.Read += o.Read
	t.Write5m += o.Write5m
	t.Write1h += o.Write1h
}

func (t Tokens) Total() int { return t.Input + t.Output + t.Read + t.Write5m + t.Write1h }

func (t Tokens) Cost(model string) (float64, bool) {
	p, ok := PriceOf(model)
	if !ok {
		return 0, false
	}
	return (float64(t.Input)*p.In + float64(t.Output)*p.Out + float64(t.Read)*p.Read +
		float64(t.Write5m)*p.Write5m + float64(t.Write1h)*p.Write1h) / 1e6, true
}

type DayModel struct {
	Day   string // 2006-01-02, local time
	Model string
	Tokens
}

type usageFile struct {
	offset int64
	size   int64
	mtime  time.Time
}

// UsageScanner totals token usage per local day and model, and per session,
// over every transcript, subagents included, reading each file incrementally.
// The first scan reads every transcript on disk, later ones only those changed
// in the last Days.
type UsageScanner struct {
	Profiles []Profile
	Days     int
	files    map[string]*usageFile
	seen     map[string]bool
	agg      map[[2]string]*Tokens
	sess     map[string]*sessionAgg
	scanned  bool
}

type sessionAgg struct {
	profile     string
	ticket      string
	models      map[string]*Tokens
	first, last time.Time
}

func NewUsageScanner(profiles []Profile, days int) *UsageScanner {
	return &UsageScanner{Profiles: profiles, Days: days}
}

type usageLine struct {
	Type      string    `json:"type"`
	GitBranch string    `json:"gitBranch"`
	RequestID string    `json:"requestId"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input         int `json:"input_tokens"`
			Output        int `json:"output_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			Split         *struct {
				M5 int `json:"ephemeral_5m_input_tokens"`
				H1 int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

func (s *UsageScanner) reset() {
	s.files = map[string]*usageFile{}
	s.seen = map[string]bool{}
	s.agg = map[[2]string]*Tokens{}
	s.sess = map[string]*sessionAgg{}
}

func (s *UsageScanner) Scan() []DayModel {
	if s.files == nil {
		s.reset()
	}
	cutoff := time.Now().AddDate(0, 0, -s.Days)
	fileCutoff := cutoff
	if !s.scanned {
		fileCutoff = time.Time{}
	}
	s.scanned = true
	for _, p := range s.Profiles {
		root := filepath.Join(p.Dir, "projects")
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(fileCutoff) {
				return nil
			}
			src := usageSource{session: sessionOf(path), profile: p.Name}
			if !s.feed(path, info, src) {
				// A transcript shrank (rewritten); start over so nothing is counted twice.
				s.reset()
				s.feed(path, info, src)
			}
			return nil
		})
	}
	day := cutoff.Format("2006-01-02")
	var out []DayModel
	for k, t := range s.agg {
		if k[0] >= day {
			out = append(out, DayModel{Day: k[0], Model: k[1], Tokens: *t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day > out[j].Day
		}
		return out[i].Output > out[j].Output
	})
	return out
}

// usageSource is the session a transcript's usage counts toward.
type usageSource struct{ session, profile string }

// sessionOf is a transcript's session id; a subagent's
// <sid>/subagents/agent-*.jsonl counts toward its parent.
func sessionOf(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		return filepath.Base(filepath.Dir(dir))
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

func (s *UsageScanner) feed(path string, info fs.FileInfo, src usageSource) bool {
	f := s.files[path]
	if f == nil {
		f = &usageFile{}
		s.files[path] = f
	}
	if info.Size() == f.size && info.ModTime().Equal(f.mtime) {
		return true
	}
	if info.Size() < f.offset {
		return false
	}
	f.size, f.mtime = info.Size(), info.ModTime()
	fh, err := os.Open(path)
	if err != nil {
		return true
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return true
	}
	r := bufio.NewReaderSize(fh, 256*1024)
	for {
		head, _ := r.Peek(32)
		if len(head) == 0 {
			return true
		}
		if skippable(head) {
			n, complete, _ := discardLine(r)
			if !complete {
				return true
			}
			f.offset += n
			continue
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			return true
		}
		f.offset += int64(len(line))
		s.apply(line, src)
	}
}

func (s *UsageScanner) apply(line []byte, src usageSource) {
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	var e usageLine
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.Message.Model == "" || e.Message.Model == "<synthetic>" {
		return
	}
	sa := s.sess[src.session]
	if sa == nil {
		sa = &sessionAgg{profile: src.profile, models: map[string]*Tokens{}}
		s.sess[src.session] = sa
	}
	// Before the dedup, so a fork still knows its ticket from the lines it copied.
	if k := TicketFrom(e.GitBranch); k != "" {
		sa.ticket = k
	}
	// Each content block of a response is its own line with the same usage, and
	// resumed or forked sessions copy earlier lines: count every response once.
	key := e.Message.ID + ":" + e.RequestID
	if key == ":" || s.seen[key] {
		return
	}
	s.seen[key] = true
	u := e.Message.Usage
	t := Tokens{Requests: 1, Input: u.Input, Output: u.Output, Read: u.CacheRead, Write5m: u.CacheCreation}
	if u.Split != nil && u.Split.M5+u.Split.H1 > 0 {
		t.Write5m, t.Write1h = u.Split.M5, u.Split.H1
	}
	k := [2]string{e.Timestamp.Local().Format("2006-01-02"), e.Message.Model}
	a := s.agg[k]
	if a == nil {
		a = &Tokens{}
		s.agg[k] = a
	}
	a.Add(t)
	mt := sa.models[e.Message.Model]
	if mt == nil {
		mt = &Tokens{}
		sa.models[e.Message.Model] = mt
	}
	mt.Add(t)
	if sa.first.IsZero() || e.Timestamp.Before(sa.first) {
		sa.first = e.Timestamp
	}
	if e.Timestamp.After(sa.last) {
		sa.last = e.Timestamp
	}
}

// Sessions is what each session scanned so far cost. It reads the scanner's
// state, so call it between scans, never during one.
func (s *UsageScanner) Sessions() []SessionCost {
	if s == nil {
		return nil
	}
	var out []SessionCost
	for id, a := range s.sess {
		if len(a.models) == 0 {
			continue
		}
		c := SessionCost{ID: id, Ticket: a.ticket, Profile: a.profile, Models: map[string]Tokens{}, First: a.first, Last: a.last}
		for model, t := range a.models {
			c.Models[model] = *t
		}
		out = append(out, c.priced())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Limits is the plan usage Claude Code passes to the status line; claudia's
// status line hook saves it per profile (see README).
type Limits struct {
	FiveHour   *Window   `json:"five_hour"`
	SevenDay   *Window   `json:"seven_day"`
	SpendLimit *Window   `json:"spend_limit"`
	At         time.Time `json:"-"`
}

type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       any     `json:"resets_at"`
}

// Resets handles both epoch seconds and RFC 3339 strings.
func (w Window) Resets() time.Time {
	switch v := w.ResetsAt.(type) {
	case float64:
		return time.Unix(int64(v), 0)
	case string:
		t, _ := time.Parse(time.RFC3339, v)
		return t
	}
	return time.Time{}
}

func ReadLimits(path string) (Limits, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Limits{}, false
	}
	var raw struct {
		RateLimits Limits  `json:"rate_limits"`
		At         float64 `json:"at"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return Limits{}, false
	}
	l := raw.RateLimits
	l.At = time.Unix(int64(raw.At), 0)
	return l, true
}
