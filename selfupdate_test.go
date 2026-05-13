package selfupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"v1.0.0", "1.0.0", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"1.0", "1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", 0},
		{"1.0.0+build", "1.0.0", 0},
		{"0.9.9", "1.0.0", -1},
		{"10.0.0", "9.0.0", 1},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate, current string
		want               bool
	}{
		{"1.1.0", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"2.0.0", "1.9.9", true},
		{"v1.1.0", "v1.0.0", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.candidate, tt.current)
		if got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	if err := ValidateVersion("1.0.0"); err != nil {
		t.Errorf("ValidateVersion(\"1.0.0\") = %v", err)
	}
	if err := ValidateVersion(""); err == nil {
		t.Error("ValidateVersion(\"\") should return error")
	}
	if err := ValidateVersion("1..0"); err == nil {
		t.Error("ValidateVersion(\"1..0\") should return error")
	}
}

func TestHTTPSource(t *testing.T) {
	expected := Release{
		Version: "2.0.0",
		Assets: map[string]Asset{
			"linux/amd64": {URL: "http://example.com/app", SHA256: "abc", Size: 100},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	src := NewHTTPSource(srv.URL)
	release, err := src.GetLatest()
	if err != nil {
		t.Fatal(err)
	}

	if release.Version != "2.0.0" {
		t.Errorf("version = %q, want %q", release.Version, "2.0.0")
	}

	asset, err := release.AssetForPlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.SHA256 != "abc" {
		t.Errorf("sha256 = %q, want %q", asset.SHA256, "abc")
	}
}

func TestCurrentPlatform(t *testing.T) {
	p := CurrentPlatform()
	if p == "" {
		t.Error("CurrentPlatform() returned empty string")
	}
}

func TestCheckNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "1.0.0"})
	}))
	defer srv.Close()

	u := New(NewHTTPSource(srv.URL), "1.0.0")
	release, err := u.Check()
	if err != nil {
		t.Fatal(err)
	}
	if release != nil {
		t.Error("expected nil release when already up to date")
	}
}

func TestCheckHasUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "2.0.0"})
	}))
	defer srv.Close()

	u := New(NewHTTPSource(srv.URL), "1.0.0")
	release, err := u.Check()
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatal("expected release for available update")
	}
	if release.Version != "2.0.0" {
		t.Errorf("version = %q, want %q", release.Version, "2.0.0")
	}
}

func TestSHA256Validation(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.bin")
	content := []byte("hello selfupdate")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// compute expected hash
	invalidHash := "0000000000000000000000000000000000000000000000000000000000000000"
	if err := validateSHA256(testFile, invalidHash); err == nil {
		t.Error("expected error for wrong sha256")
	}

	// empty checksum should skip validation
	if err := validateSHA256(testFile, ""); err != nil {
		t.Errorf("empty checksum should skip validation: %v", err)
	}
}
