// testupdater is a test program for the go-selfupdater library.
//
// Modes:
//   testupdater daemon <update-url> [addr]  — start background daemon with REST API + web UI
//   testupdater status [addr]               — show daemon status
//   testupdater check [addr]                — check for updates
//   testupdater update [addr]               — trigger update + restart
//   testupdater stop [addr]                 — stop daemon
//   testupdater serve [addr]                — mock update file server
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
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	selfupdate "github.com/viccom/go-selfupdater"
)

const version = "1.0.0"
const defaultAddr = "localhost:18080"

//go:embed index.html
var indexHTML []byte

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "daemon":
		addr := defaultAddr
		if len(os.Args) < 3 {
			log.Fatal("usage: testupdater daemon <update-url> [addr]")
		}
		updateURL := os.Args[2]
		if len(os.Args) >= 4 {
			addr = os.Args[3]
		}
		runDaemon(updateURL, addr)

	case "status":
		addr := defaultAddr
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		cliGet(addr, "/api/version")

	case "check":
		addr := defaultAddr
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		cliGet(addr, "/api/check")

	case "update":
		addr := defaultAddr
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		cliPost(addr, "/api/update")

	case "stop":
		addr := defaultAddr
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		cliPost(addr, "/api/stop")

	case "version":
		fmt.Printf("testupdater %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		if exe, err := os.Executable(); err == nil {
			fmt.Printf("exe: %s\n", exe)
		}

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
	fmt.Fprintf(os.Stderr, "  daemon <update-url> [addr]  start background daemon\n")
	fmt.Fprintf(os.Stderr, "  status [addr]               show daemon status & version\n")
	fmt.Fprintf(os.Stderr, "  check [addr]                check for updates\n")
	fmt.Fprintf(os.Stderr, "  update [addr]               upgrade daemon + auto restart\n")
	fmt.Fprintf(os.Stderr, "  stop [addr]                 stop daemon\n")
	fmt.Fprintf(os.Stderr, "  version                     show local version\n")
	fmt.Fprintf(os.Stderr, "  serve [addr]                mock update file server\n")
	fmt.Fprintf(os.Stderr, "\ndefault addr: %s\n", defaultAddr)
	os.Exit(1)
}

// ===== Daemon =====

func runDaemon(updateURL, addr string) {
	src := selfupdate.NewHTTPSource(updateURL)
	u := selfupdate.New(src, version)
	apiServer := selfupdate.NewServer(u)

	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer)
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "stopping"})
		go func() {
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		}()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nstopping daemon ...")
		server.Close()
	}()

	fmt.Printf("testupdater %s daemon started\n", version)
	fmt.Printf("  listen:    http://%s/\n", addr)
	fmt.Printf("  update:    %s\n", updateURL)
	fmt.Printf("  pid:       %d\n", os.Getpid())
	log.Fatal(server.ListenAndServe())
}

// ===== CLI client =====

func cliGet(addr, path string) {
	resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, path))
	if err != nil {
		log.Fatalf("daemon not reachable at %s: %v", addr, err)
	}
	defer resp.Body.Close()
	var v interface{}
	json.NewDecoder(resp.Body).Decode(&v)
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func cliPost(addr, path string) {
	resp, err := http.Post(fmt.Sprintf("http://%s%s", addr, path), "", nil)
	if err != nil {
		log.Fatalf("daemon not reachable at %s: %v", addr, err)
	}
	defer resp.Body.Close()
	var v interface{}
	json.NewDecoder(resp.Body).Decode(&v)
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

// ===== Mock update file server =====

func serveMock(addr string) {
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	// Determine serve directory (looks for download/ subdirectory)
	serveDir := filepath.Dir(exePath)
	fileHash := fileSHA256(exePath)

	mux := http.NewServeMux()

	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		platform := runtime.GOOS + "/" + runtime.GOARCH
		release := selfupdate.Release{
			Version: "2.0.0",
			Assets: map[string]selfupdate.Asset{
				platform: {
					URL:    fmt.Sprintf("http://%s/download/testupdater", r.Host),
					SHA256: fileHash,
					Size:   fileSize(exePath),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	})

	fs := http.FileServer(http.Dir(serveDir))
	mux.Handle("/download/", http.StripPrefix("/download", fs))

	fmt.Printf("mock update server on %s\n", addr)
	fmt.Printf("  release:  http://localhost%s/latest.json\n", addr)
	fmt.Printf("  files:    %s/\n", serveDir)
	fmt.Printf("  sha256:   %s...\n", fileHash[:16])
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
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

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
