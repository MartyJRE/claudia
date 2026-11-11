package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAutopilotDefaults(t *testing.T) {
	var a Autopilot
	if a.Enabled || a.Mode("plan") != ModeAuto || a.Mode("implement") != ModeAfterPlanOK || a.Mode("merge") != ModeAsk || a.Mode("findings") != ModeAsk {
		t.Error("modes")
	}
	if a.PermissionMode() != "auto" {
		t.Error("permission mode")
	}
	a.PermissionModeRaw = "bypassPermissions"
	if a.PermissionMode() != "bypassPermissions" {
		t.Error("set permission mode")
	}
	a.PermissionModeRaw = ""
	if !a.ReviewApprove() || a.ReviewReact() != "✅" || a.MaxRunning() != 1 || a.ReviewEvery() != time.Hour {
		t.Error("review and running")
	}
	if a.CommentNote() != "" || (Autopilot{CommentNoteRaw: " By Claude. "}).CommentNote() != "By Claude." {
		t.Error("comment note")
	}
	if i := slices.IndexFunc(Fields, func(f Field) bool { return f.Key == "autopilot.comment_note" }); i < 0 || Fields[i].Default != "none" {
		t.Error("no comment note setting")
	}
	for _, p := range []string{defaultReviewPrompt, defaultImplementationPrompt, defaultAnalysisPrompt, defaultReadinessPrompt, defaultMRPrompt, defaultQAPrompt} {
		if strings.Contains(p, "(claudia)") {
			t.Errorf("a default prompt still says (claudia): %s", p)
		}
	}
	if a.MaxFiveHour() != 60 || a.MaxFiveHourAfterHours() != 90 || a.MaxSevenDay() != 80 || a.MaxTicketUSD() != 50 || a.MaxDayUSD() != 30 {
		t.Error("caps")
	}
	if from, to := a.WorkHours(); from != 9*60 || to != 18*60 {
		t.Errorf("hours %d-%d", from, to)
	}
	if !a.Workday(time.Monday) || a.Workday(time.Sunday) || a.Workday(time.Saturday) {
		t.Error("days")
	}
}

func TestAutopilotSet(t *testing.T) {
	no, none, three, half := false, "", 3, 12.5
	a := Autopilot{
		Steps:            map[string]string{"implement": "ask", "qa": "off", "plan": "after-plan-ok", "mr": "bogus"},
		ReviewApproveRaw: &no, ReviewReactRaw: &none, MaxRunningRaw: &three, MaxDayUSDRaw: &half,
		Hours: "07:30-20:00", Days: []string{"Saturday", "sun"}, ReviewEveryRaw: "30m",
	}
	// A mode a step can't take falls back to its default.
	if a.Mode("implement") != ModeAsk || a.Mode("qa") != ModeOff || a.Mode("plan") != ModeAuto || a.Mode("mr") != ModeAuto {
		t.Error("modes")
	}
	if a.ReviewApprove() || a.ReviewReact() != "" || a.MaxRunning() != 3 || a.MaxDayUSD() != 12.5 || a.ReviewEvery() != 30*time.Minute {
		t.Error("set values")
	}
	if from, to := a.WorkHours(); from != 7*60+30 || to != 20*60 {
		t.Errorf("hours %d-%d", from, to)
	}
	if a.Workday(time.Monday) || !a.Workday(time.Saturday) || !a.Workday(time.Sunday) {
		t.Error("days")
	}
	a.Hours, a.ReviewEveryRaw = "late", "off"
	zero := 0
	a.MaxRunningRaw = &zero
	if from, _ := a.WorkHours(); from != 9*60 || a.ReviewEvery() != 0 || a.MaxRunning() != 1 {
		t.Error("broken values fall back")
	}
}

func TestCheckAutopilot(t *testing.T) {
	for _, c := range []struct {
		a   Autopilot
		err string
	}{
		{Autopilot{Steps: map[string]string{"implement": "after-plan-ok", "merge": "auto"}, Days: []string{"mon"}, Hours: "8:00-17:00", ReviewEveryRaw: "2h"}, ""},
		{Autopilot{Steps: map[string]string{"deploy": "auto"}}, `"deploy" isn't a step`},
		{Autopilot{Steps: map[string]string{"plan": "sometimes"}}, `autopilot.steps.plan: "sometimes" isn't one of`},
		{Autopilot{Steps: map[string]string{"plan": "after-plan-ok"}}, "only applies to implement"},
		{Autopilot{Hours: "nine to six"}, "isn't like 09:00-18:00"},
		{Autopilot{Hours: "09:00-late"}, "isn't like 09:00-18:00"},
		{Autopilot{Hours: "18:00-09:00"}, "ends before it starts"},
		{Autopilot{Days: []string{"someday"}}, `"someday" isn't a day`},
		{Autopilot{Days: []string{"mo"}}, `"mo" isn't a day`},
		{Autopilot{ReviewEveryRaw: "hourly"}, "autopilot.review_every"},
		{Autopilot{PermissionModeRaw: "yolo"}, "autopilot.permission_mode"},
		{Autopilot{PermissionModeRaw: "acceptEdits"}, ""},
	} {
		err := CheckAutopilot(c.a)
		if (c.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%+v: %v, want %q", c.a, err, c.err)
		}
	}
	f, err := File{}.With("autopilot.hours", []byte(`"x"`))
	if err == nil {
		err = Check(f)
	}
	if err == nil || !strings.Contains(err.Error(), "autopilot.hours") {
		t.Errorf("Check reads the autopilot: %v", err)
	}
}

func TestReviewStaleAfter(t *testing.T) {
	for raw, want := range map[string]time.Duration{"": 30 * 24 * time.Hour, "336h": 336 * time.Hour, "off": 0, "nonsense": 30 * 24 * time.Hour} {
		if got := (Autopilot{ReviewStaleAfterRaw: raw}).ReviewStaleAfter(); got != want {
			t.Errorf("%q: %v, want %v", raw, got, want)
		}
	}
	if err := CheckAutopilot(Autopilot{ReviewStaleAfterRaw: "a month"}); err == nil || !strings.Contains(err.Error(), "review_stale_after") {
		t.Errorf("a bad duration: %v", err)
	}
}
