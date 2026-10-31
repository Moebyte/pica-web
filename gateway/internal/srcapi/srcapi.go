// Package srcapi implements the semantic source API (/api/src/*): a uniform
// browsing surface over the supported comic sources (JM, PicACG). Sources talk
// to their upstream APIs through the shared SSRF-safe outbound pipeline and
// hand out server-signed /api/img/<token> URLs for every image, so browsers
// never need source-specific headers or credentials.
package srcapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"picagateway/internal/downloads"
	"picagateway/internal/imgproxy"
	"picagateway/internal/outbound"
)

// Error codes used by StatusError.
const (
	CodeNotSupported       = "not_supported"
	CodeLoginRequired      = "login_required"
	CodeInvalidCredentials = "invalid_credentials"
	CodeNotFound           = "not_found"
	CodeUpstreamError      = "upstream_error"
	CodeInvalidRequest     = "invalid_request"
)

// StatusError is a source-level failure with an HTTP status attached. The HTTP
// layer maps it straight onto writeError.
type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func errNotSupported(message string) error {
	return &StatusError{Status: http.StatusBadRequest, Code: CodeNotSupported, Message: message}
}

func errLoginRequired(message string) error {
	return &StatusError{Status: http.StatusUnauthorized, Code: CodeLoginRequired, Message: message}
}

func errInvalidCredentials(message string) error {
	return &StatusError{Status: http.StatusUnauthorized, Code: CodeInvalidCredentials, Message: message}
}

func errNotFound(message string) error {
	return &StatusError{Status: http.StatusNotFound, Code: CodeNotFound, Message: message}
}

func errUpstream(format string, args ...any) error {
	return &StatusError{Status: http.StatusBadGateway, Code: CodeUpstreamError, Message: fmt.Sprintf(format, args...)}
}

