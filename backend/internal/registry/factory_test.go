package registry

import (
	"testing"
	"time"
)

// legacyLikeSpecs mirrors what config.legacySpecs() produces from the flat env
// vars — the backward-compat path that must keep the exact same 8 services.
func legacyLikeSpecs() []Spec {
	return []Spec{
		{ID: "seerr", Kind: KindSeerr, URL: "http://seerr"},
		{ID: "sonarr", Kind: KindSonarr, URL: "http://sonarr"},
		{ID: "radarr", Kind: KindRadarr, URL: "http://radarr"},
		{ID: "prowlarr", Kind: KindProwlarr, URL: "http://prowlarr"},
		{ID: "arrarr", Kind: KindArrarr, URL: "http://arrarr"},
		{ID: "jellyfin", Kind: KindJellyfin, URL: "http://jellyfin", Extra: map[string]string{"user_id": "u1"}},
		{ID: "waha", Kind: KindWaha, URL: "http://waha"},
		{ID: "notifyarr", Kind: KindNotifyarr, URL: "http://notifyarr"},
	}
}

func TestBuildLegacyBackwardCompat(t *testing.T) {
	reg, err := Build(legacyLikeSpecs(), 5*time.Second)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Same 8 tiles, and every one must be a health check (CapHealth + HealthChecker).
	if got := len(reg.All()); got != 8 {
		t.Fatalf("instances = %d, want 8", got)
	}
	nHealth := 0
	for _, inst := range reg.WithCapability(CapHealth) {
		if _, ok := inst.Client.(HealthChecker); ok {
			nHealth++
		}
	}
	if nHealth != 8 {
		t.Fatalf("health checkers = %d, want 8", nHealth)
	}

	// Order preserves the historical UI order.
	wantOrder := []string{"seerr", "sonarr", "radarr", "prowlarr", "arrarr", "jellyfin", "waha", "notifyarr"}
	for i, inst := range reg.All() {
		if inst.ID != wantOrder[i] {
			t.Errorf("order[%d] = %q, want %q", i, inst.ID, wantOrder[i])
		}
	}

	// Typed accessors resolve.
	if reg.Sonarr() == nil || reg.Radarr() == nil || reg.Seerr() == nil ||
		reg.Jellyfin() == nil || reg.Arrarr() == nil || reg.Notifyarr() == nil {
		t.Fatal("a typed accessor returned nil")
	}
	if reg.Jellyfin().UserID != "u1" {
		t.Errorf("jellyfin UserID = %q, want u1", reg.Jellyfin().UserID)
	}
	if got := reg.MediaArrs(); len(got) != 2 {
		t.Fatalf("MediaArrs = %d, want 2 (sonarr+radarr)", len(got))
	}

	// Capability derivation.
	if s := reg.ByID("sonarr"); !s.Has(CapSearchMissing) || !s.Has(CapSeasonSearch) {
		t.Error("sonarr missing search caps")
	}
	if p := reg.ByID("prowlarr"); p.Has(CapSearchMissing) || !p.Has(CapHealth) {
		t.Error("prowlarr caps wrong (health-only expected)")
	}
	if c := reg.ByID("notifyarr"); !c.Has(CapNotifySend) {
		t.Error("notifyarr missing CapNotifySend")
	}
}

func TestBuildSkipsEmptyURLAndDupes(t *testing.T) {
	reg, err := Build([]Spec{
		{ID: "sonarr", Kind: KindSonarr, URL: "http://s"},
		{ID: "unset", Kind: KindRadarr, URL: ""}, // skipped
	}, time.Second)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(reg.All()) != 1 {
		t.Fatalf("instances = %d, want 1 (empty URL skipped)", len(reg.All()))
	}

	if _, err := Build([]Spec{
		{ID: "dup", Kind: KindSonarr, URL: "http://a"},
		{ID: "dup", Kind: KindRadarr, URL: "http://b"},
	}, time.Second); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

func TestBuildRejectsUnsupportedKind(t *testing.T) {
	if _, err := Build([]Spec{{ID: "x", Kind: Kind("nonesuch"), URL: "http://x"}}, time.Second); err == nil {
		t.Fatal("expected unsupported-kind error")
	}
}

func TestTdarrProvidesTranscodeStage(t *testing.T) {
	reg, err := Build([]Spec{{ID: "tdarr", Kind: KindTdarr, URL: "http://tdarr:8265"}}, time.Second)
	if err != nil {
		t.Fatalf("Build tdarr: %v", err)
	}
	tdarr := reg.ByID("tdarr")
	if tdarr == nil || !tdarr.Has(CapTranscodeStage) {
		t.Fatal("tdarr should provide CapTranscodeStage")
	}
	if !reg.StageActive("transcode") {
		t.Error("transcode stage should be active when a Tdarr instance exists")
	}
	// Without Tdarr, transcode is gated off.
	empty, _ := Build([]Spec{{ID: "sonarr", Kind: KindSonarr, URL: "http://s"}}, time.Second)
	if empty.StageActive("transcode") {
		t.Error("transcode stage should be gated off without a Tdarr instance")
	}
}

func TestExplicitCapsOverrideDefaults(t *testing.T) {
	reg, err := Build([]Spec{
		{ID: "sonarr", Kind: KindSonarr, URL: "http://s", Caps: []Capability{CapHealth}},
	}, time.Second)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s := reg.ByID("sonarr"); s.Has(CapSearchMissing) || !s.Has(CapHealth) {
		t.Error("explicit Caps should override defaults (health-only)")
	}
}

func TestSecondInstanceOfKind(t *testing.T) {
	reg, err := Build([]Spec{
		{ID: "sonarr", Kind: KindSonarr, URL: "http://s1"},
		{ID: "sonarr-4k", Kind: KindSonarr, URL: "http://s2", Order: 25},
	}, time.Second)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := len(reg.ByKind(KindSonarr)); got != 2 {
		t.Fatalf("sonarr instances = %d, want 2", got)
	}
	if got := len(reg.MediaArrs()); got != 2 {
		t.Fatalf("MediaArrs = %d, want 2", got)
	}
	// Sonarr() returns the first by order.
	if reg.ByKind(KindSonarr)[0].ID != "sonarr" {
		t.Errorf("first sonarr = %q, want sonarr", reg.ByKind(KindSonarr)[0].ID)
	}
}
