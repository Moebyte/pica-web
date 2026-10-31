package srcapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"picagateway/internal/downloads"
	"picagateway/internal/imgproxy"
)

// jmImageProfile is the header profile key used for JM image tokens. The
// server registers matching headers at startup.
const jmImageProfile = "jm"

// jmSource browses JM (禁漫天堂) without login.
type jmSource struct {
	deps Deps

	// domainListURLs and seedDomains default to the package constants; tests
	// point them at local servers.
	domainListURLs []string
	seedDomains    []string

	mu        sync.Mutex
	domains   []string
	pref      int // sticky index of the last working domain
	refetched time.Time

	imgMu      sync.Mutex
	imgBase    string
	imgFetched time.Time
}

func newJM(deps Deps) *jmSource {
	return &jmSource{deps: deps, domainListURLs: jmDomainURLs, seedDomains: jmAPIDomainsSeed}
}

func (s *jmSource) listURLs() []string {
	if len(s.domainListURLs) > 0 {
		return s.domainListURLs
	}
	return jmDomainURLs
}

func (s *jmSource) seeds() []string {
	if len(s.seedDomains) > 0 {
		return s.seedDomains
	}
	return jmAPIDomainsSeed
}

func (s *jmSource) Key() string                   { return "jm" }
func (s *jmSource) Name() string                  { return "禁漫天堂" }
func (s *jmSource) NeedLogin() bool               { return false }
func (s *jmSource) LoggedIn(context.Context) bool { return false }

func (s *jmSource) Tabs() []Tab {
	return []Tab{
		{ID: "home", Title: "主页"},
		{ID: "latest", Title: "最新"},
	}
}

func (s *jmSource) Login(context.Context, string, string) (*LoginResult, error) {
	return nil, errNotSupported("jm v1 API is browsable without login; login is not supported")
}

func (s *jmSource) Logout(context.Context) error { return nil }

// ---- domain management ----

// refreshDomains fetches the encrypted live server list. Results are cached in
// memory; callers invalidate on failure so the next request refetches.
func (s *jmSource) refreshDomains(ctx context.Context) []string {
	for _, listURL := range s.listURLs() {
		resp, err := s.deps.get(ctx, listURL, func(req *http.Request) {
			jmBaseHeaders(req.Header)
			req.Header.Set("User-Agent", JMUserAgent)
		})
		if err != nil {
			s.deps.logger().Warn("src jm: domain list fetch failed", "target", listURL, "error", err.Error())
			continue
		}
		if resp.Status != http.StatusOK {
			continue
		}
		// The list ships with a UTF-8 BOM in front of the base64 payload.
		body := strings.TrimSpace(strings.TrimPrefix(string(resp.Body), "\ufeff"))
		plain, err := jmDecrypt(body, jmDomainSecret)
		if err != nil {
			s.deps.logger().Warn("src jm: domain list decrypt failed", "target", listURL, "error", err.Error())
			continue
		}
		var doc struct {
			Server []string `json:"Server"`
		}
		if err := json.Unmarshal([]byte(plain), &doc); err != nil || len(doc.Server) == 0 {
			continue
		}
		if len(doc.Server) > 4 {
			doc.Server = doc.Server[:4]
		}
		s.mu.Lock()
		s.domains = doc.Server
		s.pref = 0
		s.refetched = time.Now()
		s.mu.Unlock()
		s.deps.logger().Info("src jm: api domains refreshed", "domains", strings.Join(doc.Server, ","))
		return doc.Server
	}
	return nil
}

func (s *jmSource) currentDomains(ctx context.Context) []string {
	s.mu.Lock()
	domains := append([]string(nil), s.domains...)
	s.mu.Unlock()
	if len(domains) > 0 {
		return domains
	}
	if fresh := s.refreshDomains(ctx); len(fresh) > 0 {
		return fresh
	}
	return s.seeds()
}

func (s *jmSource) invalidateDomains() {
	s.mu.Lock()
	s.domains = nil
	s.pref = 0
	s.mu.Unlock()
}

// ---- API client ----

// jmBaseHeaders mirrors getBaseHeaders in headers.dart. Authorization is
// deliberately absent: the bytepluses domain-list CDN rejects it with a 400.
func jmBaseHeaders(h http.Header) {
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	h.Set("Connection", "keep-alive")
	h.Set("Origin", "https://localhost")
	h.Set("Referer", "https://localhost/")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "cross-site")
	h.Set("Sec-Fetch-Storage-Access", "active")
	h.Set("X-Requested-With", "com.example.app")
}

