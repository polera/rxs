package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	latestReleaseURL   = "https://api.github.com/repos/polera/rxs/releases/latest"
	maxAPIResponse     = 1 << 20
	maxChecksumFile    = 1 << 20
	maxArchive         = 100 << 20
	maxBinary          = 100 << 20
	maxExpandedArchive = 200 << 20
	httpTimeout        = 15 * time.Second
)

var ErrDevelopmentVersion = errors.New("the installed version is not a released semantic version")

var errArchiveTooLarge = errors.New("decompressed release archive is too large")

type Client struct {
	HTTPClient *http.Client
	LatestURL  string
	GOOS       string
	GOARCH     string
}

type Release struct {
	Version      string
	ArchiveName  string
	ArchiveURL   string
	ChecksumsURL string
}

type Result struct {
	Previous string
	Current  string
	Updated  bool
}

type releaseResponse struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: httpTimeout},
		LatestURL:  latestReleaseURL,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	}
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.latestURL(), nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	body, err := c.readHTTP(req, maxAPIResponse, "check GitHub releases: ", "read GitHub release: ", "GitHub release response is too large")
	if err != nil {
		return Release{}, err
	}
	var releaseData releaseResponse
	if err = json.Unmarshal(body, &releaseData); err != nil {
		return Release{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if _, err = parseVersion(releaseData.TagName); err != nil {
		return Release{}, fmt.Errorf("latest GitHub release has invalid version %q", releaseData.TagName)
	}

	archiveName := "rxs_" + c.goos() + "_" + c.goarch() + ".tar.gz"
	if c.goos() == "windows" {
		archiveName = "rxs_" + c.goos() + "_" + c.goarch() + ".zip"
	}
	release := Release{Version: releaseData.TagName, ArchiveName: archiveName}
	for _, asset := range releaseData.Assets {
		switch asset.Name {
		case archiveName:
			release.ArchiveURL = asset.URL
		case "checksums.txt":
			release.ChecksumsURL = asset.URL
		}
	}
	if release.ArchiveURL == "" {
		return Release{}, fmt.Errorf("release %s has no asset for %s/%s", release.Version, c.goos(), c.goarch())
	}
	if release.ChecksumsURL == "" {
		return Release{}, fmt.Errorf("release %s has no checksums.txt", release.Version)
	}
	return release, nil
}

func Available(installed, latest string) (bool, error) {
	installedVersion, err := parseVersion(installed)
	if err != nil {
		return false, fmt.Errorf("%w: %q", ErrDevelopmentVersion, installed)
	}
	latestVersion, err := parseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("invalid latest version %q", latest)
	}
	return semver.Compare(installedVersion, latestVersion) < 0, nil
}

func (c *Client) Upgrade(ctx context.Context, installed, executable string) (Result, error) {
	if _, err := Available(installed, installed); err != nil {
		return Result{}, err
	}
	release, err := c.Latest(ctx)
	if err != nil {
		return Result{}, err
	}
	return c.UpgradeTo(ctx, installed, executable, release)
}

// UpgradeTo installs a release already returned by Latest. This keeps an
// interactive offer tied to the exact release the user accepted.
func (c *Client) UpgradeTo(ctx context.Context, installed, executable string, release Release) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	available, err := Available(installed, release.Version)
	if err != nil {
		return Result{}, err
	}
	result := Result{Previous: installed, Current: release.Version}
	if !available {
		return result, nil
	}
	archive, err := c.download(ctx, release.ArchiveURL, maxArchive)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", release.ArchiveName, err)
	}
	checksumFile, err := c.download(ctx, release.ChecksumsURL, maxChecksumFile)
	if err != nil {
		return Result{}, fmt.Errorf("download checksums.txt: %w", err)
	}
	want, err := checksumFor(checksumFile, release.ArchiveName)
	if err != nil {
		return Result{}, err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, contextReader{ctx, bytes.NewReader(archive)}); err != nil {
		return Result{}, fmt.Errorf("checksum release archive: %w", err)
	}
	if !bytes.Equal(hash.Sum(nil), want) {
		return Result{}, fmt.Errorf("checksum mismatch for %s", release.ArchiveName)
	}
	binary, err := extractBinary(ctx, archive, release.ArchiveName)
	if err != nil {
		return Result{}, err
	}
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("locate current executable: %w", err)
		}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	if err = replaceExecutable(ctx, executable, binary); err != nil {
		return Result{}, err
	}
	result.Updated = true
	return result, nil
}

func (c *Client) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.readHTTP(req, limit, "", "", "download is too large")
}

