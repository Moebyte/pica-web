package srcapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"picagateway/internal/vault"
)

// PicACG app constants, ported from app/lib/network/picacg_network/headers.dart.
const (
	picacgAPIBase = "https://picaapi.picacomic.com"
	picacgAPIKey  = "C69BAF41DA5ABD1FFEDC6D2FEA56B"
	// picacgHMACSecret is the HMAC key for the signature header.
	picacgHMACSecret = "~d}$Q7$eIni=V)9\\RK/P.RM4;9[7|@/CA}b~OW!3?EV`:<>M7pddUBL5n|0/*Cn"
	picacgAccept     = "application/vnd.picacomic.com.v1+json"
	// picacgCredKey stores credentials in the encrypted app store. Keys are
	// limited to [a-z0-9_-], so no colons.
	picacgCredKey = "src_picacg_cred"
	// picacgMaxListPages bounds the eps / chapter-image pagination loops.
	picacgMaxListPages = 100
)

// picacgCred is the encrypted-at-rest credential record.
type picacgCred struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
	Name     string `json:"name"`
}

// picacgSource browses PicACG; every endpoint needs the login token.
type picacgSource struct {
	deps Deps

	mu   sync.Mutex
	cred *picacgCred
}

func newPicacg(deps Deps) *picacgSource {
	return &picacgSource{deps: deps}
}

func (s *picacgSource) Key() string     { return "picacg" }
func (s *picacgSource) Name() string    { return "PicACG" }
func (s *picacgSource) NeedLogin() bool { return true }

func (s *picacgSource) LoggedIn(ctx context.Context) bool {
	cred := s.loadCred(ctx)
	return cred != nil && cred.Token != ""
}

func (s *picacgSource) Tabs() []Tab {
	return []Tab{
		{ID: "recommend", Title: "推荐"},
		{ID: "latest", Title: "最新"},
	}
}

// ---- credentials ----

func (s *picacgSource) loadCred(ctx context.Context) *picacgCred {
	s.mu.Lock()
	cred := s.cred
	s.mu.Unlock()
	if cred != nil {
		return cred
	}
	if s.deps.Store == nil {
		return nil
	}
	raw, err := s.deps.Store.GetStore(s.deps.Tenant, picacgCredKey)
	if err != nil {
		if !errors.Is(err, vault.ErrStoreNotFound) {
			s.deps.logger().Warn("src picacg: credential read failed", "error", err.Error())
		}
		return nil
	}
	var c picacgCred
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil
	}
	s.mu.Lock()
	s.cred = &c
	s.mu.Unlock()
	return &c
}

func (s *picacgSource) saveCred(cred *picacgCred) error {
	s.mu.Lock()
	s.cred = cred
	s.mu.Unlock()
	if s.deps.Store == nil {
		return nil
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return s.deps.Store.PutStore(s.deps.Tenant, picacgCredKey, string(raw), time.Now())
}

func (s *picacgSource) dropCred() error {
	s.mu.Lock()
	s.cred = nil
	s.mu.Unlock()
	if s.deps.Store == nil {
		return nil
	}
	return s.deps.Store.DeleteStore(s.deps.Tenant, picacgCredKey)
}

// ---- signing & transport ----

// picacgNonce returns a 32-hex-char nonce (the Dart client uses a UUID v1 with
// dashes stripped; uniqueness is what the API enforces).
func picacgNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x10 // version nibble, uuid-ish
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return hex.EncodeToString(b[:])
}