// apiGet performs one JM API GET, decrypting the data envelope. Dead lines are
// skipped; when every line fails the domain list is refreshed and the pass is
// retried once (取线路→缓存→失效重取, ported from the Dart client).
func (s *jmSource) apiGet(ctx context.Context, apiPath string) (json.RawMessage, error) {
	domains := s.currentDomains(ctx)
	var lastErr error
	for round := 0; round < 2; round++ {
		s.mu.Lock()
		start := s.pref
		s.mu.Unlock()
		for i := 0; i < len(domains); i++ {
			idx := (start + i) % len(domains)
			raw, err := s.tryDomain(ctx, domains[idx], apiPath)
			if err == nil {
				s.mu.Lock()
				s.pref = idx
				s.mu.Unlock()
				return raw, nil
			}
			var se *StatusError
			if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
				// A 401 errorMsg is a protocol reply, not a dead line.
				return nil, err
			}
			lastErr = err
		}
		// Every line failed: refresh the server list once and retry.
		s.invalidateDomains()
		if fresh := s.refreshDomains(ctx); len(fresh) > 0 {
			domains = fresh
		}
	}
	if lastErr == nil {
		lastErr = errUpstream("no JM api line is reachable")
	}
	return nil, lastErr
}

// tryDomain issues the request against one API domain. Domain entries from
// the live server list are bare hostnames (https); tests may inject full URLs.
func (s *jmSource) tryDomain(ctx context.Context, domain, apiPath string) (json.RawMessage, error) {
	now := time.Now().Unix()
	reqURL := domain + apiPath
	if !strings.Contains(domain, "://") {
		reqURL = "https://" + domain + apiPath
	}
	resp, err := s.deps.get(ctx, reqURL, func(req *http.Request) {
		jmBaseHeaders(req.Header)
		req.Header.Set("Authorization", "Bearer") // getApiOptions adds it on API calls
		req.Header.Set("token", jmToken(now))
		req.Header.Set("tokenparam", jmTokenParam(now))
		req.Header.Set("User-Agent", JMUserAgent)
	})
	if err != nil {
		return nil, errUpstream("%s: %v", domain, err)
	}
	if resp.Status == http.StatusUnauthorized {
		msg := "unauthorized"
		var envelope struct {
			ErrorMsg string `json:"errorMsg"`
		}
		if json.Unmarshal(resp.Body, &envelope) == nil && envelope.ErrorMsg != "" {
			msg = envelope.ErrorMsg
		}
		return nil, &StatusError{Status: http.StatusUnauthorized, Code: CodeUpstreamError, Message: "jm: " + msg}
	}
	if resp.Status != http.StatusOK {
		return nil, errUpstream("jm line %s returned status %d", domain, resp.Status)
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return nil, errUpstream("jm line %s: invalid envelope: %v", domain, err)
	}
	// Unknown ids yield an empty array instead of an encrypted string; pass
	// the empty list through so list endpoints see "no results" and the
	// object endpoints (album/chapter) can map it to a 404.
	if data := strings.TrimSpace(string(envelope.Data)); data == "" || data == "[]" || data == "[ ]" {
		return json.RawMessage("[]"), nil
	}
	if envelope.Data[0] != '"' {
		return nil, errUpstream("jm line %s: unexpected data field %q", domain, string(envelope.Data[:min(len(envelope.Data), 40)]))
	}
	var encrypted string
	if err := json.Unmarshal(envelope.Data, &encrypted); err != nil {
		return nil, errUpstream("jm line %s: invalid data field: %v", domain, err)
	}
	if encrypted == "" {
		return nil, errUpstream("jm line %s: empty data field", domain)
	}
	plain, err := jmDecrypt(encrypted, fmt.Sprintf("%d%s", now, jmDataSecret))
	if err != nil {
		return nil, errUpstream("jm line %s: %v", domain, err)
	}
	return json.RawMessage(plain), nil
}

