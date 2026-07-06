package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pburkhalter/journarr/internal/pipeline"
)

// Tolerant subsets of Sonarr v4 / Radarr v5+ webhook payloads. eventType is
// PascalCase; everything else camelCase. Neither app sends failure webhooks —
// failures arrive via the history poller.

type arrWebhookCommon struct {
	EventType          string `json:"eventType"`
	DownloadID         string `json:"downloadId"`
	DownloadClientType string `json:"downloadClientType"`
	IsUpgrade          bool   `json:"isUpgrade"`
	Release            *struct {
		ReleaseTitle string `json:"releaseTitle"`
		Indexer      string `json:"indexer"`
		Size         int64  `json:"size"`
	} `json:"release"`
	DownloadStatusMessages []struct {
		Title    string   `json:"title"`
		Messages []string `json:"messages"`
	} `json:"downloadStatusMessages"`
}

type sonarrWebhook struct {
	arrWebhookCommon
	Series *struct {
		ID     int64  `json:"id"`
		Title  string `json:"title"`
		TvdbID int64  `json:"tvdbId"`
		TmdbID int64  `json:"tmdbId"`
	} `json:"series"`
	Episodes []struct {
		ID            int64  `json:"id"`
		SeasonNumber  int64  `json:"seasonNumber"`
		EpisodeNumber int64  `json:"episodeNumber"`
		Title         string `json:"title"`
	} `json:"episodes"`
	EpisodeFile *struct {
		Path string `json:"path"`
	} `json:"episodeFile"`
	EpisodeFiles []struct {
		Path string `json:"path"`
	} `json:"episodeFiles"`
}

type radarrWebhook struct {
	arrWebhookCommon
	Movie *struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		Year     int64  `json:"year"`
		TmdbID   int64  `json:"tmdbId"`
		FilePath string `json:"filePath"`
	} `json:"movie"`
	MovieFile *struct {
		Path string `json:"path"`
	} `json:"movieFile"`
}

func protocolFromClientType(t string) string {
	switch {
	case strings.EqualFold(t, "sabnzbd"), strings.EqualFold(t, "nzbget"):
		return "usenet"
	case strings.Contains(strings.ToLower(t), "torrent"), strings.EqualFold(t, "deluge"),
		strings.EqualFold(t, "transmission"), strings.EqualFold(t, "rtorrent"):
		return "torrent"
	}
	return ""
}

func (h *Handler) handleSonarr(body []byte) error {
	var p sonarrWebhook
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if p.Series == nil {
		if p.EventType == "Test" {
			h.Log.Info("ingest: sonarr test received")
			return nil
		}
		return fmt.Errorf("sonarr: %s without series", p.EventType)
	}
	series := &pipeline.SeriesRef{
		SonarrID: p.Series.ID, TvdbID: p.Series.TvdbID, TmdbID: p.Series.TmdbID, Title: p.Series.Title,
	}
	episodes := make([]pipeline.EpisodeRef, 0, len(p.Episodes))
	for _, ep := range p.Episodes {
		episodes = append(episodes, pipeline.EpisodeRef{
			SonarrID: ep.ID, Season: ep.SeasonNumber, Episode: ep.EpisodeNumber, Title: ep.Title,
		})
	}

	switch p.EventType {
	case "Test":
		h.Log.Info("ingest: sonarr test received")
		return nil
	case "Grab":
		op := pipeline.GrabOp{
			Arr: "sonarr", DownloadID: p.DownloadID,
			Protocol: protocolFromClientType(p.DownloadClientType),
			Series:   series, Episodes: episodes,
		}
		if p.Release != nil {
			op.ReleaseTitle, op.Indexer, op.Size = p.Release.ReleaseTitle, p.Release.Indexer, p.Release.Size
		}
		return h.emit("sonarr", "grab", "", op)
	case "Download":
		op := pipeline.ImportOp{
			Arr: "sonarr", DownloadID: p.DownloadID,
			Series: series, Episodes: episodes, IsUpgrade: p.IsUpgrade,
			EpisodePaths: map[int64]string{},
		}
		// Single-file import carries episodeFile; the ImportComplete variant
		// carries episodeFiles[] without a per-episode mapping — only map
		// when it is unambiguous.
		if p.EpisodeFile != nil && len(episodes) == 1 {
			op.EpisodePaths[episodes[0].SonarrID] = p.EpisodeFile.Path
		} else if len(p.EpisodeFiles) == 1 && len(episodes) == 1 {
			op.EpisodePaths[episodes[0].SonarrID] = p.EpisodeFiles[0].Path
		}
		return h.emit("sonarr", "import", "", op)
	case "ManualInteractionRequired":
		msg := "manual interaction required"
		if len(p.DownloadStatusMessages) > 0 && len(p.DownloadStatusMessages[0].Messages) > 0 {
			msg = p.DownloadStatusMessages[0].Messages[0]
		}
		op := pipeline.FailureOp{
			Arr: "sonarr", DownloadID: p.DownloadID, Message: msg,
			Series: series, Episodes: episodes, Soft: true,
		}
		return h.emit("sonarr", "failure", "", op)
	default:
		h.Log.Debug("ingest: sonarr event ignored", "type", p.EventType)
		return nil
	}
}

func (h *Handler) handleRadarr(body []byte) error {
	var p radarrWebhook
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if p.Movie == nil {
		if p.EventType == "Test" {
			h.Log.Info("ingest: radarr test received")
			return nil
		}
		return fmt.Errorf("radarr: %s without movie", p.EventType)
	}
	movie := &pipeline.MovieRef{
		RadarrID: p.Movie.ID, TmdbID: p.Movie.TmdbID, Title: p.Movie.Title, Year: p.Movie.Year,
	}

	switch p.EventType {
	case "Test":
		h.Log.Info("ingest: radarr test received")
		return nil
	case "Grab":
		op := pipeline.GrabOp{
			Arr: "radarr", DownloadID: p.DownloadID,
			Protocol: protocolFromClientType(p.DownloadClientType),
			Movie:    movie,
		}
		if p.Release != nil {
			op.ReleaseTitle, op.Indexer, op.Size = p.Release.ReleaseTitle, p.Release.Indexer, p.Release.Size
		}
		return h.emit("radarr", "grab", "", op)
	case "Download":
		op := pipeline.ImportOp{
			Arr: "radarr", DownloadID: p.DownloadID, Movie: movie, IsUpgrade: p.IsUpgrade,
		}
		if p.MovieFile != nil {
			op.MoviePath = p.MovieFile.Path
		} else if p.Movie.FilePath != "" {
			op.MoviePath = p.Movie.FilePath
		}
		return h.emit("radarr", "import", "", op)
	case "ManualInteractionRequired":
		msg := "manual interaction required"
		if len(p.DownloadStatusMessages) > 0 && len(p.DownloadStatusMessages[0].Messages) > 0 {
			msg = p.DownloadStatusMessages[0].Messages[0]
		}
		op := pipeline.FailureOp{Arr: "radarr", DownloadID: p.DownloadID, Message: msg, Movie: movie, Soft: true}
		return h.emit("radarr", "failure", "", op)
	default:
		h.Log.Debug("ingest: radarr event ignored", "type", p.EventType)
		return nil
	}
}