// Keep request/read error context at the call sites while sharing the HTTP policy.
func (c *Client) readHTTP(req *http.Request, limit int64, requestPrefix, readPrefix, sizeMessage string) ([]byte, error) {
	req.Header.Set("User-Agent", "rxs-updater")
	response, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s%w", requestPrefix, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%sHTTP %s", requestPrefix, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s%w", readPrefix, err)
	}
	if int64(len(body)) > limit {
		return nil, errors.New(sizeMessage)
	}
	return body, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: httpTimeout}
}

func (c *Client) latestURL() string {
	if c.LatestURL != "" {
		return c.LatestURL
	}
	return latestReleaseURL
}

func (c *Client) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

func (c *Client) goarch() string {
	if c.GOARCH != "" {
		return c.GOARCH
	}
	return runtime.GOARCH
}

func checksumFor(data []byte, name string) ([]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimPrefix(fields[len(fields)-1], "*") != name {
			continue
		}
		checksum, err := hex.DecodeString(fields[0])
		if err != nil || len(checksum) != sha256.Size {
			return nil, fmt.Errorf("invalid checksum for %s", name)
		}
		return checksum, nil
	}
	return nil, fmt.Errorf("checksums.txt has no entry for %s", name)
}

func extractBinary(ctx context.Context, archive []byte, archiveName string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasSuffix(archiveName, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open release archive: %w", err)
		}
		for _, file := range reader.File {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if filepath.Base(file.Name) != "rxs.exe" || file.FileInfo().IsDir() {
				continue
			}
			if file.UncompressedSize64 > maxBinary {
				return nil, errors.New("binary in release archive is too large")
			}
			entry, openErr := file.Open()
			if openErr != nil {
				return nil, fmt.Errorf("open binary in release archive: %w", openErr)
			}
			binary, readErr := io.ReadAll(io.LimitReader(contextReader{ctx, entry}, maxBinary+1))
			closeErr := entry.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read binary in release archive: %w", readErr)
			}
			if closeErr != nil {
				return nil, closeErr
			}
			if len(binary) > maxBinary {
				return nil, errors.New("binary in release archive is too large")
			}
			return binary, nil
		}
		return nil, errors.New("release archive does not contain rxs.exe")
	}

	gzipArchive, err := gzip.NewReader(contextReader{ctx, bytes.NewReader(archive)})
	if err != nil {
		return nil, fmt.Errorf("open release archive: %w", err)
	}
	defer gzipArchive.Close()
	return extractTarBinary(ctx, gzipArchive, maxExpandedArchive)
}

func extractTarBinary(ctx context.Context, source io.Reader, limit int64) (binary []byte, err error) {
	// Limit below tar.Reader so skipped bodies, padding and hidden PAX/GNU
	// metadata all count. The extra byte distinguishes a full budget from EOF.
	bounded := &io.LimitedReader{R: contextReader{ctx, source}, N: limit + 1}
	defer func() {
		if bounded.N == 0 {
			binary, err = nil, errArchiveTooLarge
		}
	}()
	reader := tar.NewReader(bounded)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("open release archive: %w", err)
		}
		if binary != nil || filepath.Base(header.Name) != "rxs" || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size > maxBinary {
			return nil, errors.New("binary in release archive is too large")
		}
		binary, err = io.ReadAll(io.LimitReader(reader, maxBinary+1))
		if err != nil {
			return nil, fmt.Errorf("read binary in release archive: %w", err)
		}
		if len(binary) > maxBinary {
			return nil, errors.New("binary in release archive is too large")
		}
	}
	// TAR EOF can precede gzip EOF. Drain through the same budget to validate
	// the gzip trailer and count trailing data, including concatenated members.
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return nil, fmt.Errorf("finish release archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if binary == nil {
		return nil, errors.New("release archive does not contain rxs")
	}
	return binary, nil
}

// Local reads are cancellable between bounded chunks, not during a blocking OS
// call. Recheck after Read so cancellation at EOF cannot silently commit.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.reader.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

func writeReplacement(ctx context.Context, path string, binary io.Reader) (tempPath string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect current executable: %w", err)
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".rxs-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("create upgrade beside %s: %w", path, err)
	}
	tempPath = temp.Name()
	if _, err = io.Copy(temp, contextReader{ctx, binary}); err == nil {
		err = temp.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("write upgraded executable: %w", err)
	}
	return tempPath, nil
}

func parseVersion(value string) (string, error) {
	value = "v" + strings.TrimPrefix(strings.TrimSpace(value), "v")
	core, _, _ := strings.Cut(value, "-")
	core, _, _ = strings.Cut(core, "+")
	if strings.Count(core, ".") != 2 || !semver.IsValid(value) {
		return "", errors.New("expected a valid major.minor.patch semantic version")
	}
	return value, nil
}
