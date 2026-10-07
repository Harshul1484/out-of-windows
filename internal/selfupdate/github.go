package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChecksumsName is the release asset that lists the SHA-256 of every other
// asset, in `sha256sum` format.
const ChecksumsName = "SHA256SUMS"

// MaxAssetSize bounds a download so a bad release cannot fill the disk.
const MaxAssetSize = 256 << 20

const maxChecksumsSize = 1 << 20

// DefaultAPIBase is the GitHub REST API.
const DefaultAPIBase = "https://api.github.com"

// Errors that callers may want to tell apart.
var (
	ErrNoRelease        = errors.New("no published release found")
	ErrNoAsset          = errors.New("the release has no build for this system")
	ErrChecksumMissing  = errors.New("the release has no checksum for this file")
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

// Source is where releases are published.
type Source struct {
	APIBase   string // e.g. https://api.github.com (tests use an httptest server)
	Repo      string // "owner/name"
	Token     string // optional; sent only to APIBase's host
	UserAgent string
	Client    *http.Client // nil means a client with sane timeouts
}

// Release is the subset of a GitHub release that update needs.
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	URL         string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	APIURL     string `json:"url"`
	BrowserURL string `json:"browser_download_url"`
}

// Version parses the release tag.
func (r *Release) Version() (Version, error) { return ParseVersion(r.Tag) }

// Find returns the asset with exactly this name.
func (r *Release) Find(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the name of the raw executable the release pipeline publishes
// for one architecture, e.g. oow-1.2.3-windows-amd64.exe.
func AssetName(name string, v Version, arch string) string {
	return fmt.Sprintf("%s-%s-windows-%s.exe", name, v, arch)
}

func (s Source) client() *http.Client {
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	// Never follow a redirect from HTTPS down to plain HTTP. (net/http
	// already drops the Authorization header on redirects to another host.)
	cc := *c
	cc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect from HTTPS to %s", req.URL.Scheme)
		}
		return nil
	}
	return &cc
}

func (s Source) apiHost() string {
	u, err := url.Parse(s.APIBase)
	if err != nil {
		return ""
	}
	return u.Host
}

