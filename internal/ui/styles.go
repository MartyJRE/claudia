package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// One Dark, as in ~/dot/.tmux/plugins/tmux-onedark-theme plus onedark.nvim's accents.
var (
	black       = lipgloss.Color("#282c34")
	bg1         = lipgloss.Color("#31353f")
	visualGrey  = lipgloss.Color("#3e4452")
	commentGrey = lipgloss.Color("#5c6370")
	white       = lipgloss.Color("#aab2bf")
	fg          = lipgloss.Color("#abb2bf")
	blue        = lipgloss.Color("#61afef")
	green       = lipgloss.Color("#98c379")
	yellow      = lipgloss.Color("#e5c07b")
	red         = lipgloss.Color("#e06c75")
	purple      = lipgloss.Color("#c678dd")
	cyan        = lipgloss.Color("#56b6c2")
	orange      = lipgloss.Color("#d19a66")
)

var (
	sText   = lipgloss.NewStyle().Foreground(fg)
	sDim    = lipgloss.NewStyle().Foreground(commentGrey)
	sBold   = lipgloss.NewStyle().Foreground(white).Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(purple).Bold(true)
	sKey    = lipgloss.NewStyle().Foreground(blue).Bold(true)
	sTicket = lipgloss.NewStyle().Foreground(cyan)
	sBranch = lipgloss.NewStyle().Foreground(orange)
	sErr    = lipgloss.NewStyle().Foreground(red)
	sOK     = lipgloss.NewStyle().Foreground(green)

	sTabOn  = lipgloss.NewStyle().Foreground(black).Background(blue).Bold(true).Padding(0, 1)
	sTabOff = lipgloss.NewStyle().Foreground(commentGrey).Background(bg1).Padding(0, 1)

	sBar   = lipgloss.NewStyle().Foreground(purple)
	sRule  = lipgloss.NewStyle().Foreground(visualGrey)
	sPanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(visualGrey).Padding(0, 1)
)

func pill(fgc, bgc lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(fgc).Background(bgc).Bold(true).Padding(0, 1).Render(s)
}

func modelColor(family string) lipgloss.Color {
	switch family {
	case "Opus":
		return purple
	case "Sonnet":
		return blue
	case "Haiku":
		return cyan
	case "Fable":
		return red
	}
	return commentGrey
}

// highlight puts the cursor background behind already-styled text. Each styled
// segment ends with a reset that would also end a plain background, so the
// background is put back after every reset.
func highlight(s string) string {
	bg := lipgloss.NewStyle().Background(visualGrey).Render(" ")
	open, _, _ := strings.Cut(bg, " ")
	if open == "" {
		return s // no colour support
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+open)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+open)
	return open + s + "\x1b[0m"
}
