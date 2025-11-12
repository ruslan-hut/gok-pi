package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRun_UpToDateSkipsDownload(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "gok")
	binaryContents := []byte("existing-binary")
	hash := sha256Sum(binaryContents)

	if err := os.WriteFile(binaryPath, binaryContents, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "VERSION"), []byte(hash+"\n"), 0o644); err != nil {
		t.Fatalf("write version: %v", err)
	}

	requests := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/VERSION":
			fmt.Fprint(w, hash)
		case "/gok":
			t.Fatalf("binary download should not be attempted when version is current")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{
		AgentRoot:  tempDir,
		BinaryName: "gok",
		VersionURL: server.URL + "/VERSION",
		BinaryURL:  server.URL + "/gok",
		Timeout:    time.Second,
		Client:     server.Client(),
	}

	if err := Run(context.Background(), cfg, log); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if requests["/VERSION"] != 1 {
		t.Fatalf("expected exactly one VERSION request, got %d", requests["/VERSION"])
	}

	gotBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(gotBinary) != string(binaryContents) {
		t.Errorf("binary changed unexpectedly: %q", gotBinary)
	}
}

func TestRun_PerformsBinarySwap(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "gok")

	initialBinary := []byte("initial-binary")
	if err := os.WriteFile(binaryPath, initialBinary, 0o755); err != nil {
		t.Fatalf("write initial binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "VERSION"), []byte(sha256Sum(initialBinary)+"\n"), 0o644); err != nil {
		t.Fatalf("write initial version: %v", err)
	}

	newBinary := []byte("new-release")
	newHash := sha256Sum(newBinary)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/VERSION":
			fmt.Fprint(w, newHash)
		case "/gok":
			if _, err := w.Write(newBinary); err != nil {
				t.Fatalf("write binary response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{
		AgentRoot:  tempDir,
		BinaryName: "gok",
		VersionURL: server.URL + "/VERSION",
		BinaryURL:  server.URL + "/gok",
		Timeout:    time.Second,
		Client:     server.Client(),
	}

	if err := Run(context.Background(), cfg, log); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	gotBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(gotBinary) != string(newBinary) {
		t.Errorf("binary not updated, got %q", gotBinary)
	}

	versionBytes, err := os.ReadFile(filepath.Join(tempDir, "VERSION"))
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if string(versionBytes) != newHash+"\n" {
		t.Errorf("VERSION mismatch: %q", versionBytes)
	}
}

func sha256Sum(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:])
}
