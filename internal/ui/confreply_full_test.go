package ui

import (
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

func TestConfluenceMentionAnsweredByMyLaterComment(t *testing.T) {
	m := newModelShell(t)
	at := time.Date(2026, 11, 1, 10, 7, 0, 0, time.UTC)
	m.conf.Mentions = []atlassian.Page{{ID: "45", Title: "Re: Bubbles 2.0", Space: "DE", Comment: true, OnPage: "44", Modified: at}}
	open := func() bool {
		for _, x := range m.mentions() {
			if x.id == "conf:45:"+at.Format(time.RFC3339) {
				return m.open(x)
			}
		}
		t.Fatal("mention missing")
		return false
	}
	if !open() {
		t.Fatal("unanswered at first")
	}
	m.conf.Comments = []atlassian.Page{
		{OnPage: "44", Modified: at.Add(-time.Hour)},
		{OnPage: "99", Modified: at.Add(time.Hour)},
		{Modified: at.Add(time.Hour)},
	}
	if !open() {
		t.Error("an older comment, or one on another page, doesn't answer it")
	}
	m.conf.Comments = append(m.conf.Comments, atlassian.Page{OnPage: "44", Modified: at.Add(2 * 24 * time.Hour)})
	if open() {
		t.Error("my later comment on the same page answers the mention")
	}
}
