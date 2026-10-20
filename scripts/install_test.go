package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// releaseDir builds a local directory laid out like a GitHub Release:
// one stub binary per architecture plus a checksums file.
func releaseDir(t *testing.T, corrupt bool) string {
	t.Helper()
	dir := t.TempDir()
	sums := ""
	for _, v := range []struct{ prefix, tag string }{{"sys-telemetry-app-linux-", ""}, {"sys-telemetry-app-nvml-linux-", "nvml-"}} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := v.prefix + arch
			body := []byte("#!/bin/sh\necho " + v.tag + "built-for-" + arch + "\n")
			sum := sha256.Sum256(body)
			if corrupt {
				sum[0] ^= 0xff
			}
			if err := os.WriteFile(filepath.Join(dir, name), body, 0o755); err != nil {
				t.Fatal(err)
			}
			sums += fmt.Sprintf("%x  %s\n", sum, name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runInstall(t *testing.T, release, machine string) (string, string, error) {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYS_TELEMETRY_BASE_URL=" + release,
		"SYS_TELEMETRY_UNAME_M=" + machine,
		"INSTALL_DIR=" + dest,
	}
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	return dest, string(out), err
}

func installed(t *testing.T, dest string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(dest, "sys-telemetry-app")).Output()
	if err != nil {
		t.Fatalf("installed binary does not run: %v", err)
	}
	return string(out)
}

func TestInstallPicksArchitecture(t *testing.T) {
	cases := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}
	for machine, arch := range cases {
		dest, _, err := runInstall(t, releaseDir(t, false), machine)
		if err != nil {
			t.Fatalf("%s: %v", machine, err)
		}
		if got := installed(t, dest); got != "built-for-"+arch+"\n" {
			t.Errorf("%s: got %q", machine, got)
		}
	}
}

func TestInstallRejectsUnsupportedArchitecture(t *testing.T) {
	dest, _, err := runInstall(t, releaseDir(t, false), "riscv64")
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "sys-telemetry-app")); statErr == nil {
		t.Fatal("binary installed despite failure")
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	dest, _, err := runInstall(t, releaseDir(t, true), "x86_64")
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "sys-telemetry-app")); statErr == nil {
		t.Fatal("binary installed despite checksum mismatch")
	}
}

func runInstallEnv(t *testing.T, release, machine string, extra ...string) (string, string, error) {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"SYS_TELEMETRY_BASE_URL=" + release,
		"SYS_TELEMETRY_UNAME_M=" + machine,
		"INSTALL_DIR=" + dest,
	}, extra...)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	return dest, string(out), err
}

// The default install is the static binary; the nvml variant is opt-in.
func TestInstallVariantNvmlIsOptIn(t *testing.T) {
	dest, _, err := runInstallEnv(t, releaseDir(t, false), "x86_64", "SYS_TELEMETRY_VARIANT=nvml")
	if err != nil {
		t.Fatal(err)
	}
	if got := installed(t, dest); got != "nvml-built-for-amd64\n" {
		t.Fatalf("got %q", got)
	}
	dest, _, err = runInstallEnv(t, releaseDir(t, false), "aarch64", "SYS_TELEMETRY_VARIANT=nvml")
	if err != nil {
		t.Fatal(err)
	}
	if got := installed(t, dest); got != "nvml-built-for-arm64\n" {
		t.Fatalf("got %q", got)
	}
}

func TestInstallRejectsUnknownVariant(t *testing.T) {
	dest, _, err := runInstallEnv(t, releaseDir(t, false), "x86_64", "SYS_TELEMETRY_VARIANT=cuda")
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "sys-telemetry-app")); statErr == nil {
		t.Fatal("binary installed despite failure")
	}
}