func (s Source) newRequest(ctx context.Context, rawURL, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	ua := s.UserAgent
	if ua == "" {
		ua = "oow-selfupdate"
	}
	req.Header.Set("User-Agent", ua)
	if s.Token != "" && req.URL.Host == s.apiHost() {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	return req, nil
}

// Latest returns the newest published (non-draft, non-prerelease) release.
func (s Source) Latest(ctx context.Context) (*Release, error) {
	u := strings.TrimRight(s.APIBase, "/") + "/repos/" + s.Repo + "/releases/latest"
	req, err := s.newRequest(ctx, u, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, s.statusError(resp, s.Repo)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("unexpected answer from GitHub: %w", err)
	}
	if r.Draft || r.Prerelease {
		return nil, fmt.Errorf("%w: GitHub returned a draft or pre-release as the latest release", ErrNoRelease)
	}
	if _, err := r.Version(); err != nil {
		return nil, fmt.Errorf("the latest release tag %q is not a version: %w", r.Tag, err)
	}
	return &r, nil
}

// statusError turns an HTTP failure into a message that names the cause and
// the next step. It never includes the token.
func (s Source) statusError(resp *http.Response, what string) error {
	switch {
	case resp.StatusCode == http.StatusNotFound && s.Token == "":
		return fmt.Errorf("%w for %s: if the repository is private, set GITHUB_TOKEN or GH_TOKEN to a token that can read it", ErrNoRelease, what)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w for %s (the token in GITHUB_TOKEN/GH_TOKEN may not have access to %s)", ErrNoRelease, what, s.Repo)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("GitHub rejected the token in GITHUB_TOKEN/GH_TOKEN (401): check it, or unset it for a public repository")
	case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		resp.Header.Get("X-RateLimit-Remaining") == "0":
		hint := "try again later"
		if s.Token == "" {
			hint += ", or set GITHUB_TOKEN for a higher limit"
		}
		return fmt.Errorf("GitHub API rate limit reached while reading %s: %s", what, hint)
	}
	return fmt.Errorf("GitHub answered %s while reading %s", resp.Status, what)
}

// downloadURL picks the asset URL. With a token, the API URL is used (it is
// the only one that works for private repositories); without one, the public
// download URL, which does not count against the API rate limit.
func (s Source) downloadURL(a Asset) (string, string) {
	if s.Token != "" || a.BrowserURL == "" {
		return a.APIURL, "application/octet-stream"
	}
	return a.BrowserURL, "application/octet-stream"
}

// fetch streams an asset into w, refusing anything larger than limit.
func (s Source) fetch(ctx context.Context, a Asset, w io.Writer, limit int64) (int64, error) {
	u, accept := s.downloadURL(a)
	if u == "" {
		return 0, fmt.Errorf("asset %s has no download URL", a.Name)
	}
	req, err := s.newRequest(ctx, u, accept)
	if err != nil {
		return 0, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("download %s: GitHub answered %s", a.Name, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, s.statusError(resp, a.Name)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, fmt.Errorf("download %s: %w", a.Name, err)
	}
	if n > limit {
		return n, fmt.Errorf("download %s: larger than %d bytes, refusing it", a.Name, limit)
	}
	return n, nil
}

// Checksums downloads and parses the release's SHA256SUMS. A release
// without one cannot be verified, so it is an error, never skipped.
func (s Source) Checksums(ctx context.Context, r *Release) (map[string]string, error) {
	a, ok := r.Find(ChecksumsName)
	if !ok {
		return nil, fmt.Errorf("%w: release %s has no %s file, so nothing from it can be verified", ErrChecksumMissing, r.Tag, ChecksumsName)
	}
	var buf bytes.Buffer
	if _, err := s.fetch(ctx, a, &buf, maxChecksumsSize); err != nil {
		return nil, err
	}
	return ParseChecksums(buf.Bytes())
}

// ParseChecksums reads `sha256sum` output: "<64 hex>  <name>" or
// "<64 hex> *<name>" per line. Conflicting entries for one name are an error.
func ParseChecksums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	line := 0
	for sc.Scan() {
		line++
		t := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		sum, name, ok := strings.Cut(t, " ")
		name = strings.TrimPrefix(strings.TrimLeft(name, " "), "*")
		if !ok || name == "" || len(sum) != 64 {
			return nil, fmt.Errorf("%s line %d is malformed", ChecksumsName, line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("%s line %d is malformed", ChecksumsName, line)
		}
		sum = strings.ToLower(sum)
		if prev, dup := out[name]; dup && prev != sum {
			return nil, fmt.Errorf("%s lists two different checksums for %s", ChecksumsName, name)
		}
		out[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Plan is a verified-to-be-verifiable download: the asset for this
// architecture and the checksum it must have.
type Plan struct {
	Release *Release
	Version Version
	Asset   Asset
	SHA256  string
}

// PlanFor selects the executable for arch from r and its expected SHA-256.
// It fails closed: no asset, no checksum file, or no entry for the asset all
// stop the update.
func (s Source) PlanFor(ctx context.Context, r *Release, name, arch string) (*Plan, error) {
	v, err := r.Version()
	if err != nil {
		return nil, err
	}
	want := AssetName(name, v, arch)
	a, ok := r.Find(want)
	if !ok {
		return nil, fmt.Errorf("%w: release %s has no %s (windows/%s)", ErrNoAsset, r.Tag, want, arch)
	}
	if a.Size <= 0 || a.Size > MaxAssetSize {
		return nil, fmt.Errorf("release asset %s has an implausible size (%d bytes)", a.Name, a.Size)
	}
	sums, err := s.Checksums(ctx, r)
	if err != nil {
		return nil, err
	}
	sum, ok := sums[a.Name]
	if !ok {
		return nil, fmt.Errorf("%w: %s has no entry for %s; refusing an unverified file", ErrChecksumMissing, ChecksumsName, a.Name)
	}
	return &Plan{Release: r, Version: v, Asset: a, SHA256: sum}, nil
}

// Download streams the planned asset into w and verifies its size and
// SHA-256. On any mismatch it returns an error; the caller must discard
// whatever was written.
func (s Source) Download(ctx context.Context, p *Plan, w io.Writer) error {
	h := sha256.New()
	n, err := s.fetch(ctx, p.Asset, io.MultiWriter(w, h), p.Asset.Size)
	if err != nil {
		return err
	}
	if n != p.Asset.Size {
		return fmt.Errorf("download %s: got %d bytes, the release lists %d", p.Asset.Name, n, p.Asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != p.SHA256 {
		return fmt.Errorf("%w for %s: expected %s, got %s", ErrChecksumMismatch, p.Asset.Name, p.SHA256, got)
	}
	return nil
}