func errBadRequest(code, format string, args ...any) error {
	return &StatusError{Status: http.StatusBadRequest, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Tab is one explore entry of a source.
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ComicBrief is a list entry (explore/search results).
type ComicBrief struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Subtitle  string   `json:"subtitle"`
	Author    string   `json:"author"`
	Cover     string   `json:"cover"`
	PageCount int      `json:"pageCount"`
	Chapters  int      `json:"chapters"`
	Tags      []string `json:"tags"`
}

// Chapter identifies one readable episode.
type Chapter struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

// Comic is the full comic document.
type Comic struct {
	ComicBrief
	Favorites int       `json:"favorites"`
	UploadAt  int64     `json:"uploadDate"`
	Tags      TagGroups `json:"tags"`
	Chapters  []Chapter `json:"chapters"`
	Thumbs    []string  `json:"thumbs"`
}

// TagGroups keeps the source-native tag grouping (e.g. JM: 作者/作品/登场人物/标签,
// PicACG: 分类/标签).
type TagGroups map[string][]string

// ListResult is a page of comics.
type ListResult struct {
	Comics  []ComicBrief `json:"comics"`
	Page    int          `json:"page"`
	MaxPage int          `json:"maxPage"`
}

// LoginResult reports a successful login.
type LoginResult struct {
	Name string `json:"name"`
}

// Source is one comic source. Implementations must be safe for concurrent use.
type Source interface {
	// Key is the URL identifier ("jm", "picacg").
	Key() string
	// Name is the display name.
	Name() string
	// NeedLogin reports whether browsing requires source credentials.
	NeedLogin() bool
	// LoggedIn reports whether credentials are currently available.
	LoggedIn(ctx context.Context) bool
	// Tabs lists the explore tabs.
	Tabs() []Tab
	// Explore lists one page of a tab.
	Explore(ctx context.Context, tab string, page int) (*ListResult, error)
	// Search queries the source; sort is source-specific and optional.
	Search(ctx context.Context, query, sort string, page int) (*ListResult, error)
	// Comic returns the full comic document.
	Comic(ctx context.Context, id string) (*Comic, error)
	// Pages returns signed /api/img/<token> URLs for one chapter.
	Pages(ctx context.Context, comicID, ep string) ([]string, error)
	// Login validates credentials and persists them.
	Login(ctx context.Context, username, password string) (*LoginResult, error)
	// Logout drops persisted credentials.
	Logout(ctx context.Context) error
}

// Downloader is an optional capability: a source that can resolve a chapter
// into raw upstream pages for the server-side download queue. Unlike Pages,
// no /api/img token is issued — the returned pages carry the direct upstream
// URL, the headers the origin needs and, for JM, the scramble parameters.
//
// Sources assert this interface where needed, so existing Source
// implementations stay valid without it.
type Downloader interface {
	DownloadPages(ctx context.Context, comicID, ep string) ([]downloads.Page, error)
}

// CredentialStore persists source credentials encrypted at rest. It is
// satisfied by *vault.Vault.
type CredentialStore interface {
	GetStore(tenant, name string) (string, error)
	PutStore(tenant, name, value string, now time.Time) error
	DeleteStore(tenant, name string) error
}

// Deps carries the shared gateway plumbing handed to every source.
type Deps struct {
	// Policy performs SSRF validation for every outbound URL.
	Policy *outbound.Policy
	// Transport is the validated outbound transport. Sources set their own
	// headers, so this must be the raw policy transport, not one that injects
	// cookies or a global User-Agent.
	Transport http.RoundTripper
	// Signer issues image tokens.
	Signer *imgproxy.Signer
	// TokenTTL is the image token lifetime.
	TokenTTL time.Duration
	// Timeout bounds a single upstream request.
	Timeout time.Duration
	// Tenant labels the credential store namespace.
	Tenant string
	// Store persists credentials (nil disables credential sources).
	Store CredentialStore
	// MaxBody caps one upstream response body (default 4 MiB).
	MaxBody int64
	Log     *slog.Logger
}

func (d *Deps) maxBody() int64 {
	if d.MaxBody <= 0 {
		return 4 << 20
	}
	return d.MaxBody
}

func (d *Deps) logger() *slog.Logger {
	if d.Log == nil {
		return slog.Default()
	}
	return d.Log
}

func (d *Deps) tokenTTL() time.Duration {
	if d.TokenTTL <= 0 {
		return time.Hour
	}
	return d.TokenTTL
}

// signImage issues a signed image URL for the gateway-relative path.
func (d *Deps) signImage(rawURL, profile string, jm *imgproxy.JMParams, now time.Time) string {
	token, _ := d.Signer.SignPayload(imgproxy.Payload{URL: rawURL, Profile: profile, JM: jm}, d.tokenTTL(), now)
	return "/api/img/" + token
}

// response is one upstream reply, already size-capped.
type response struct {
	Status      int
	Body        []byte
	ContentType string
	Header      http.Header
}

// do performs one outbound request through the policy pipeline. customize may
// be nil; besides headers it can set request-level fields such as Host.
// Non-2xx statuses are returned as-is (the caller decides); policy failures
// come back as *outbound.Error.
func (d *Deps) do(ctx context.Context, method, rawURL string, body []byte, customize func(*http.Request)) (*response, error) {
	if d.Transport == nil || d.Policy == nil {
		return nil, errors.New("srcapi: Deps.Transport and Deps.Policy are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}

	target, _, err := d.Policy.ValidateURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bodyReader)
	if err != nil {
		return nil, errBadRequest(CodeInvalidRequest, "invalid request: %v", err)
	}
	if customize != nil {
		customize(req)
	}

	var redirectErr error
	client := &http.Client{
		Transport: d.Transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return outbound.ErrTooManyRedirects
			}
			if _, _, err := d.Policy.ValidateURL(next.Context(), next.URL.String()); err != nil {
				redirectErr = err
				return err
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if redirectErr != nil {
			return nil, redirectErr
		}
		if errors.Is(err, outbound.ErrTooManyRedirects) {
			return nil, errUpstream("too many redirects for %s", rawURL)
		}
		if ctx.Err() != nil {
			return nil, errUpstream("upstream request timed out")
		}
		return nil, errUpstream("%s", err)
	}
	defer resp.Body.Close()

	// Same logging convention as the tunnel: 4xx Info / 5xx Warn, with the
	// target address so reports can be correlated.
	if resp.StatusCode >= 400 {
		level := slog.LevelInfo
		if resp.StatusCode >= 500 {
			level = slog.LevelWarn
		}
		d.logger().Log(ctx, level, "src upstream status", "target", rawURL, "status", resp.StatusCode)
	}

	buf, err := io.ReadAll(io.LimitReader(resp.Body, d.maxBody()+1))
	if err != nil {
		return nil, errUpstream("read %s: %v", rawURL, err)
	}
	if int64(len(buf)) > d.maxBody() {
		return nil, errUpstream("response from %s exceeds the %d byte limit", rawURL, d.maxBody())
	}
	return &response{Status: resp.StatusCode, Body: buf, ContentType: resp.Header.Get("Content-Type"), Header: resp.Header}, nil
}

func (d *Deps) get(ctx context.Context, rawURL string, customize func(*http.Request)) (*response, error) {
	return d.do(ctx, http.MethodGet, rawURL, nil, customize)
}

// Registry maps source keys to implementations.
type Registry struct {
	byKey map[string]Source
	order []string
}

// NewRegistry builds the default registry (jm + picacg).
func NewRegistry(deps Deps) *Registry {
	r := &Registry{byKey: make(map[string]Source, 2)}
	r.Register(newJM(deps))
	r.Register(newPicacg(deps))
	return r
}

// NewRegistryFrom builds a registry from explicit sources in order. The
// default wiring is NewRegistry; this constructor exists so callers (tests)
// can substitute the built-in sources.
func NewRegistryFrom(sources ...Source) *Registry {
	r := &Registry{byKey: make(map[string]Source, len(sources))}
	for _, src := range sources {
		r.Register(src)
	}
	return r
}

// Register adds a source (used by NewRegistry; exposed for tests).
func (r *Registry) Register(s Source) {
	if _, exists := r.byKey[s.Key()]; exists {
		return
	}
	r.byKey[s.Key()] = s
	r.order = append(r.order, s.Key())
}

// Get looks a source up by key.
func (r *Registry) Get(key string) (Source, bool) {
	s, ok := r.byKey[key]
	return s, ok
}

// Sources returns all sources in registration order.
func (r *Registry) Sources() []Source {
	out := make([]Source, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.byKey[k])
	}
	return out
}
