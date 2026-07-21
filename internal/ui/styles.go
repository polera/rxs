package ui

import "charm.land/lipgloss/v2"

var (
	Accent   = lipgloss.Color("63")
	Muted    = lipgloss.Color("244")
	Danger   = lipgloss.Color("203")
	Success  = lipgloss.Color("42")
	Selected = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(Accent).Bold(true)
	Dim      = lipgloss.NewStyle().Foreground(Muted)
)

func Pane(title string, active bool, width, height int, content string) string {
	color := Muted
	if active {
		color = Accent
	}
	border := lipgloss.RoundedBorder()
	border.Top = "─"
	style := lipgloss.NewStyle().
		Border(border).
		BorderForeground(color).
		Width(max(1, width-2)).
		Height(max(1, height-2)).
		MaxWidth(width).
		MaxHeight(height)
	label := lipgloss.NewStyle().Foreground(color).Bold(active).Render(" " + title + " ")
	return style.Render(label + "\n" + content)
}
