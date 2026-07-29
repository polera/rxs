package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

const DefaultScheme = "default"

// Scheme is a palette of semantic colors. Palette values are kept separate
// from rendered styles so every part of the UI uses the same roles.
type Scheme struct {
	Foreground          color.Color
	Background          color.Color
	Accent              color.Color
	Muted               color.Color
	Danger              color.Color
	Warning             color.Color
	Success             color.Color
	Selection           color.Color
	SelectionForeground color.Color
	Link                color.Color
	SearchMatch         color.Color
}

// Styles contains the immutable styles derived from a Scheme.
type Styles struct {
	Name        string
	Scheme      Scheme
	Base        lipgloss.Style
	Selected    lipgloss.Style
	Dim         lipgloss.Style
	Danger      lipgloss.Style
	Warning     lipgloss.Style
	Success     lipgloss.Style
	Link        lipgloss.Style
	SearchMatch lipgloss.Style
}

var schemes = map[string]Scheme{
	DefaultScheme: {
		Accent:              lipgloss.Color("63"),
		Muted:               lipgloss.Color("244"),
		Danger:              lipgloss.Color("203"),
		Warning:             lipgloss.Color("214"),
		Success:             lipgloss.Color("42"),
		Selection:           lipgloss.Color("63"),
		SelectionForeground: lipgloss.Color("230"),
		Link:                lipgloss.Color("63"),
		SearchMatch:         lipgloss.Color("63"),
	},
	"catppuccin-latte": {
		Foreground:          lipgloss.Color("#4c4f69"),
		Background:          lipgloss.Color("#eff1f5"),
		Accent:              lipgloss.Color("#8839ef"),
		Muted:               lipgloss.Color("#8c8fa1"),
		Danger:              lipgloss.Color("#d20f39"),
		Warning:             lipgloss.Color("#df8e1d"),
		Success:             lipgloss.Color("#40a02b"),
		Selection:           lipgloss.Color("#bcc0cc"),
		SelectionForeground: lipgloss.Color("#4c4f69"),
		Link:                lipgloss.Color("#1e66f5"),
		SearchMatch:         lipgloss.Color("#df8e1d"),
	},
	"catppuccin-mocha": {
		Foreground:          lipgloss.Color("#cdd6f4"),
		Background:          lipgloss.Color("#1e1e2e"),
		Accent:              lipgloss.Color("#cba6f7"),
		Muted:               lipgloss.Color("#6c7086"),
		Danger:              lipgloss.Color("#f38ba8"),
		Warning:             lipgloss.Color("#f9e2af"),
		Success:             lipgloss.Color("#a6e3a1"),
		Selection:           lipgloss.Color("#45475a"),
		SelectionForeground: lipgloss.Color("#cdd6f4"),
		Link:                lipgloss.Color("#89b4fa"),
		SearchMatch:         lipgloss.Color("#f9e2af"),
	},
	"dracula": {
		Foreground:          lipgloss.Color("#f8f8f2"),
		Background:          lipgloss.Color("#282a36"),
		Accent:              lipgloss.Color("#bd93f9"),
		Muted:               lipgloss.Color("#6272a4"),
		Danger:              lipgloss.Color("#ff5555"),
		Warning:             lipgloss.Color("#f1fa8c"),
		Success:             lipgloss.Color("#50fa7b"),
		Selection:           lipgloss.Color("#44475a"),
		SelectionForeground: lipgloss.Color("#f8f8f2"),
		Link:                lipgloss.Color("#8be9fd"),
		SearchMatch:         lipgloss.Color("#f1fa8c"),
	},
	"gruvbox-dark": {
		Foreground:          lipgloss.Color("#ebdbb2"),
		Background:          lipgloss.Color("#282828"),
		Accent:              lipgloss.Color("#d79921"),
		Muted:               lipgloss.Color("#928374"),
		Danger:              lipgloss.Color("#fb4934"),
		Warning:             lipgloss.Color("#fabd2f"),
		Success:             lipgloss.Color("#b8bb26"),
		Selection:           lipgloss.Color("#504945"),
		SelectionForeground: lipgloss.Color("#fbf1c7"),
		Link:                lipgloss.Color("#83a598"),
		SearchMatch:         lipgloss.Color("#fabd2f"),
	},
	"gruvbox-light": {
		Foreground:          lipgloss.Color("#3c3836"),
		Background:          lipgloss.Color("#fbf1c7"),
		Accent:              lipgloss.Color("#b57614"),
		Muted:               lipgloss.Color("#928374"),
		Danger:              lipgloss.Color("#9d0006"),
		Warning:             lipgloss.Color("#af3a03"),
		Success:             lipgloss.Color("#79740e"),
		Selection:           lipgloss.Color("#d5c4a1"),
		SelectionForeground: lipgloss.Color("#282828"),
		Link:                lipgloss.Color("#076678"),
		SearchMatch:         lipgloss.Color("#af3a03"),
	},
	// Saturated primaries on pure black; every role clears WCAG AA against the
	// background and selection inverts to black on white.
	"high-contrast": {
		Foreground:          lipgloss.Color("#ffffff"),
		Background:          lipgloss.Color("#000000"),
		Accent:              lipgloss.Color("#00ffff"),
		Muted:               lipgloss.Color("#b0b0b0"),
		Danger:              lipgloss.Color("#ff6b6b"),
		Warning:             lipgloss.Color("#ffd700"),
		Success:             lipgloss.Color("#5cff5c"),
		Selection:           lipgloss.Color("#ffffff"),
		SelectionForeground: lipgloss.Color("#000000"),
		Link:                lipgloss.Color("#87d7ff"),
		SearchMatch:         lipgloss.Color("#ffd700"),
	},
	"nord": {
		Foreground:          lipgloss.Color("#d8dee9"),
		Background:          lipgloss.Color("#2e3440"),
		Accent:              lipgloss.Color("#88c0d0"),
		Muted:               lipgloss.Color("#7b88a1"),
		Danger:              lipgloss.Color("#bf616a"),
		Warning:             lipgloss.Color("#ebcb8b"),
		Success:             lipgloss.Color("#a3be8c"),
		Selection:           lipgloss.Color("#4c566a"),
		SelectionForeground: lipgloss.Color("#eceff4"),
		Link:                lipgloss.Color("#81a1c1"),
		SearchMatch:         lipgloss.Color("#ebcb8b"),
	},
	"solarized-dark": {
		Foreground:          lipgloss.Color("#839496"),
		Background:          lipgloss.Color("#002b36"),
		Accent:              lipgloss.Color("#b58900"),
		Muted:               lipgloss.Color("#586e75"),
		Danger:              lipgloss.Color("#dc322f"),
		Warning:             lipgloss.Color("#b58900"),
		Success:             lipgloss.Color("#859900"),
		Selection:           lipgloss.Color("#073642"),
		SelectionForeground: lipgloss.Color("#eee8d5"),
		Link:                lipgloss.Color("#268bd2"),
		SearchMatch:         lipgloss.Color("#b58900"),
	},
	"solarized-light": {
		Foreground:          lipgloss.Color("#657b83"),
		Background:          lipgloss.Color("#fdf6e3"),
		Accent:              lipgloss.Color("#b58900"),
		Muted:               lipgloss.Color("#93a1a1"),
		Danger:              lipgloss.Color("#dc322f"),
		Warning:             lipgloss.Color("#b58900"),
		Success:             lipgloss.Color("#859900"),
		Selection:           lipgloss.Color("#eee8d5"),
		SelectionForeground: lipgloss.Color("#586e75"),
		Link:                lipgloss.Color("#268bd2"),
		SearchMatch:         lipgloss.Color("#b58900"),
	},
	"tokyo-night": {
		Foreground:          lipgloss.Color("#c0caf5"),
		Background:          lipgloss.Color("#1a1b26"),
		Accent:              lipgloss.Color("#7aa2f7"),
		Muted:               lipgloss.Color("#565f89"),
		Danger:              lipgloss.Color("#f7768e"),
		Warning:             lipgloss.Color("#e0af68"),
		Success:             lipgloss.Color("#9ece6a"),
		Selection:           lipgloss.Color("#283457"),
		SelectionForeground: lipgloss.Color("#c0caf5"),
		Link:                lipgloss.Color("#7dcfff"),
		SearchMatch:         lipgloss.Color("#e0af68"),
	},
}

