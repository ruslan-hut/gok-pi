package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gok-pi/internal/lib/atomicfile"
)

// Config configures the updater workflow.
type Config struct {
	AgentRoot      string
	BinaryName     string
	VersionURL     string
	BinaryURL      string
	Timeout        time.Duration
	Client         *http.Client
	RestartService string
	RestartEnabled bool
}

// Run performs the update check and binary swap if necessary.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: cfg.Timeout,
		}
	}

	localVersion, err := readLocalVersion(cfg.localVersionPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read local version: %w", err)
	}

	remoteVersion, err := fetchRemoteVersion(ctx, client, cfg.VersionURL)
	if err != nil {
		return fmt.Errorf("fetch remote version: %w", err)
	}

	if localVersion == remoteVersion {
		log.Info("agent binary already up to date", "version", remoteVersion)
		return nil
	}

	log.Info("detected new version", "current", localVersion, "remote", remoteVersion)

	binaryURL, err := cfg.effectiveBinaryURL()
	if err != nil {
		return err
	}

	tempPath, err := downloadBinary(ctx, client, binaryURL, cfg.AgentRoot)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	defer os.Remove(tempPath)

	if err := verifySHA256(tempPath, remoteVersion); err != nil {
		return fmt.Errorf("verify binary: %w", err)
	}

	if err := installBinary(tempPath, cfg.localBinaryPath()); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}

	if err := atomicfile.Write(cfg.localVersionPath(), []byte(remoteVersion+"\n"), 0o644); err != nil {
		return fmt.Errorf("write version file: %w", err)
	}

	log.Info("agent binary updated successfully", "version", remoteVersion)

	// Restart agent service if enabled
	if cfg.RestartEnabled && cfg.RestartService != "" {
		if err := restartService(ctx, cfg.RestartService, log); err != nil {
			// Log error but don't fail the update - the binary is already installed
			log.Warn("failed to restart agent service after update", slog.String("service", cfg.RestartService), slog.Any("error", err))
		} else {
			log.Info("agent service restarted successfully", slog.String("service", cfg.RestartService))
		}
	}

	return nil
}

func (cfg Config) validate() error {
	if cfg.AgentRoot == "" {
		return errors.New("agent-root must not be empty")
	}

	if cfg.BinaryName == "" {
		return errors.New("binary-name must not be empty")
	}

	if cfg.VersionURL == "" {
		return errors.New("version-url must not be empty")
	}

	if cfg.Timeout <= 0 {
		return errors.New("timeout must be greater than zero")
	}

	info, err := os.Stat(cfg.AgentRoot)
	if err != nil {
		return fmt.Errorf("agent-root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agent-root is not a directory: %s", cfg.AgentRoot)
	}

	if cfg.BinaryURL != "" {
		if _, err = url.ParseRequestURI(cfg.BinaryURL); err != nil {
			return fmt.Errorf("binary-url invalid: %w", err)
		}
	}

	if _, err = url.ParseRequestURI(cfg.VersionURL); err != nil {
		return fmt.Errorf("version-url invalid: %w", err)
	}

	return nil
}

func (cfg Config) localBinaryPath() string {
	return filepath.Join(cfg.AgentRoot, cfg.BinaryName)
}

func (cfg Config) localVersionPath() string {
	return filepath.Join(cfg.AgentRoot, "VERSION")
}

func (cfg Config) effectiveBinaryURL() (string, error) {
	if cfg.BinaryURL != "" {
		return cfg.BinaryURL, nil
	}

	versionURL, err := url.Parse(cfg.VersionURL)
	if err != nil {
		return "", fmt.Errorf("parse version-url: %w", err)
	}

	versionURL.Path = path.Join(path.Dir(versionURL.Path), cfg.BinaryName)
	return versionURL.String(), nil
}

func readLocalVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func fetchRemoteVersion(ctx context.Context, client *http.Client, versionURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	version := strings.TrimSpace(string(body))
	if err := validateHash(version); err != nil {
		return "", fmt.Errorf("remote VERSION invalid: %w", err)
	}

	return version, nil
}

func downloadBinary(ctx context.Context, client *http.Client, binaryURL, agentRoot string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binaryURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	tempFile, err := os.CreateTemp(agentRoot, "gok-update-*")
	if err != nil {
		return "", err
	}

	if _, err = io.Copy(tempFile, resp.Body); err != nil {
		tempFile.Close()
		return "", err
	}

	if err = tempFile.Close(); err != nil {
		return "", err
	}

	if err = os.Chmod(tempFile.Name(), 0o755); err != nil {
		return "", err
	}

	return tempFile.Name(), nil
}

func verifySHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	got := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("hash mismatch: got %s expected %s", got, expected)
	}

	return nil
}

func installBinary(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}


func validateHash(hash string) error {
	if len(hash) != 64 {
		return fmt.Errorf("expected 64 hex characters, got %d", len(hash))
	}
	for _, c := range hash {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return fmt.Errorf("invalid hex character %q", c)
	}
	return nil
}

// restartService restarts a systemd service using systemctl.
func restartService(ctx context.Context, serviceName string, log *slog.Logger) error {
	// Validate service name to prevent command injection
	if strings.ContainsAny(serviceName, " \t\n\r;&|$`\"'<>") {
		return fmt.Errorf("invalid service name: contains forbidden characters")
	}

	cmd := exec.CommandContext(ctx, "systemctl", "restart", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart failed: %w, output: %s", err, string(output))
	}

	log.Debug("systemctl restart completed", slog.String("service", serviceName), slog.String("output", string(output)))
	return nil
}
