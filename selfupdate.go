// Package selfupdate provides a lightweight self-updating mechanism for Go CLI programs.
//
// Basic usage:
//
//	src := selfupdate.NewHTTPSource("https://example.com/api/latest")
//	u := selfupdate.New(src, "1.0.0")
//
//	release, err := u.Check()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if release == nil {
//	    fmt.Println("already up to date")
//	    return
//	}
//
//	if err := u.UpdateAndRestart(release); err != nil {
//	    log.Fatal(err)
//	}
package selfupdate

import (
	"fmt"
)

// Updater checks for updates and applies them.
type Updater struct {
	source  Source
	current string
	logger  func(string, ...interface{})
}

// New creates a new Updater with the given source and current version.
func New(source Source, currentVersion string, opts ...Option) *Updater {
	u := &Updater{
		source:  source,
		current: currentVersion,
		logger:  DefaultLogger(),
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

	result, err := DownloadAndReplace(asset, u.logger)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

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

	// Save exe path BEFORE replacing (Linux /proc/self/exe breaks after replace)
	exePath, err := executablePath()
	if err != nil {
		return fmt.Errorf("get exe path: %w", err)
	}

	result, err := DownloadAndReplace(asset, u.logger)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	CleanupBackup(result.BackupPath)
	u.logger("updated to %s", release.Version)
	u.logger("restarting ...")
	return restartWith(exePath)
}