// imgBaseURL returns the image CDN base, refreshed from /setting at most once
// per hour. The factory default is used until then and kept as fallback.
func (s *jmSource) imgBaseURL(ctx context.Context) string {
	s.imgMu.Lock()
	if s.imgBase != "" && time.Since(s.imgFetched) < time.Hour {
		base := s.imgBase
		s.imgMu.Unlock()
		return base
	}
	s.imgMu.Unlock()

	base := jmImgBaseSeed
	if raw, err := s.apiGet(ctx, "/setting?app_img_shunt=1"); err == nil {
		var doc struct {
			ImgHost string `json:"img_host"`
		}
		if json.Unmarshal(raw, &doc) == nil &&
			(strings.HasPrefix(doc.ImgHost, "https://") || strings.HasPrefix(doc.ImgHost, "http://")) {
			base = strings.TrimSuffix(doc.ImgHost, "/")
		}
	}
	s.imgMu.Lock()
	s.imgBase = base
	s.imgFetched = time.Now()
	s.imgMu.Unlock()
	return base
}

// ---- endpoint mapping ----

func (s *jmSource) Explore(ctx context.Context, tab string, page int) (*ListResult, error) {
	switch tab {
	case "home":
		return s.exploreHome(ctx, page)
	case "latest":
		return s.exploreLatest(ctx, page)
	case "":
		return nil, errBadRequest("missing_tab", "tab query parameter is required")
	default:
		return nil, errBadRequest("unknown_tab", "unknown tab %q for jm", tab)
	}
}

// exploreHome flattens the promote sections into one list (single page).
func (s *jmSource) exploreHome(ctx context.Context, page int) (*ListResult, error) {
	raw, err := s.apiGet(ctx, "/promote?page=0")
	if err != nil {
		return nil, err
	}
	var sections []struct {
		Title   string           `json:"title"`
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, errUpstream("jm promote: %v", err)
	}
	comics := make([]ComicBrief, 0, 32)
	for _, sec := range sections {
		for _, c := range sec.Content {
			if b, ok := s.briefFromMap(ctx, c); ok {
				comics = append(comics, b)
			}
		}
	}
	return &ListResult{Comics: comics, Page: 1, MaxPage: 1}, nil
}

func (s *jmSource) exploreLatest(ctx context.Context, page int) (*ListResult, error) {
	raw, err := s.apiGet(ctx, fmt.Sprintf("/latest?page=%d", page))
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errUpstream("jm latest: %v", err)
	}
	comics := make([]ComicBrief, 0, len(items))
	for _, c := range items {
		if b, ok := s.briefFromMap(ctx, c); ok {
			comics = append(comics, b)
		}
	}
	// /latest carries no total; report a next page while items keep coming.
	maxPage := page
	if len(comics) > 0 {
		maxPage = page + 1
	}
	return &ListResult{Comics: comics, Page: page, MaxPage: maxPage}, nil
}

// jmSearchSorts are the orders the contract exposes.
var jmSearchSorts = map[string]bool{"mr": true, "mv": true, "mp": true, "mfm": true, "tp": true}

