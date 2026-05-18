package selfupdate

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Phase constants for ProgressState.
const (
	PhaseDownloading = "downloading"
	PhaseValidating  = "validating"
	PhaseReplacing   = "replacing"
	PhaseDone        = "done"
)

// ProgressSnapshot is a typed copy of the current progress state.
type ProgressSnapshot struct {
	Active     bool   `json:"active"`
	Phase      string `json:"phase"`
	Percent    int    `json:"percent"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Error      string `json:"error,omitempty"`
}

// ProgressState holds the current download progress for querying.
type ProgressState struct {
	mu         sync.RWMutex
	active     bool
	phase      string
	downloaded int64
	total      int64
	err        string
}

// Percent returns download progress as 0-100.
func (p *ProgressState) Percent() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.total <= 0 {
		return 0
	}
	return int(p.downloaded * 100 / p.total)
}

// Snapshot returns a typed copy of the current state.
func (p *ProgressState) Snapshot() ProgressSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := ProgressSnapshot{
		Active:     p.active,
		Phase:      p.phase,
		Downloaded: p.downloaded,
		Total:      p.total,
	}
	if p.total > 0 {
		s.Percent = int(p.downloaded * 100 / p.total)
	}
	if p.err != "" {
		s.Error = p.err
	}
	return s
}

// SnapshotMap returns the current state as a map[string]interface{}.
// Deprecated: Use Snapshot() which returns a typed ProgressSnapshot.
func (p *ProgressState) SnapshotMap() map[string]interface{} {
	s := p.Snapshot()
	m := map[string]interface{}{
		"active":     s.Active,
		"phase":      s.Phase,
		"percent":    s.Percent,
		"downloaded": s.Downloaded,
		"total":      s.Total,
	}
	if s.Error != "" {
		m["error"] = s.Error
	}
	return m
}

func (p *ProgressState) setPhase(phase string) {
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
}

func (p *ProgressState) setProgress(downloaded, total int64) {
	p.mu.Lock()
	p.downloaded = downloaded
	p.total = total
	p.mu.Unlock()
}

func (p *ProgressState) setError(err string) {
	p.mu.Lock()
	p.err = err
	p.active = false
	p.mu.Unlock()
}

func (p *ProgressState) setDone() {
	p.mu.Lock()
	p.active = false
	p.phase = PhaseDone
	p.mu.Unlock()
}

func (p *ProgressState) start() {
	p.mu.Lock()
	p.active = true
	p.phase = PhaseDownloading
	p.downloaded = 0
	p.total = 0
	p.err = ""
	p.mu.Unlock()
}

// Updater checks for updates and applies them.
type Updater struct {
	source   Source
	current  string
	logger   func(string, ...interface{})
	progress ProgressState
	retries  int
	timeout  time.Duration
	updateMu sync.Mutex
	exePath  string       // cached exe path before replacement, for Restart()
	exeMu    sync.RWMutex // protects exePath
}

// New creates a new Updater with the given source and current version.
func New(source Source, currentVersion string, opts ...Option) *Updater {
	u := &Updater{
		source:  source,
		current: currentVersion,
		logger:  DefaultLogger(),
		retries: 3,
		timeout: 5 * time.Minute,
	}
	for _, o := range opts {
		o(u)
	}
	return u
}

// Option configures an Updater.
type Option func(*Updater)

// WithLogger sets a custom logger function.
func WithLogger(logger func(string, ...interface{})) Option {
	return func(u *Updater) {
		u.logger = logger
	}
}

// WithRetries sets the number of download retries (default: 3).
func WithRetries(n int) Option {
	return func(u *Updater) {
		u.retries = n
	}
}

// WithTimeout sets the download timeout (default: 5min).
func WithTimeout(d time.Duration) Option {
	return func(u *Updater) {
		u.timeout = d
	}
}

// Progress returns the progress state for polling.
func (u *Updater) Progress() *ProgressState {
	return &u.progress
}

// CurrentVersion returns the current version string.
func (u *Updater) CurrentVersion() string {
	return u.current
}

// Check queries the source for a newer release.
// Returns nil if the current version is already the latest.
func (u *Updater) Check() (*Release, error) {
	if err := ValidateVersion(u.current); err != nil {
		return nil, fmt.Errorf("invalid current version: %w", err)
	}

	release, err := u.source.GetLatest()
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}

	if !IsNewer(release.Version, u.current) {
		return nil, nil
	}

	return release, nil
}

// Update downloads and installs the update for the current platform.
// The program must restart manually afterwards.
// The .old backup is NOT deleted immediately (it's locked on Windows while
// the process runs). Call CleanupStaleBackup at startup to remove leftovers.
// The executable path is cached before replacement for subsequent Restart() calls.
// Concurrent calls are serialized via mutex.
func (u *Updater) Update(release *Release) (*ReplaceResult, error) {
	if !u.updateMu.TryLock() {
		return nil, fmt.Errorf("update already in progress")
	}
	defer u.updateMu.Unlock()

	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		return nil, err
	}

	// Cache exe path BEFORE replacement for Restart() on Linux
	// where /proc/self/exe points to the deleted .old after replacement.
	if exePath, err := executablePath(); err == nil {
		u.exeMu.Lock()
		u.exePath = exePath
		u.exeMu.Unlock()
	}

	u.progress.start()

	result, err := DownloadAndReplace(asset, u.logger, u.downloadConfig())
	if err != nil {
		u.progress.setError(err.Error())
		return nil, fmt.Errorf("update: %w", err)
	}

	u.progress.setDone()
	u.logger("updated to %s", release.Version)
	return result, nil
}

// UpdateAndRestart downloads, installs, and restarts the program.
// The .old backup is cleaned up only on Linux (where the file is not locked).
// On Windows, the .old remains until the next startup calls CleanupStaleBackup.
// Concurrent calls are serialized via mutex.
func (u *Updater) UpdateAndRestart(release *Release) error {
	if !u.updateMu.TryLock() {
		return fmt.Errorf("update already in progress")
	}
	defer u.updateMu.Unlock()
	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		return err
	}

	exePath, err := executablePath()
	if err != nil {
		return fmt.Errorf("get exe path: %w", err)
	}

	// Cache for DoRestart() consistency
	u.exeMu.Lock()
	u.exePath = exePath
	u.exeMu.Unlock()

	u.progress.start()

	result, err := DownloadAndReplace(asset, u.logger, u.downloadConfig())
	if err != nil {
		u.progress.setError(err.Error())
		return fmt.Errorf("update: %w", err)
	}

	// Only cleanup on Linux; on Windows the .old is locked by the running process
	if runtime.GOOS != "windows" {
		CleanupStaleBackup(result.BackupPath)
	}

	u.progress.setDone()
	u.logger("updated to %s", release.Version)
	u.logger("restarting ...")
	return restartWith(exePath)
}

func (u *Updater) downloadConfig() *DownloadConfig {
	return &DownloadConfig{
		MaxRetries: u.retries,
		Timeout:    u.timeout,
		Progress: func(downloaded, total int64) {
			u.progress.setProgress(downloaded, total)
		},
	}
}

// DoRestart restarts the current program using the cached executable path
// from a prior Update() call. If no cached path is available, it falls back
// to resolving the path at restart time (which may fail on Linux after
// binary replacement if /proc/self/exe points to a deleted .old file).
func (u *Updater) DoRestart() error {
	u.exeMu.RLock()
	cached := u.exePath
	u.exeMu.RUnlock()
	return globalRestart(cached)
}
