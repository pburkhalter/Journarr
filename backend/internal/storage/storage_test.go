package storage

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/pburkhalter/journarr/internal/clients"
	"github.com/pburkhalter/journarr/internal/registry"
)

type fakeArr struct {
	entries []clients.DiskEntry
	roots   []clients.RootFolder
	err     error
	rootErr error
}

func (f *fakeArr) DiskSpace(context.Context) ([]clients.DiskEntry, error) {
	return f.entries, f.err
}
func (f *fakeArr) RootFolders(context.Context) ([]clients.RootFolder, error) {
	return f.roots, f.rootErr
}

// fakeReg liefert die Instanzen direkt — die echte Registry hat private Felder
// und wird hier nicht gebraucht.
type fakeReg []*registry.Instance

func (f fakeReg) WithCapability(c registry.Capability) []*registry.Instance {
	var out []*registry.Instance
	for _, i := range f {
		if i.Caps[c] {
			out = append(out, i)
		}
	}
	return out
}

func regWith(insts ...*registry.Instance) fakeReg { return fakeReg(insts) }

func inst(id string, c any) *registry.Instance {
	return &registry.Instance{
		ID: id, Kind: registry.KindSonarr, Client: c,
		Caps: map[registry.Capability]bool{registry.CapDiskSpace: true},
	}
}

const gb = int64(1) << 30

// Sonarr und Radarr auf demselben Host melden dieselben Mounts. Die duerfen
// nicht doppelt erscheinen, aber beide Melder sollen sichtbar bleiben.
func TestMountsDeduplicatesAcrossInstances(t *testing.T) {
	shared := []clients.DiskEntry{{Path: "/media", FreeSpace: 100 * gb, TotalSpace: 200 * gb}}
	s := &Service{Log: slog.Default(), Reg: regWith(
		inst("sonarr", &fakeArr{entries: shared, roots: []clients.RootFolder{{Path: "/media/tv"}}}),
		inst("radarr", &fakeArr{entries: shared, roots: []clients.RootFolder{{Path: "/media/movies"}}}),
	)}
	got := s.Mounts(context.Background())
	if len(got) != 1 {
		t.Fatalf("erwartet 1 Mount, bekam %d: %+v", len(got), got)
	}
	if len(got[0].ReportedBy) != 2 {
		t.Errorf("ReportedBy = %v, erwartet beide Instanzen", got[0].ReportedBy)
	}
	if got[0].UsedSpace != 100*gb {
		t.Errorf("UsedSpace = %d", got[0].UsedSpace)
	}
	if got[0].UsedPercent != 50 {
		t.Errorf("UsedPercent = %v, erwartet 50", got[0].UsedPercent)
	}
}

// ZFS-Datasets teilen sich den freien Platz, haben aber eigene Quoten. Gleicher
// Pfad mit anderem Total ist deshalb ein echter, eigener Eintrag — zusammen-
// fassen waere falsch.
func TestMountsKeepsSamePathWithDifferentTotals(t *testing.T) {
	s := &Service{Log: slog.Default(), Reg: regWith(
		inst("sonarr", &fakeArr{entries: []clients.DiskEntry{
			{Path: "/media/tv", FreeSpace: 50 * gb, TotalSpace: 400 * gb},
			{Path: "/media/movies", FreeSpace: 50 * gb, TotalSpace: 300 * gb},
		}}),
	)}
	if got := s.Mounts(context.Background()); len(got) != 2 {
		t.Fatalf("erwartet 2 Mounts, bekam %d: %+v", len(got), got)
	}
}

// Ein abgeschalteter Dienst darf die Karte nicht leeren.
func TestMountsSurvivesUnreachableInstance(t *testing.T) {
	s := &Service{Log: slog.Default(), Reg: regWith(
		inst("kaputt", &fakeArr{err: errors.New("connection refused")}),
		inst("ok", &fakeArr{entries: []clients.DiskEntry{{Path: "/media", FreeSpace: gb, TotalSpace: 2 * gb}}}),
	)}
	got := s.Mounts(context.Background())
	if len(got) != 1 || got[0].Path != "/media" {
		t.Fatalf("erwartet den erreichbaren Mount, bekam %+v", got)
	}
}

// Bibliothekspfade zuerst, dann die groessten Volumes.
func TestMountsSortsLibrariesFirst(t *testing.T) {
	s := &Service{Log: slog.Default(), Reg: regWith(
		inst("sonarr", &fakeArr{
			entries: []clients.DiskEntry{
				{Path: "/config", FreeSpace: gb, TotalSpace: 900 * gb},
				{Path: "/media/tv", FreeSpace: gb, TotalSpace: 100 * gb},
			},
			roots: []clients.RootFolder{{Path: "/media/tv/"}}, // Slash darf egal sein
		}),
	)}
	got := s.Mounts(context.Background())
	if len(got) != 2 {
		t.Fatalf("erwartet 2, bekam %d", len(got))
	}
	if !got[0].IsLibrary || got[0].Path != "/media/tv" {
		t.Errorf("Bibliothekspfad muss zuerst kommen, bekam %+v", got[0])
	}
	if got[1].IsLibrary {
		t.Errorf("/config ist keine Bibliothek: %+v", got[1])
	}
}

// Zaehlt die Aufrufe, um den Cache nachzuweisen.
type countingArr struct {
	fakeArr
	calls int
}

func (c *countingArr) DiskSpace(ctx context.Context) ([]clients.DiskEntry, error) {
	c.calls++
	return c.fakeArr.DiskSpace(ctx)
}

func TestMountsCachesWithinTTL(t *testing.T) {
	arr := &countingArr{fakeArr: fakeArr{entries: []clients.DiskEntry{
		{Path: "/media", FreeSpace: gb, TotalSpace: 2 * gb},
	}}}
	clock := time.Unix(1700000000, 0)
	s := &Service{
		Log: slog.Default(), Reg: regWith(inst("sonarr", arr)),
		TTL: 60 * time.Second, Now: func() time.Time { return clock },
	}
	s.Mounts(context.Background())
	s.Mounts(context.Background())
	if arr.calls != 1 {
		t.Errorf("innerhalb der TTL erwartet 1 Abfrage, bekam %d", arr.calls)
	}
	clock = clock.Add(61 * time.Second)
	s.Mounts(context.Background())
	if arr.calls != 2 {
		t.Errorf("nach Ablauf der TTL erwartet 2 Abfragen, bekam %d", arr.calls)
	}
}

// Ein leeres Ergebnis (alle Dienste unerreichbar) darf nicht zementiert werden,
// sonst bleibt die Karte nach der Erholung eine TTL lang leer.
func TestMountsDoesNotCacheEmptyResult(t *testing.T) {
	arr := &countingArr{fakeArr: fakeArr{err: errors.New("down")}}
	clock := time.Unix(1700000000, 0)
	s := &Service{
		Log: slog.Default(), Reg: regWith(inst("sonarr", arr)),
		Now: func() time.Time { return clock },
	}
	s.Mounts(context.Background())
	arr.fakeArr = fakeArr{entries: []clients.DiskEntry{{Path: "/media", FreeSpace: gb, TotalSpace: 2 * gb}}}
	if got := s.Mounts(context.Background()); len(got) != 1 {
		t.Fatalf("nach Erholung erwartet 1 Mount, bekam %+v", got)
	}
}

// Ohne Registry darf nichts knallen.
func TestMountsWithoutRegistry(t *testing.T) {
	s := &Service{Log: slog.Default()}
	if got := s.Mounts(context.Background()); got != nil {
		t.Errorf("erwartet nil, bekam %+v", got)
	}
}
