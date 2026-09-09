package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAvailable(t *testing.T) {
	tests := []struct {
		installed string
		latest    string
		want      bool
	}{
		{"v0.1.4", "v0.1.5", true},
		{"0.1.4", "v1.0.0", true},
		{"v1.2.3-beta.1", "v1.2.3", true},
		{"v1.2.3", "v1.2.3", false},
		{"v2.0.0", "v1.9.9", false},
		{"v1.2.3+build.1", "v1.2.3+build.2", false},
		{" \t1.2.3\n", " v1.2.4+001 ", true},
		{"v1.2.3-99999999999999999999", "v1.2.3-100000000000000000000", true},
		{"v1.2.3-99999999999999999999", "v1.2.3-alpha", true},
		{"v99999999999999999999.0.0", "v100000000000000000000.0.0", true},
	}
	for _, test := range tests {
		t.Run(test.installed+"_"+test.latest, func(t *testing.T) {
			got, err := Available(test.installed, test.latest)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("Available(%q, %q) = %v, want %v", test.installed, test.latest, got, test.want)
			}
		})
	}
	if _, err := Available("dev", "v1.0.0"); !errors.Is(err, ErrDevelopmentVersion) {
		t.Fatalf("development version error = %v", err)
	}
	if _, err := Available("v1.0.0", "latest"); err == nil {
		t.Fatal("expected invalid latest version error")
	}
}

func TestAvailablePrereleaseOrdering(t *testing.T) {
	chain := []string{"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2", "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0"}
	for i, installed := range chain {
		for j, latest := range chain {
			if got, err := Available(installed, latest); err != nil || got != (i < j) {
				t.Fatalf("Available(%q, %q) = %v, %v", installed, latest, got, err)
			}
		}
	}
}

func TestAvailableRejectsInvalidVersions(t *testing.T) {
	for _, version := range []string{
		"", "dev", "dev-abc123", "v1", "1.2", "v1.2+build.3", "v1.2.3.4",
		"V1.2.3", "vv1.2.3", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-01",
		"v1.2.3-00000000000000000000001", "v1.2.3-", "v1.2.3-alpha..1",
		"v1.2.3+", "v1.2.3+bad!", "v1.2.3+a..b", "v1.2.3+a+b", "v1.2.3\nx",
	} {
		t.Run(version, func(t *testing.T) {
			if _, err := Available(version, "v2.0.0"); !errors.Is(err, ErrDevelopmentVersion) {
				t.Fatalf("installed error = %v", err)
			}
			if _, err := Available("v1.0.0", version); err == nil || errors.Is(err, ErrDevelopmentVersion) {
				t.Fatalf("latest error = %v", err)
			}
		})
	}
}

func TestUpgradeToPreservesDisplayVersions(t *testing.T) {
	installed, latest := " 1.2.3+local ", "v1.2.3+release"
	result, err := (&Client{}).UpgradeTo(context.Background(), installed, "", Release{Version: latest})
	if err != nil || result.Previous != installed || result.Current != latest || result.Updated {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestUpgradeToCanceledAfterDownload(t *testing.T) {
	archive := tarGzip(t, "rxs", []byte("new binary"))
	sum := sha256.Sum256(archive)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := io.NopCloser(bytes.NewReader(archive))
		if req.URL.Path == "/checksums" {
			body = &cancelOnClose{Reader: strings.NewReader(fmt.Sprintf("%x  release.tar.gz\n", sum)), cancel: cancel}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header), Request: req}, nil
	})}}
	path := filepath.Join(t.TempDir(), "rxs")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := client.UpgradeTo(ctx, "v1.0.0", path, Release{
		Version: "v1.1.0", ArchiveName: "release.tar.gz",
		ArchiveURL: "https://release.test/archive", ChecksumsURL: "https://release.test/checksums",
	})
	if !errors.Is(err, context.Canceled) || result.Updated {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old binary" {
		t.Fatalf("executable = %q, %v", data, err)
	}
	assertNoStagingFiles(t, filepath.Dir(path))
}

type cancelOnClose struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelOnClose) Close() error {
	r.cancel()
	return nil
}

func TestLatestSelectsPlatformAssets(t *testing.T) {
	apiURL := "https://api.test/latest"
	response := []byte(`{"tag_name":"v1.2.0","assets":[{"name":"rxs_linux_arm64.tar.gz","browser_download_url":"https://release.test/archive"},{"name":"checksums.txt","browser_download_url":"https://release.test/checksums"}]}`)
	client := &Client{HTTPClient: routeClient(map[string][]byte{apiURL: response}), LatestURL: apiURL, GOOS: "linux", GOARCH: "arm64"}
	release, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1.2.0" || release.ArchiveName != "rxs_linux_arm64.tar.gz" {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestUpgradeToVerifiesAndReplacesExecutable(t *testing.T) {
	archive := tarGzip(t, "rxs", []byte("new binary"))
	sum := sha256.Sum256(archive)
	archiveURL := "https://release.test/archive"
	checksumsURL := "https://release.test/checksums"
	checksums := []byte(fmt.Sprintf("%x  rxs_linux_amd64.tar.gz\n", sum))

	executable := filepath.Join(t.TempDir(), "rxs")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTPClient: routeClient(map[string][]byte{archiveURL: archive, checksumsURL: checksums})}
	result, err := client.UpgradeTo(context.Background(), "v1.0.0", executable, Release{
		Version:      "v1.1.0",
		ArchiveName:  "rxs_linux_amd64.tar.gz",
		ArchiveURL:   archiveURL,
		ChecksumsURL: checksumsURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Current != "v1.1.0" {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Fatalf("executable = %q", got)
	}
}

func TestUpgradeToRejectsChecksumMismatch(t *testing.T) {
	archive := tarGzip(t, "rxs", []byte("new binary"))
	archiveURL := "https://release.test/archive"
	checksumsURL := "https://release.test/checksums"
	checksums := []byte(fmt.Sprintf("%064x  rxs_linux_amd64.tar.gz\n", 0))

	executable := filepath.Join(t.TempDir(), "rxs")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTPClient: routeClient(map[string][]byte{archiveURL: archive, checksumsURL: checksums})}
	_, err := client.UpgradeTo(context.Background(), "v1.0.0", executable, Release{
		Version: "v1.1.0", ArchiveName: "rxs_linux_amd64.tar.gz",
		ArchiveURL: archiveURL, ChecksumsURL: checksumsURL,
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	got, readErr := os.ReadFile(executable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old binary" {
		t.Fatalf("executable changed to %q", got)
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("rxs.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("windows binary"))
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractBinary(context.Background(), archive.Bytes(), "rxs_windows_amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows binary" {
		t.Fatalf("binary = %q", got)
	}
}

func tarGzip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func routeClient(routes map[string][]byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, ok := routes[request.URL.String()]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}
}
