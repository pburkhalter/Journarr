package actions

import (
	"context"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/registry"
)

func TestAvailableGlobalDerivesFromCaps(t *testing.T) {
	reg, err := registry.Build([]registry.Spec{
		{ID: "sonarr", Kind: registry.KindSonarr, URL: "http://s"},
		{ID: "sonarr-4k", Kind: registry.KindSonarr, URL: "http://s2"},
		{ID: "radarr", Kind: registry.KindRadarr, URL: "http://r"},
		{ID: "jellyfin", Kind: registry.KindJellyfin, URL: "http://j"},
		{ID: "prowlarr", Kind: registry.KindProwlarr, URL: "http://p"}, // no action caps
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := &Actions{Reg: reg}
	got := a.Available(context.Background(), "global", 0)

	var searchMissing, libraryScan int
	for _, d := range got {
		switch d.Kind {
		case "search-missing":
			searchMissing++
			if d.InstanceID == "" {
				t.Error("search-missing descriptor missing instance_id")
			}
		case "library-scan":
			libraryScan++
		}
	}
	if searchMissing != 3 { // two sonarrs + radarr
		t.Errorf("search-missing = %d, want 3", searchMissing)
	}
	if libraryScan != 1 {
		t.Errorf("library-scan = %d, want 1", libraryScan)
	}
}

func TestExecuteUnknownKindErrors(t *testing.T) {
	a := &Actions{}
	if err := a.Execute(context.Background(), "bogus", nil); err == nil {
		t.Fatal("expected error for unknown action kind")
	}
}

func TestParamCoercion(t *testing.T) {
	// JSON numbers arrive as float64.
	if got := pInt(map[string]any{"n": float64(42)}, "n"); got != 42 {
		t.Errorf("pInt float64 = %d, want 42", got)
	}
	if got := pStr(map[string]any{"s": "x"}, "s"); got != "x" {
		t.Errorf("pStr = %q, want x", got)
	}
	if got := pInt(map[string]any{}, "missing"); got != 0 {
		t.Errorf("pInt missing = %d, want 0", got)
	}
}
