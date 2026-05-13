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

	// Save exe path BEFORE replacing
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

	result, err := DownloadAndReplace(asset, s.updater.logger)
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"error":   true,
			"message": fmt.Sprintf("update failed: %v", err),
		})
		return
	}

	CleanupBackup(result.BackupPath)

	writeJSON(w, map[string]interface{}{
		"success":     true,
		"message":     "update completed, restarting",
		"old_version": s.updater.current,
		"new_version": release.Version,
	})

	go func() {
		s.updater.logger("restarting after update ...")
		if err := restartWith(exePath); err != nil {
			s.updater.logger("restart failed: %v", err)
		}
	}()
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