// picacgSignature mirrors createSignature in headers.dart:
// HMAC-SHA256(key=secret, msg=lower(path+time+nonce+METHOD+apiKey)) hex.
func picacgSignature(pathWithQuery, nonce, timeStr, method string) string {
	msg := strings.ToLower(pathWithQuery + timeStr + nonce + strings.ToUpper(method) + picacgAPIKey)
	mac := hmac.New(sha256.New, []byte(picacgHMACSecret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *picacgSource) headers(method, token, pathWithQuery string) (http.Header, string, string) {
	nonce := picacgNonce()
	timeStr := strconv.FormatInt(time.Now().Unix(), 10)
	h := http.Header{}
	h.Set("api-key", picacgAPIKey)
	h.Set("accept", picacgAccept)
	h.Set("app-channel", "3")
	h.Set("authorization", token)
	h.Set("time", timeStr)
	h.Set("nonce", nonce)
	h.Set("app-version", "2.2.1.3.3.4")
	h.Set("app-uuid", "defaultUuid")
	h.Set("image-quality", "original")
	h.Set("app-platform", "android")
	h.Set("app-build-version", "45")
	h.Set("Content-Type", "application/json; charset=UTF-8")
	h.Set("user-agent", "okhttp/3.8.1")
	h.Set("version", "v1.4.1")
	h.Set("Host", "picaapi.picacomic.com")
	h.Set("signature", picacgSignature(pathWithQuery, nonce, timeStr, method))
	return h, timeStr, nonce
}

// apiCall performs one signed API request. apiPath includes the query string.
// A 401 triggers one automatic re-login with the stored credentials (the
// Dart client does the same); body may be nil.
func (s *picacgSource) apiCall(ctx context.Context, method, apiPath string, body map[string]string) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		cred := s.loadCred(ctx)
		token := ""
		if cred != nil {
			token = cred.Token
		}
		raw, err := s.apiCallOnce(ctx, method, apiPath, body, token)
		if err == nil {
			return raw, nil
		}
		var se *StatusError
		if !errors.As(err, &se) || se.Status != http.StatusUnauthorized || attempt > 0 {
			return nil, err
		}
		// Token rejected: re-login once with the stored credentials.
		s.deps.logger().Info("src picacg: token rejected, re-logging in", "path", apiPath)
		if rerr := s.relogin(ctx); rerr != nil {
			return nil, errLoginRequired("picacg login expired and re-login failed; please sign in again")
		}
	}
	return nil, errLoginRequired("picacg login required")
}

// picacgAPIBaseOverride redirects the API base (tests only).
var picacgAPIBaseOverride string

func picacgBase() string {
	if picacgAPIBaseOverride != "" {
		return picacgAPIBaseOverride
	}
	return picacgAPIBase
}

func (s *picacgSource) apiCallOnce(ctx context.Context, method, apiPath string, body map[string]string, token string) (json.RawMessage, error) {
	fullURL := picacgBase() + "/" + strings.TrimPrefix(apiPath, "/")

	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	h, _, _ := s.headers(method, token, apiPath)
	resp, err := s.deps.do(ctx, method, fullURL, payload, func(req *http.Request) {
		for k, vv := range h {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
		// The app pins the Host header; Header.Set("Host") would be ignored.
		req.Host = "picaapi.picacomic.com"
	})
	if err != nil {
		return nil, errUpstream("%v", err)
	}

	var doc struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	parseErr := json.Unmarshal(resp.Body, &doc)
	switch {
	case resp.Status == http.StatusOK:
		if parseErr != nil {
			return nil, errUpstream("picacg: invalid response: %v", parseErr)
		}
		if doc.Message != "" && doc.Message != "success" {
			return nil, errUpstream("picacg: %s", doc.Message)
		}
		return doc.Data, nil
	case resp.Status == http.StatusUnauthorized:
		return nil, &StatusError{Status: http.StatusUnauthorized, Code: CodeLoginRequired, Message: "picacg: token rejected"}
	case resp.Status == http.StatusBadRequest:
		msg := "bad request"
		if parseErr == nil && doc.Message != "" {
			msg = doc.Message
		}
		return nil, &StatusError{Status: http.StatusBadRequest, Code: CodeUpstreamError, Message: "picacg: " + msg}
	default:
		return nil, errUpstream("picacg returned status %d", resp.Status)
	}
}

// ---- login ----

func (s *picacgSource) login(ctx context.Context, username, password string) (string, error) {
	raw, err := s.apiCallOnce(ctx, http.MethodPost, "auth/sign-in", map[string]string{
		"email":    username,
		"password": password,
	}, "")
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusBadRequest) {
			msg := strings.TrimPrefix(se.Message, "picacg: ")
			if msg == "" {
				msg = "invalid credentials"
			}
			return "", errInvalidCredentials(msg)
		}
		return "", err
	}
	var doc struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Token == "" {
		return "", errUpstream("picacg: login response has no token")
	}
	return doc.Token, nil
}