func (s *jmSource) Search(ctx context.Context, query, sort string, page int) (*ListResult, error) {
	query = strings.TrimSpace(query)
	query = strings.ReplaceAll(query, "  ", " ")
	if query == "" {
		return nil, errBadRequest("missing_query", "q query parameter is required")
	}
	if sort == "" {
		sort = "mr"
	}
	if !jmSearchSorts[sort] {
		return nil, errBadRequest("invalid_sort", "sort must be one of mr, mv, mp, mfm, tp")
	}
	raw, err := s.apiGet(ctx, fmt.Sprintf("/search?search_query=%s&o=%s&page=%d", url.QueryEscape(query), sort, page))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Content []map[string]any `json:"content"`
		Total   any              `json:"total"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errUpstream("jm search: %v", err)
	}
	comics := make([]ComicBrief, 0, len(doc.Content))
	for _, c := range doc.Content {
		if b, ok := s.briefFromMap(ctx, c); ok {
			comics = append(comics, b)
		}
	}
	total, _ := toInt(doc.Total)
	maxPage := page
	if len(comics) > 0 {
		pages := int((total + int64(len(comics)) - 1) / int64(len(comics)))
		if pages < page {
			pages = page
		}
		maxPage = pages
	}
	return &ListResult{Comics: comics, Page: page, MaxPage: maxPage}, nil
}

func (s *jmSource) Comic(ctx context.Context, id string) (*Comic, error) {
	if id == "" {
		return nil, errBadRequest("missing_id", "comic id is required")
	}
	raw, err := s.apiGet(ctx, "/album?id="+url.QueryEscape(id))
	if err != nil {
		return nil, err
	}
	if string(raw) == "[]" {
		return nil, errNotFound("jm album " + id + " does not exist")
	}
	var album struct {
		Name           string `json:"name"`
		Author         any    `json:"author"`
		Description    string `json:"description"`
		Likes          any    `json:"likes"`
		TotalFavorites any    `json:"total_favorites"`
		TotalPhotos    any    `json:"total_photos"`
		Addtime        any    `json:"addtime"`
		Series         []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Sort string `json:"sort"`
		} `json:"series"`
		Tags   []string `json:"tags"`
		Works  []string `json:"works"`
		Actors []string `json:"actors"`
	}
	if err := json.Unmarshal(raw, &album); err != nil {
		return nil, errUpstream("jm album: %v", err)
	}

	authors := toStringSlice(album.Author)
	favorites, _ := toInt(album.Likes)
	if f, ok := toInt(album.TotalFavorites); ok && f > favorites {
		favorites = f
	}
	uploadAt, _ := toInt(album.Addtime)
	photos, _ := toInt(album.TotalPhotos)

	chapters := make([]Chapter, 0, len(album.Series))
	for i, ep := range album.Series {
		if ep.ID == "" {
			continue
		}
		title := ep.Name
		if title == "" && ep.Sort != "" {
			title = "第" + ep.Sort + "話"
		}
		if title == "" {
			title = fmt.Sprintf("第%d話", i+1)
		}
		chapters = append(chapters, Chapter{ID: ep.ID, Title: title, Order: i + 1})
	}
	if len(chapters) == 0 {
		chapters = append(chapters, Chapter{ID: id, Title: "第1話", Order: 1})
	}

	groups := TagGroups{}
	appendGroup(groups, "作者", authors)
	appendGroup(groups, "作品", album.Works)
	appendGroup(groups, "登场人物", album.Actors)
	appendGroup(groups, "标签", album.Tags)

	brief := ComicBrief{
		ID:        id,
		Title:     album.Name,
		Subtitle:  album.Description,
		Author:    strings.Join(authors, ","),
		Cover:     s.deps.signImage(s.coverURL(ctx, id), jmImageProfile, nil, time.Now()),
		PageCount: int(photos),
		Chapters:  len(chapters),
		Tags:      append(append([]string{}, album.Tags...), album.Works...),
	}
	return &Comic{
		ComicBrief: brief,
		Favorites:  int(favorites),
		UploadAt:   uploadAt,
		Tags:       groups,
		Chapters:   chapters,
		Thumbs:     []string{},
	}, nil
}

// JMImageHeaders returns the headers the JM image CDN expects. It is the same
// set the gateway registers as the built-in "jm" image-header profile, shared
// by the image proxy and the server-side downloader.
func JMImageHeaders() map[string]string {
	return map[string]string{
		"Accept":                   "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
		"Accept-Language":          "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
		"Referer":                  "https://localhost/",
		"Sec-Fetch-Dest":           "image",
		"Sec-Fetch-Mode":           "no-cors",
		"Sec-Fetch-Site":           "cross-site",
		"Sec-Fetch-Storage-Access": "active",
		"User-Agent":               JMUserAgent,
		"X-Requested-With":         "com.example.app",
	}
}

// chapterImages fetches /chapter for ep and returns the raw image names plus
// the scramble id (defaulting to the app-wide constant).
func (s *jmSource) chapterImages(ctx context.Context, ep string) ([]string, string, error) {
	raw, err := s.apiGet(ctx, "/chapter?&id="+url.QueryEscape(ep))
	if err != nil {
		return nil, "", err
	}
	if string(raw) == "[]" {
		return nil, "", errNotFound("jm chapter " + ep + " does not exist")
	}
	var chapter struct {
		Images     []string `json:"images"`
		ScrambleID any      `json:"scramble_id"`
	}
	if err := json.Unmarshal(raw, &chapter); err != nil {
		return nil, "", errUpstream("jm chapter: %v", err)
	}
	if len(chapter.Images) == 0 {
		return nil, "", errNotFound("jm chapter has no images")
	}
	scramble := jmDefaultScramble
	if sc, ok := toString(chapter.ScrambleID); ok && sc != "" {
		scramble = sc
	}
	return chapter.Images, scramble, nil
}

// jmBookID strips the extension/query from an image file name ("00001.jpg" ->
// "00001"); the Dart client does the same before recombining.
func jmBookID(name string) string {
	bookID := name
	if i := strings.IndexAny(bookID, ".?"); i >= 0 {
		bookID = bookID[:i]
	}
	return bookID
}

// Pages lists one chapter's images as signed /api/img URLs carrying the JM
// scramble parameters so the proxy recombines JPEGs on cache misses.
func (s *jmSource) Pages(ctx context.Context, comicID, ep string) ([]string, error) {
	if ep == "" {
		return nil, errBadRequest("missing_ep", "ep query parameter (chapter id) is required")
	}
	images, scramble, err := s.chapterImages(ctx, ep)
	if err != nil {
		return nil, err
	}
	base := s.imgBaseURL(ctx)
	now := time.Now()
	pages := make([]string, 0, len(images))
	for _, name := range images {
		imgURL := fmt.Sprintf("%s/media/photos/%s/%s", base, ep, name)
		jm := &imgproxy.JMParams{EpsID: ep, ScrambleID: scramble, BookID: jmBookID(name)}
		pages = append(pages, s.deps.signImage(imgURL, jmImageProfile, jm, now))
	}
	return pages, nil
}

// DownloadPages resolves one chapter into raw upstream pages for the
// server-side download queue: direct CDN URLs, the headers the image CDN
// needs, and the JM scramble parameters. An empty ep falls back to the comic
// id, which is the chapter id of single-chapter albums.
func (s *jmSource) DownloadPages(ctx context.Context, comicID, ep string) ([]downloads.Page, error) {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		ep = strings.TrimSpace(comicID)
	}
	if ep == "" {
		return nil, errBadRequest("missing_ep", "ep (chapter id) is required")
	}
	images, scramble, err := s.chapterImages(ctx, ep)
	if err != nil {
		return nil, err
	}
	base := s.imgBaseURL(ctx)
	headers := JMImageHeaders()
	pages := make([]downloads.Page, 0, len(images))
	for _, name := range images {
		pages = append(pages, downloads.Page{
			URL:     fmt.Sprintf("%s/media/photos/%s/%s", base, ep, name),
			Headers: headers,
			JM:      &downloads.JMInfo{EpsID: ep, ScrambleID: scramble, BookID: jmBookID(name)},
		})
	}
	return pages, nil
}

func (s *jmSource) coverURL(ctx context.Context, id string) string {
	return fmt.Sprintf("%s/media/albums/%s_3x4.jpg", s.imgBaseURL(ctx), id)
}

// briefFromMap converts one JM comic map (latest/search/promote entry) into a
// ComicBrief with a signed cover URL.
func (s *jmSource) briefFromMap(ctx context.Context, c map[string]any) (ComicBrief, bool) {
	id, _ := toString(c["id"])
	if id == "" {
		return ComicBrief{}, false
	}
	title, _ := toString(c["name"])
	author, _ := toString(c["author"])
	subtitle, _ := toString(c["description"])

	var tags []string
	for _, key := range []string{"category", "category_sub"} {
		if m, ok := c[key].(map[string]any); ok {
			if t, _ := toString(m["title"]); t != "" {
				tags = appendUnique(tags, t)
			}
		}
	}
	brief := ComicBrief{
		ID:       id,
		Title:    title,
		Subtitle: subtitle,
		Author:   author,
		Cover:    s.deps.signImage(s.coverURL(ctx, id), jmImageProfile, nil, time.Now()),
		Tags:     tags,
	}
	return brief, true
}

// ---- small JSON helpers (JM mixes strings and numbers freely) ----

// toString accepts a JSON string or number.
func toString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatInt(int64(t), 10), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case nil:
		return "", false
	default:
		return "", false
	}
}

// toInt accepts a JSON number or numeric string.
func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int:
		return int64(t), true
	case int64:
		return t, true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// toStringSlice accepts a JSON string (single author), array of strings, or
// null; JM's album "author" is a list while briefs use a comma string.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := toString(item); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func appendGroup(groups TagGroups, key string, values []string) {
	filtered := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			filtered = append(filtered, v)
		}
	}
	if len(filtered) > 0 {
		groups[key] = filtered
	}
}

func appendUnique(dst []string, v string) []string {
	for _, existing := range dst {
		if existing == v {
			return dst
		}
	}
	return append(dst, v)
}
