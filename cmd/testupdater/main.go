// testupdater is a test program for the go-selfupdater library.
//
// Usage:
//   testupdater version                — print current version
//   testupdater check <url>            — check for updates
//   testupdater update <url>           — download and install update
//   testupdater self-update <url>      — check + update + restart
//   testupdater agent <update-url>     — start REST API + web UI
//   testupdater serve [addr]           — run a mock update server
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"

	selfupdate "github.com/viccom/go-selfupdater"
)

const version = "1.0.0"

//go:embed index.html
var indexHTML []byte

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("testupdater %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("exe: %s\n", mustExePath())

	case "check":
		if len(os.Args) < 3 {
			log.Fatal("usage: testupdater check <url>")
		}
		src := selfupdate.NewHTTPSource(os.Args[2])
		u := selfupdate.New(src, version)
		release, err := u.Check()
		if err != nil {
			log.Fatalf("check failed: %v", err)
		}
		if release == nil {
			fmt.Println("already up to date")
			return
		}
		fmt.Printf("update available: %s → %s\n", version, release.Version)
		asset, err := release.AssetForCurrentPlatform()
		if err != nil {
			log.Printf("warning: %v", err)
			return
		}
		fmt.Printf("  url:    %s\n", asset.URL)
		fmt.Printf("  sha256: %s\n", asset.SHA256)
		fmt.Printf("  size:   %d\n", asset.Size)

	case "update":
		if len(os.Args) < 3 {
			log.Fatal("usage: testupdater update <url>")
		}
		src := selfupdate.NewHTTPSource(os.Args[2])
		u := selfupdate.New(src, version)
		release, err := u.Check()
		if err != nil {
			log.Fatalf("check failed: %v", err)
		}
		if release == nil {
			fmt.Println("already up to date")
			return
		}
		fmt.Printf("updating from %s to %s ...\n", version, release.Version)
		if err := u.Update(release); err != nil {
			log.Fatalf("update failed: %v", err)
		}
		fmt.Println("update complete (restart required)")

	case "self-update":
		if len(os.Args) < 3 {
			log.Fatal("usage: testupdater self-update <url>")
		}
		src := selfupdate.NewHTTPSource(os.Args[2])
		u := selfupdate.New(src, version)
		release, err := u.Check()
		if err != nil {
			log.Fatalf("check failed: %v", err)
		}
		if release == nil {
			fmt.Println("already up to date")
			return
		}
		fmt.Printf("updating from %s to %s and restarting ...\n", version, release.Version)
		if err := u.UpdateAndRestart(release); err != nil {
			log.Fatalf("update and restart failed: %v", err)
		}

	case "agent":
		if len(os.Args) < 3 {
			log.Fatal("usage: testupdater agent <update-url> [listen-addr]")
		}
		updateURL := os.Args[2]
		addr := ":8080"
		if len(os.Args) >= 4 {
			addr = os.Args[3]
		}
		runAgent(updateURL, addr)

	case "serve":
		addr := ":9090"
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		serveMock(addr)

	default:
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "testupdater %s — selfupdate library test\n\n", version)
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  version              show version info\n")
	fmt.Fprintf(os.Stderr, "  check <url>          check for updates\n")
	fmt.Fprintf(os.Stderr, "  update <url>         download and install update\n")
	fmt.Fprintf(os.Stderr, "  self-update <url>    check + update + restart\n")
	fmt.Fprintf(os.Stderr, "  agent <url> [addr]   start REST API + web UI (default :8080)\n")
	fmt.Fprintf(os.Stderr, "  serve [addr]         run mock update server (default :9090)\n")
	os.Exit(1)
}

func mustExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return err.Error()
	}
	return exe
}

func runAgent(updateURL, addr string) {
	src := selfupdate.NewHTTPSource(updateURL)
	u := selfupdate.New(src, version)

	apiServer := selfupdate.NewServer(u)

	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	fmt.Printf("testupdater agent %s on %s\n", version, addr)
	fmt.Printf("  web UI:   http://localhost%s/\n", addr)
	fmt.Printf("  update:   %s\n", updateURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func serveMock(addr string) {
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	fileHash := fileSHA256(exePath)

	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		platform := runtime.GOOS + "/" + runtime.GOARCH
		release := selfupdate.Release{
			Version: "2.0.0",
			Assets: map[string]selfupdate.Asset{
				platform: {
					URL:    fmt.Sprintf("http://%s/download/testupdater", r.Host),
					Size:   0,
					SHA256: fileHash,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	})

	mux.HandleFunc("/download/testupdater", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, exePath)
	})

	fmt.Printf("mock update server on %s\n", addr)
	fmt.Printf("  release info:  http://localhost%s/api/latest\n", addr)
	fmt.Printf("  binary served: %s (sha256: %s...)\n", exePath, fileHash[:16])
	log.Fatal(http.ListenAndServe(addr, mux))
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}
