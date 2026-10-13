package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestRegisterAndApply(t *testing.T) {
	defer Apply(Default)
	if err := Register(map[string]map[string]string{"noc": {"base": "nord", "accent": "#ffaf00"}}); err != nil {
		t.Fatal(err)
	}
	if Themes["noc"]["accent"] != "#ffaf00" || Themes["noc"]["bg"] != Themes["nord"]["bg"] {
		t.Fatal(Themes["noc"])
	}
	if err := Register(map[string]map[string]string{"x": {"acent": "red"}}); err == nil {
		t.Fatal("typo accepted")
	}
	if err := Apply("noc"); err != nil || Accent != tcell.GetColor("#ffaf00") || Current() != "noc" {
		t.Fatalf("apply: %v %v %s", err, Accent, Current())
	}
	// "k9s" is the default theme under the name opensips-tui used
	if err := Apply("k9s"); err != nil || Current() != Default {
		t.Fatalf("k9s alias: %v %s", err, Current())
	}
	if Apply("nope") == nil {
		t.Fatal("unknown theme accepted")
	}
}

// In every theme the focused button is readable and differs from the others.
func TestButtonStylesDistinct(t *testing.T) {
	defer Apply(Default)
	for _, n := range Names() {
		if err := Apply(n); err != nil {
			t.Fatal(err)
		}
		afg, abg, _ := ButtonActivatedStyle().Decompose()
		_, bbg, _ := ButtonStyle().Decompose()
		if afg == abg {
			t.Errorf("%s: focused button text has the color of its background (%v)", n, afg)
		}
		if abg == bbg {
			t.Errorf("%s: focused and other buttons share the background %v", n, abg)
		}
	}
}