func (s *picacgSource) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errBadRequest("missing_credentials", "username and password are required")
	}
	token, err := s.login(ctx, username, password)
	if err != nil {
		return nil, err
	}
	name := username
	if raw, err := s.apiCallOnce(ctx, http.MethodGet, "users/profile", nil, token); err == nil {
		var profile struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		}
		if json.Unmarshal(raw, &profile) == nil && profile.User.Name != "" {
			name = profile.User.Name
		}
	}
	if err := s.saveCred(&picacgCred{Username: username, Password: password, Token: token, Name: name}); err != nil {
		return nil, &StatusError{Status: http.StatusInternalServerError, Code: "store_failed", Message: err.Error()}
	}
	return &LoginResult{Name: name}, nil
}

// relogin refreshes the token from the stored username/password.
func (s *picacgSource) relogin(ctx context.Context) error {
	cred := s.loadCred(ctx)
	if cred == nil || cred.Username == "" || cred.Password == "" {
		return errors.New("no stored credentials")
	}
	token, err := s.login(ctx, cred.Username, cred.Password)
	if err != nil {
		// Keep the record (so loggedIn reports false) but drop the stale token
		// to avoid hammering sign-in on every request.
		_ = s.saveCred(&picacgCred{Username: cred.Username, Password: cred.Password})
		return err
	}
	cred.Token = token
	return s.saveCred(cred)
}

func (s *picacgSource) Logout(ctx context.Context) error {
	if err := s.dropCred(); err != nil {
		return &StatusError{Status: http.StatusInternalServerError, Code: "store_failed", Message: err.Error()}
	}
	return nil
}

// requireCred gates the browsing endpoints.
func (s *picacgSource) requireCred(ctx context.Context) (*picacgCred, error) {
	cred := s.loadCred(ctx)
	if cred == nil || cred.Token == "" {
		return nil, errLoginRequired("picacg login required")
	}
	return cred, nil
}

// ---- endpoints ----

func (s *picacgSource) Explore(ctx context.Context, tab string, page int) (*ListResult, error) {
	if _, err := s.requireCred(ctx); err != nil {
		return nil, err
	}
	switch tab {
	case "recommend":
		return s.exploreRecommend(ctx, page)
	case "latest":
		return s.exploreLatest(ctx, page)
	case "":
		return nil, errBadRequest("missing_tab", "tab query parameter is required")
	default:
		return nil, errBadRequest("unknown_tab", "unknown tab %q for picacg", tab)
	}
}

