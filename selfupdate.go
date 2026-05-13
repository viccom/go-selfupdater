package selfupdate

import (
	"fmt"
	"sync"
	"time"
)

// ProgressState holds the current download progress for querying.
type ProgressState struct {
	mu        sync.RWMutex
	Active    bool
	Phase     string // "downloading", "validating", "replacing", "done", ""
	Downloaded int64
	Total      int64
	Err        string
}

// Percent returns download progress as 0-100.
func (p *ProgressState) Percent() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.Total <= 0 {
		return 0
	}
	return int(p.Downloaded * 100 / p.Total)
}

// Snapshot returns a copy of the current state.
func (p *ProgressState) Snapshot() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	m := map[string]interface{}{
		"active":     p.Active,
		"phase":      p.Phase,
		"downloaded": p.Downloaded,
		"total":      p.Total,
		"percent":    0,
	}
	if p.Total > 0 {
		m["percent"] = p.Downloaded * 100 / p.Total
	}
	if p.Err != "" {
		m["error"] = p.Err
	}
	return m
}

func (p *ProgressState) setPhase(phase string) {
	p.mu.Lock()
	p.Phase = phase
	p.mu.Unlock()
}

func (p *ProgressState) setProgress(downloaded, total int64) {
	p.mu.Lock()
	p.Downloaded = downloaded
	p.Total = total
	p.mu.Unlock()
}

func (p *ProgressState) setError(err string) {
	p.mu.Lock()
	p.Err = err
	p.Active = false
	p.mu.Unlock()
}

func (p *ProgressState) reset() {
	p.mu.Lock()
	p.Active = true
	p.Phase = ""
	p.Downloaded = 0
	p.Total = 0
	p.Err = ""
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
func (u *Updater) Update(release *Release) error {
	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		return err
	}

	cfg := &DownloadConfig{
		MaxRetries: u.retries,
		Timeout:    u.timeout,
		Progress: func(downloaded, total int64) {
			u.progress.setProgress(downloaded, total)
		},
	}

	u.progress.reset()
	u.progress.setPhase("downloading")

	result, err := DownloadAndReplace(asset, u.logger, cfg)
	if err != nil {
		u.progress.setError(err.Error())
		return fmt.Errorf("update: %w", err)
	}

	u.progress.setPhase("done")
	u.progress.Active = false
	CleanupBackup(result.BackupPath)
	u.logger("updated to %s", release.Version)
	return nil
}

// UpdateAndRestart downloads, installs, and restarts the program.
func (u *Updater) UpdateAndRestart(release *Release) error {
	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		return err
	}

	exePath, err := executablePath()
	if err != nil {
		return fmt.Errorf("get exe path: %w", err)
	}

	cfg := &DownloadConfig{
		MaxRetries: u.retries,
		Timeout:    u.timeout,
		Progress: func(downloaded, total int64) {
			u.progress.setProgress(downloaded, total)
		},
	}

	u.progress.reset()
	u.progress.setPhase("downloading")

	result, err := DownloadAndReplace(asset, u.logger, cfg)
	if err != nil {
		u.progress.setError(err.Error())
		return fmt.Errorf("update: %w", err)
	}

	CleanupBackup(result.BackupPath)
	u.progress.setPhase("done")
	u.logger("updated to %s", release.Version)
	u.logger("restarting ...")
	return restartWith(exePath)
}
