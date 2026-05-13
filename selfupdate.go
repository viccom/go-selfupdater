package selfupdate

import (
	"fmt"
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

// Snapshot returns a copy of the current state.
func (p *ProgressState) Snapshot() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	m := map[string]interface{}{
		"active":     p.active,
		"phase":      p.phase,
		"downloaded": p.downloaded,
		"total":      p.total,
		"percent":    0,
	}
	if p.total > 0 {
		m["percent"] = p.downloaded * 100 / p.total
	}
	if p.err != "" {
		m["error"] = p.err
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
	source  Source
	current string
	logger  func(string, ...interface{})
	progress ProgressState
	retries  int
	timeout  time.Duration
	updateMu sync.Mutex
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

	u.progress.start()

	result, err := DownloadAndReplace(asset, u.logger, u.downloadConfig())
	if err != nil {
		u.progress.setError(err.Error())
		return fmt.Errorf("update: %w", err)
	}

	u.progress.setDone()
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

	u.progress.start()

	result, err := DownloadAndReplace(asset, u.logger, u.downloadConfig())
	if err != nil {
		u.progress.setError(err.Error())
		return fmt.Errorf("update: %w", err)
	}

	CleanupBackup(result.BackupPath)
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
