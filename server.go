package selfupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Server exposes update functionality via REST API.
type Server struct {
	updater *Updater
	mux     *http.ServeMux
}

// NewServer creates an update API server wrapping the given Updater.
func NewServer(updater *Updater) *Server {
	s := &Server{
		updater: updater,
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("/api/version", s.handleVersion)
	s.mux.HandleFunc("/api/check", s.handleCheck)
	s.mux.HandleFunc("/api/update", s.handleUpdate)
	s.mux.HandleFunc("/api/progress", s.handleProgress)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Handler returns the http.Handler for embedding in existing servers.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version":  s.updater.current,
		"platform": CurrentPlatform(),
		"exe_path": mustExePathStr(),
	})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	release, err := s.updater.Check()
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"error":      true,
			"message":    err.Error(),
			"current":    s.updater.current,
			"has_update": false,
		})
		return
	}

	if release == nil {
		writeJSON(w, map[string]interface{}{
			"current":    s.updater.current,
			"has_update": false,
			"latest":     s.updater.current,
		})
		return
	}

	asset, _ := release.AssetForCurrentPlatform()
	assetInfo := map[string]interface{}{}
	if asset != nil {
		assetInfo = map[string]interface{}{
			"url":    asset.URL,
			"sha256": asset.SHA256,
			"size":   asset.Size,
		}
	}

	writeJSON(w, map[string]interface{}{
		"current":    s.updater.current,
		"has_update": true,
		"latest":     release.Version,
		"date":       release.Date,
		"asset":      assetInfo,
	})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	release, err := s.updater.Check()
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"error":   true,
			"message": fmt.Sprintf("check failed: %v", err),
		})
		return
	}

	if release == nil {
		writeJSON(w, map[string]interface{}{
			"error":   true,
			"message": "already up to date",
			"current": s.updater.current,
		})
		return
	}

	exePath, err := executablePath()
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"error":   true,
			"message": fmt.Sprintf("get exe path: %v", err),
		})
		return
	}

	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"error":   true,
			"message": err.Error(),
		})
		return
	}

	cfg := &DownloadConfig{
		MaxRetries: s.updater.retries,
		Timeout:    s.updater.timeout,
		Progress: func(downloaded, total int64) {
			s.updater.progress.setProgress(downloaded, total)
		},
	}

	s.updater.progress.reset()
	s.updater.progress.setPhase("downloading")

	// Respond immediately, update in background
	writeJSON(w, map[string]interface{}{
		"accepted":   true,
		"message":    "update started",
		"new_version": release.Version,
	})

	go func() {
		result, err := DownloadAndReplace(asset, s.updater.logger, cfg)
		if err != nil {
			s.updater.progress.setError(err.Error())
			s.updater.logger("update failed: %v", err)
			return
		}

		CleanupBackup(result.BackupPath)
		s.updater.progress.setPhase("done")
		s.updater.logger("updated to %s, restarting ...", release.Version)
		if err := restartWith(exePath); err != nil {
			s.updater.logger("restart failed: %v", err)
		}
	}()
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.updater.Progress().Snapshot())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func mustExePathStr() string {
	p, err := executablePath()
	if err != nil {
		return ""
	}
	return p
}