// SchemeNames returns the canonical built-in scheme names in sorted order.
func SchemeNames() []string {
	names := make([]string, 0, len(schemes))
	for name := range schemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveScheme resolves a scheme name after normalizing whitespace and case.
func ResolveScheme(name string) (Styles, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = DefaultScheme
	}
	scheme, ok := schemes[name]
	if !ok {
		return Styles{}, fmt.Errorf("unknown color scheme %q (valid schemes: %s)", name, strings.Join(SchemeNames(), ", "))
	}
	return stylesFor(name, scheme), nil
}

func stylesFor(name string, scheme Scheme) Styles {
	base := lipgloss.NewStyle()
	if scheme.Foreground != nil {
		base = base.Foreground(scheme.Foreground)
	}
	if scheme.Background != nil {
		base = base.Background(scheme.Background)
	}
	return Styles{
		Name:        name,
		Scheme:      scheme,
		Base:        base,
		Selected:    lipgloss.NewStyle().Foreground(scheme.SelectionForeground).Background(scheme.Selection).Bold(true),
		Dim:         lipgloss.NewStyle().Foreground(scheme.Muted),
		Danger:      lipgloss.NewStyle().Foreground(scheme.Danger),
		Warning:     lipgloss.NewStyle().Foreground(scheme.Warning),
		Success:     lipgloss.NewStyle().Foreground(scheme.Success),
		Link:        lipgloss.NewStyle().Foreground(scheme.Link).Underline(true),
		SearchMatch: lipgloss.NewStyle().Foreground(scheme.SearchMatch).Underline(true),
	}
}

func (s Styles) Pane(title string, active bool, width, height int, content string) string {
	color := s.Scheme.Muted
	if active {
		color = s.Scheme.Accent
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
