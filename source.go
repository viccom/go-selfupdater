package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
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
	if url != "" && strings.HasPrefix(url, "http://") {
		// Auto-upgrade http:// to https:// for non-localhost URLs
		// to prevent MITM on the release manifest.
		host := url[len("http://"):]
		if idx := strings.Index(host, "/"); idx >= 0 {
			host = host[:idx]
		}
		// Strip port for loopback check
		hostNoPort := host
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			hostNoPort = host[:idx]
		}
		// Check for localhost variants (loopback addresses are safe without TLS)
		if hostNoPort != "127.0.0.1" && hostNoPort != "::1" &&
			!strings.EqualFold(hostNoPort, "localhost") {
			url = "https://" + url[len("http://"):]
		}
	}
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

	var release Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
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
