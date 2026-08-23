package clients

import (
	"context"
	"fmt"
)

// DiskEntry is one mount as an arr reports it. Deliberately taken from the arr
// APIs rather than the host: that keeps the feature portable — it works the same
// on TrueNAS, Unraid, a Synology or bare metal, and needs no privileged access
// to the machine Journarr runs on.
type DiskEntry struct {
	Path       string `json:"path"`
	Label      string `json:"label,omitempty"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// DiskSpace returns every mount the arr can see (/api/v3/diskspace). Present on
// Sonarr, Radarr, Lidarr and Readarr; Prowlarr has no media paths and answers
// 404, which is why the capability is declared per kind.
func (c *Arr) DiskSpace(ctx context.Context) ([]DiskEntry, error) {
	var out []DiskEntry
	_, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/diskspace", c.BaseURL, c.APIBase), c.headers(), &out)
	return out, err
}

// RootFolder is a configured library path.
type RootFolder struct {
	Path      string `json:"path"`
	FreeSpace int64  `json:"freeSpace"`
}

// RootFolders lists the arr's library roots. Used to mark which mounts actually
// hold media, so the UI can separate them from incidental container mounts
// (/config, /) that /diskspace also reports.
func (c *Arr) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	_, err := getJSON(ctx, c.HTTP,
		fmt.Sprintf("%s%s/rootfolder", c.BaseURL, c.APIBase), c.headers(), &out)
	return out, err
}
