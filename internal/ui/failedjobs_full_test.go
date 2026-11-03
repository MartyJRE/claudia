package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/gitlab"
)

func TestFailedJobsShown(t *testing.T) {
	jobs := []gitlab.Job{{Name: "bump-version", Stage: "git-commit"}, {Name: "lint", Stage: "test"}}
	open := mrRow{MR: gitlab.MR{Pipeline: &gitlab.Pipeline{Status: "failed", Failed: jobs}}}
	if got := plainShell(pipelineText(open)); got != "✗ bump-version +1" {
		t.Errorf("column: %q", got)
	}
	if got := plainShell(failedLine(open)); got != "failed: git-commit › bump-version, test › lint" {
		t.Errorf("detail: %q", got)
	}
	merged := mrRow{MR: gitlab.MR{MergePipeline: &gitlab.Pipeline{Status: "failed", Failed: jobs[:1]}}, merged: true}
	if got := plainShell(pipelineText(merged)); got != "✗ bump-version" {
		t.Errorf("merged column: %q", got)
	}
	if got := plainShell(failedLine(merged)); !strings.HasPrefix(got, "failed after merge: ") {
		t.Errorf("merged detail: %q", got)
	}
	unknown := mrRow{MR: gitlab.MR{Pipeline: &gitlab.Pipeline{Status: "failed"}}}
	if plainShell(pipelineText(unknown)) != "✗ failed" || failedLine(unknown) != "" || failedLine(mrRow{}) != "" {
		t.Error("without job names it says failed, and the detail adds nothing")
	}

	m := newModelShell(t)
	m.switchView(vMine)
	m.gl.Mine[0].Pipeline = &gitlab.Pipeline{Status: "failed", Failed: jobs}
	m.refilter()
	m.cur[vMine].pos = 0
	if s := plainShell(m.View()); !strings.Contains(s, "failed: git-commit › bump-version") {
		t.Errorf("detail panel lacks the failed jobs:\n%s", s)
	}
}
