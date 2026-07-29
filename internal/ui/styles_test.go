package ui

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestResolveBuiltInSchemes(t *testing.T) {
	want := []string{
		"catppuccin-latte",
		"catppuccin-mocha",
		"default",
		"dracula",
		"gruvbox-dark",
		"gruvbox-light",
		"high-contrast",
		"nord",
		"solarized-dark",
		"solarized-light",
		"tokyo-night",
	}
	if got := strings.Join(SchemeNames(), ","); got != strings.Join(want, ",") {
		t.Fatalf("SchemeNames() = %q, want %q", got, strings.Join(want, ","))
	}
	for _, name := range want {
		styles, err := ResolveScheme(name)
		if err != nil {
			t.Fatalf("ResolveScheme(%q): %v", name, err)
		}
		if styles.Name != name {
			t.Fatalf("resolved name = %q, want %q", styles.Name, name)
		}
	}
	styles, err := ResolveScheme("  NoRd ")
	if err != nil || styles.Name != "nord" {
		t.Fatalf("normalized scheme = %q, err = %v", styles.Name, err)
	}
}

func TestResolveSchemeErrorListsValidNames(t *testing.T) {
	_, err := ResolveScheme("midnight")
	if err == nil {
		t.Fatal("expected an unknown scheme error")
	}
	for _, name := range SchemeNames() {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not list %q", err, name)
		}
	}
}

func TestDefaultSchemePreservesOriginalColors(t *testing.T) {
	styles, err := ResolveScheme("")
	if err != nil {
		t.Fatal(err)
	}
	if styles.Scheme.Foreground != nil || styles.Scheme.Background != nil {
		t.Fatalf("default base colors = %#v, %#v; want terminal defaults", styles.Scheme.Foreground, styles.Scheme.Background)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"accent", styles.Scheme.Accent, lipgloss.Color("63")},
		{"muted", styles.Scheme.Muted, lipgloss.Color("244")},
		{"danger", styles.Scheme.Danger, lipgloss.Color("203")},
		{"warning", styles.Scheme.Warning, lipgloss.Color("214")},
		{"success", styles.Scheme.Success, lipgloss.Color("42")},
		{"selection", styles.Scheme.Selection, lipgloss.Color("63")},
		{"selection foreground", styles.Scheme.SelectionForeground, lipgloss.Color("230")},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
}
