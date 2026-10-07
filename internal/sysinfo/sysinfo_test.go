package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadPi(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "class/thermal/thermal_zone0/temp"), "52612\n")
	writeFile(t, filepath.Join(root, "devices/platform/soc/soc:firmware/get_throttled"), "50005\n")

	b := Reader{Root: root}.Read()
	if b == nil || b.TempC == nil || *b.TempC != 52.612 {
		t.Fatalf("temperature: got %+v", b)
	}
	th := b.Throttled
	if th == nil {
		t.Fatal("throttled missing")
	}
	if th.Raw != 0x50005 || !th.UnderVoltage || !th.Throttled || th.FreqCapped || th.SoftTempLimit {
		t.Fatalf("current flags: %+v", th)
	}
	if !th.UnderVoltageOccurred || !th.ThrottledOccurred || th.FreqCappedOccurred {
		t.Fatalf("since-boot flags: %+v", th)
	}
}

func TestReadPi5FirmwarePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "devices/platform/soc@107c000000/soc@107c000000:firmware/get_throttled"), "0\n")

	b := Reader{Root: root}.Read()
	if b == nil || b.Throttled == nil || b.Throttled.Raw != 0 || b.TempC != nil {
		t.Fatalf("got %+v", b)
	}
}

func TestReadNothingAvailable(t *testing.T) {
	if b := (Reader{Root: t.TempDir()}).Read(); b != nil {
		t.Fatalf("expected nil, got %+v", b)
	}
}

func TestReadIgnoresGarbage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "class/thermal/thermal_zone0/temp"), "n/a")
	if b := (Reader{Root: root}).Read(); b != nil {
		t.Fatalf("expected nil, got %+v", b)
	}
}
