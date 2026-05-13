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
		writeErrorJSON(w, err.Error(), s.updater.current)
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

	resp := map[string]interface{}{
		"current":    s.updater.current,
		"has_update": true,
		"latest":     release.Version,
		"date":       release.Date,
	}

	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		resp["asset_error"] = err.Error()
	} else {
		resp["asset"] = map[string]interface{}{
			"url":    asset.URL,
			"sha256": asset.SHA256,
			"size":   asset.Size,
		}
	}

	writeJSON(w, resp)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Prevent concurrent updates
	if !s.updater.updateMu.TryLock() {
		writeErrorJSON(w, "update already in progress", s.updater.current)
		return
	}

	writeJSON(w, map[string]interface{}{
		"accepted": true,
		"message":  "update started",
	})

	go func() {
		defer s.updater.updateMu.Unlock()

		release, err := s.updater.Check()
		if err != nil {
			s.updater.progress.setError(fmt.Sprintf("check failed: %v", err))
			s.updater.logger("update check failed: %v", err)
			return
		}
		if release == nil {
			s.updater.progress.setError("already up to date")
			return
		}

		asset, err := release.AssetForCurrentPlatform()
		if err != nil {
			s.updater.progress.setError(err.Error())
			s.updater.logger("asset error: %v", err)
			return
		}

		exePath, err := executablePath()
		if err != nil {
			s.updater.progress.setError(err.Error())
			return
		}

		s.updater.progress.start()
		s.updater.progress.setPhase(PhaseDownloading)

		result, err := DownloadAndReplace(asset, s.updater.logger, s.updater.downloadConfig())
		if err != nil {
			s.updater.progress.setError(err.Error())
			s.updater.logger("update failed: %v", err)
			return
		}

		CleanupBackup(result.BackupPath)
		s.updater.progress.setDone()
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

func writeErrorJSON(w http.ResponseWriter, message, current string) {
	writeJSON(w, map[string]interface{}{
		"error":   true,
		"message": message,
		"current": current,
	})
}

func mustExePathStr() string {
	p, err := executablePath()
	if err != nil {
		return ""
	}
	return p
}