// exploreRecommend flattens the two collections groups (本子母/本子妹); the
// endpoint returns everything on one page.
func (s *picacgSource) exploreRecommend(ctx context.Context, page int) (*ListResult, error) {
	raw, err := s.apiCall(ctx, http.MethodGet, "collections", nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Collections []struct {
			Comics []map[string]any `json:"comics"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errUpstream("picacg collections: %v", err)
	}
	var comics []ComicBrief
	for _, group := range doc.Collections {
		for _, c := range group.Comics {
			if b, ok := picacgBrief(c, &s.deps); ok {
				comics = append(comics, b)
			}
		}
	}
	return &ListResult{Comics: comics, Page: 1, MaxPage: 1}, nil
}

func (s *picacgSource) exploreLatest(ctx context.Context, page int) (*ListResult, error) {
	return s.comicPage(ctx, fmt.Sprintf("comics?page=%d&s=dd", page), nil)
}

func (s *picacgSource) Search(ctx context.Context, query, sort string, page int) (*ListResult, error) {
	if _, err := s.requireCred(ctx); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errBadRequest("missing_query", "q query parameter is required")
	}
	if sort == "" {
		sort = "dd"
	}
	return s.comicPage(ctx, fmt.Sprintf("comics/advanced-search?page=%d", page), map[string]string{
		"keyword": query,
		"sort":    sort,
	})
}

// comicPage handles the shared data.comics {docs,pages} shape (latest list and
// advanced search).
func (s *picacgSource) comicPage(ctx context.Context, apiPath string, body map[string]string) (*ListResult, error) {
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	raw, err := s.apiCall(ctx, method, apiPath, body)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Comics struct {
			Docs  []map[string]any `json:"docs"`
			Pages int              `json:"pages"`
			Total int              `json:"total"`
		} `json:"comics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errUpstream("picacg comics: %v", err)
	}
	comics := make([]ComicBrief, 0, len(doc.Comics.Docs))
	for _, c := range doc.Comics.Docs {
		if b, ok := picacgBrief(c, &s.deps); ok {
			comics = append(comics, b)
		}
	}
	return &ListResult{Comics: comics, Page: pageOf(apiPath), MaxPage: maxInt(1, doc.Comics.Pages)}, nil
}

func (s *picacgSource) Comic(ctx context.Context, id string) (*Comic, error) {
	if _, err := s.requireCred(ctx); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errBadRequest("missing_id", "comic id is required")
	}
	raw, err := s.apiCall(ctx, http.MethodGet, "comics/"+urlPathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Comic struct {
			Title       string    `json:"title"`
			Author      string    `json:"author"`
			Team        string    `json:"chineseTeam"`
			Description string    `json:"description"`
			Thumb       imageRef  `json:"thumb"`
			Categories  []string  `json:"categories"`
			Tags        []string  `json:"tags"`
			LikesCount  any       `json:"likesCount"`
			EpsCount    any       `json:"epsCount"`
			PagesCount  any       `json:"pagesCount"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"comic"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errUpstream("picacg comic: %v", err)
	}
	c := doc.Comic

	chapters, err := s.fetchChapters(ctx, id)
	if err != nil {
		return nil, err
	}

	favorites, _ := toInt(c.LikesCount)
	eps, _ := toInt(c.EpsCount)
	pages, _ := toInt(c.PagesCount)

	groups := TagGroups{}
	appendGroup(groups, "分类", c.Categories)
	appendGroup(groups, "标签", c.Tags)

	now := time.Now()
	brief := ComicBrief{
		ID:        id,
		Title:     c.Title,
		Author:    c.Author,
		Cover:     s.deps.signImage(c.Thumb.URL(), "", nil, now),
		PageCount: int(pages),
		Chapters:  int(eps),
		Tags:      append(append([]string{}, c.Categories...), c.Tags...),
	}
	if c.Team != "" {
		brief.Subtitle = c.Team
	}
	return &Comic{
		ComicBrief: brief,
		Favorites:  int(favorites),
		UploadAt:   c.UpdatedAt.Unix(),
		Tags:       groups,
		Chapters:   chapters,
		Thumbs:     []string{},
	}, nil
}

// fetchChapters pages through /comics/{id}/eps. The API returns docs
// newest-first; the chapter id is the 1-based order used by
// /comics/{id}/order/{order}/pages (the Dart client derives it by reversing
// the list).
func (s *picacgSource) fetchChapters(ctx context.Context, id string) ([]Chapter, error) {
	var docs []map[string]any
	for p := 1; p <= picacgMaxListPages; p++ {
		raw, err := s.apiCall(ctx, http.MethodGet, fmt.Sprintf("comics/%s/eps?page=%d", urlPathEscape(id), p), nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Eps struct {
				Docs  []map[string]any `json:"docs"`
				Pages int              `json:"pages"`
			} `json:"eps"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, errUpstream("picacg eps: %v", err)
		}
		docs = append(docs, page.Eps.Docs...)
		if page.Eps.Pages <= p {
			break
		}
	}
	if len(docs) == 0 {
		return []Chapter{{ID: "1", Title: "第1话", Order: 1}}, nil
	}
	chapters := make([]Chapter, 0, len(docs))
	n := len(docs)
	for j, d := range docs {
		order := int64(n - j)
		if o, ok := toInt(d["order"]); ok && o > 0 {
			order = o
		}
		title, _ := toString(d["title"])
		if title == "" {
			title = fmt.Sprintf("第%d话", order)
		}
		chapters = append(chapters, Chapter{
			ID:    strconv.FormatInt(order, 10),
			Title: title,
			Order: int(order),
		})
	}
	// present in reading order
	for i := 0; i < len(chapters)/2; i++ {
		j := len(chapters) - 1 - i
		chapters[i], chapters[j] = chapters[j], chapters[i]
	}
	return chapters, nil
}

