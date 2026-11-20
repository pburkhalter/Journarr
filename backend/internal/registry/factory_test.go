package registry

import (
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
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

// An instance id is free text; Sonarr vs Radarr decisions (history include
// flags, op.Arr, the downloads CHECK) must come from the kind.
func TestArrTypeComesFromKindNotID(t *testing.T) {
	reg, err := Build([]Spec{
		{ID: "tv-main", Kind: KindSonarr, URL: "http://s1"},
		{ID: "films", Kind: KindRadarr, URL: "http://r1"},
		{ID: "indexers", Kind: KindProwlarr, URL: "http://p1"},
	}, time.Second)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for id, want := range map[string]string{"tv-main": "sonarr", "films": "radarr", "indexers": "prowlarr"} {
		a, ok := reg.ByID(id).Client.(*clients.Arr)
		if !ok {
			t.Fatalf("%s: client is %T, want *clients.Arr", id, reg.ByID(id).Client)
		}
		if a.Type() != want || a.Name != id {
			t.Errorf("%s: Type()=%q Name=%q, want %q/%q", id, a.Type(), a.Name, want, id)
		}
	}
	if got := reg.ByID("indexers").Client.(*clients.Arr).APIBase; got != "/api/v1" {
		t.Errorf("prowlarr APIBase = %q, want /api/v1", got)
	}
}

// The aggregator reaches the instances through a runtime type assertion, the
// only part of the wiring the compiler does not check: an instance with
// CapDiskSpace whose client does not implement the interface would be skipped
// silently and the card would stay empty.
func TestDiskSpaceCapableInstancesImplementReporter(t *testing.T) {
	reg, err := Build([]Spec{
		{ID: "sonarr", Kind: KindSonarr, URL: "http://sonarr:8989", APIKey: "x"},
		{ID: "radarr", Kind: KindRadarr, URL: "http://radarr:7878", APIKey: "x"},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.WithCapability(CapDiskSpace)
	if len(got) != 2 {
		t.Fatalf("erwartet 2 Instanzen mit %s, bekam %d", CapDiskSpace, len(got))
	}
	for _, inst := range got {
		if _, ok := inst.Client.(DiskSpaceReporter); !ok {
			t.Errorf("%s (%s) hat %s, erfuellt DiskSpaceReporter aber nicht",
				inst.ID, inst.Kind, CapDiskSpace)
		}
	}
}
