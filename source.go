package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

// Release represents an available update release.
type Release struct {
	Version string
	Date    string
	Assets  map[string]Asset `json:"assets"`
}

// Asset is a platform-specific downloadable binary.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Source fetches release information from an update source.
type Source interface {
	GetLatest() (*Release, error)
}

// HTTPSource fetches releases from a JSON HTTP endpoint.
//
// The endpoint must return:
//
//	{
//	  "version": "1.2.3",
//	  "date":    "2025-01-01T00:00:00Z",
//	  "assets": {
//	    "linux/amd64":   {"url": "...", "sha256": "...", "size": 12345},
//	    "windows/amd64": {"url": "...", "sha256": "...", "size": 12345}
//	  }
//	}
type HTTPSource struct {
	URL    string
	Client *http.Client
}

func NewHTTPSource(url string) *HTTPSource {
	return &HTTPSource{
		URL: url,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *HTTPSource) GetLatest() (*Release, error) {
	resp, err := s.Client.Get(s.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read release response: %w", err)
	}

	var release Release
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("parse release JSON: %w", err)
	}

	if release.Version == "" {
		return nil, fmt.Errorf("release has no version")
	}

	return &release, nil
}

// CurrentPlatform returns the current OS/arch string, e.g. "linux/amd64".
func CurrentPlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// AssetForPlatform returns the asset for a given platform key.
func (r *Release) AssetForPlatform(platform string) (*Asset, error) {
	a, ok := r.Assets[platform]
	if !ok {
		return nil, fmt.Errorf("no asset for platform %q", platform)
	}
	return &a, nil
}

// AssetForCurrentPlatform returns the asset for the current OS/arch.
func (r *Release) AssetForCurrentPlatform() (*Asset, error) {
	return r.AssetForPlatform(CurrentPlatform())
}