// Pages lists one chapter's images, paging through the chapter image list.
func (s *picacgSource) Pages(ctx context.Context, comicID, ep string) ([]string, error) {
	if _, err := s.requireCred(ctx); err != nil {
		return nil, err
	}
	if ep == "" {
		return nil, errBadRequest("missing_ep", "ep query parameter (chapter order) is required")
	}
	order, err := strconv.Atoi(strings.TrimSpace(ep))
	if err != nil || order <= 0 {
		return nil, errBadRequest("invalid_ep", "ep must be a positive chapter order number")
	}
	var pages []string
	for p := 1; p <= picacgMaxListPages; p++ {
		raw, aerr := s.apiCall(ctx, http.MethodGet,
			fmt.Sprintf("comics/%s/order/%d/pages?page=%d", urlPathEscape(comicID), order, p), nil)
		if aerr != nil {
			return nil, aerr
		}
		var doc struct {
			Pages struct {
				Docs []struct {
					Media imageRef `json:"media"`
				} `json:"docs"`
				Pages int `json:"pages"`
			} `json:"pages"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, errUpstream("picacg pages: %v", err)
		}
		now := time.Now()
		for _, d := range doc.Pages.Docs {
			pages = append(pages, s.deps.signImage(d.Media.URL(), "", nil, now))
		}
		if doc.Pages.Pages <= p {
			break
		}
	}
	if len(pages) == 0 {
		return nil, errNotFound("picacg chapter has no images")
	}
	return pages, nil
}

// imageRef is the {fileServer, path} pair picacg uses for every image.
type imageRef struct {
	FileServer string `json:"fileServer"`
	Path       string `json:"path"`
}

func (i imageRef) URL() string {
	if i.FileServer == "" || i.Path == "" {
		return ""
	}
	return strings.TrimSuffix(i.FileServer, "/") + "/static/" + strings.TrimPrefix(i.Path, "/")
}

// picacgBrief converts one comics doc into a ComicBrief.
func picacgBrief(c map[string]any, deps *Deps) (ComicBrief, bool) {
	id, _ := toString(c["_id"])
	if id == "" {
		return ComicBrief{}, false
	}
	title, _ := toString(c["title"])
	author, _ := toString(c["author"])
	pagesCount, _ := toInt(c["pagesCount"])

	var thumb imageRef
	if m, ok := c["thumb"].(map[string]any); ok {
		thumb.FileServer, _ = toString(m["fileServer"])
		thumb.Path, _ = toString(m["path"])
	}
	var tags []string
	for _, key := range []string{"tags", "categories"} {
		if list, ok := c[key].([]any); ok {
			for _, item := range list {
				if t, ok := toString(item); ok && t != "" {
					tags = appendUnique(tags, t)
				}
			}
		}
	}
	return ComicBrief{
		ID:        id,
		Title:     title,
		Author:    author,
		Cover:     deps.signImage(thumb.URL(), "", nil, time.Now()),
		PageCount: int(pagesCount),
		Tags:      tags,
	}, true
}

func pageOf(apiPath string) int {
	if i := strings.Index(apiPath, "page="); i >= 0 {
		if n, err := strconv.Atoi(apiPath[i+5:]); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func urlPathEscape(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "/", "%2F")
}
