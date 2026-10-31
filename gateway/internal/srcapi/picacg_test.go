package srcapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- signature ----

func TestPicacgSignature(t *testing.T) {
	// Vector generated with the upstream scheme:
	// HMAC-SHA256(key=secret, msg=lower(path+time+nonce+METHOD+apiKey)).
	got := picacgSignature("comics?page=1&s=dd", "abcdef0123456789abcdef0123456789", "1700000000", "GET")
	if got != "820353b8fc4037006e2aa5f5767aaf8addf2d0bd6caef94cec16075e1142630d" {
		t.Fatalf("signature = %q", got)
	}
}

func TestPicacgNonce(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		n := picacgNonce()
		if len(n) != 32 || strings.Contains(n, "-") {
			t.Fatalf("nonce malformed: %q", n)
		}
		if seen[n] {
			t.Fatalf("nonce repeated: %q", n)
		}
		seen[n] = true
	}
}

// ---- fake picacg ----

// fakePicacg validates the signed headers on every request.
type fakePicacg struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	signIns   int
	nextToken int
}

func (f *fakePicacg) newToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextToken++
	return "token-" + strings.Repeat("x", f.nextToken)
}

func (f *fakePicacg) signInsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.signIns
}

// authOK accepts the "token-N" strings issued by sign-in.
func authOK(auth string) bool {
	return strings.HasPrefix(auth, "token-")
}

func newFakePicacg(t *testing.T) *fakePicacg {
	t.Helper()
	f := &fakePicacg{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.checkHeaders(r)
		auth := r.Header.Get("authorization")

		write := func(status int, doc map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(doc)
		}
		unauthorized := func() {
			write(http.StatusUnauthorized, map[string]any{"code": 401, "message": "unauthorized"})
		}

		switch {
		case r.URL.Path == "/auth/sign-in":
			body, _ := io.ReadAll(r.Body)
			var cred struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			_ = json.Unmarshal(body, &cred)
			f.mu.Lock()
			f.signIns++
			f.mu.Unlock()
			if cred.Email == "user@example.com" && cred.Password == "correct" {
				write(http.StatusOK, map[string]any{
					"code": 200, "message": "success",
					"data": map[string]any{"token": f.newToken()},
				})
				return
			}
			write(http.StatusBadRequest, map[string]any{
				"code": 400, "error": "1004", "message": "invalid email or password",
			})

		case r.URL.Path == "/users/profile":
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"user": map[string]any{"name": "显示名"}},
			})

		case r.URL.Path == "/collections":
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"collections": []map[string]any{
					{"comics": []map[string]any{
						{"_id": "c1", "title": "推荐一", "author": "作者甲", "totalLikes": 120,
							"pagesCount": 24,
							"thumb":      map[string]any{"fileServer": "https://static001.picacomic.com", "path": "t/1.jpg"},
							"tags":       []string{"全彩"}, "categories": []string{"短篇"}},
					}},
					{"comics": []map[string]any{
						{"_id": "c2", "title": "推荐二", "author": "作者乙",
							"thumb": map[string]any{"fileServer": "https://static001.picacomic.com", "path": "t/2.jpg"}},
					}},
				}},
			})

		case r.URL.Path == "/comics":
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"comics": map[string]any{
					"pages": 3,
					"total": 90,
					"docs": []map[string]any{
						{"_id": "L1", "title": "最新一", "author": "作者丙", "likesCount": "8",
							"pagesCount": 10,
							"thumb":      map[string]any{"fileServer": "https://static001.picacomic.com", "path": "l/1.webp"}},
					},
				}},
			})

		case r.URL.Path == "/comics/advanced-search":
			if !authOK(auth) {
				unauthorized()
				return
			}
			body, _ := io.ReadAll(r.Body)
			var q struct {
				Keyword string `json:"keyword"`
				Sort    string `json:"sort"`
			}
			_ = json.Unmarshal(body, &q)
			if q.Keyword != "keyword" || q.Sort != "dd" {
				write(http.StatusBadRequest, map[string]any{"code": 400, "message": "invalid keyword"})
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"comics": map[string]any{
					"pages": 2,
					"docs": []map[string]any{
						{"_id": "S1", "title": "搜索结果", "author": "作者丁",
							"thumb": map[string]any{"fileServer": "https://static001.picacomic.com", "path": "s/1.jpg"},
							"tags":  []string{"中文"}},
					},
				}},
			})

		case strings.HasPrefix(r.URL.Path, "/comics/") && strings.HasSuffix(r.URL.Path, "/eps"):
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"eps": map[string]any{
					"pages": 1,
					"docs": []map[string]any{
						{"title": "第2话", "order": 2},
						{"title": "第1话", "order": 1},
					},
				}},
			})

		case r.URL.Path == "/comics/L1":
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"comic": map[string]any{
					"_id": "L1", "title": "最新一", "author": "作者丙", "chineseTeam": "汉化组",
					"description": "描述文本",
					"thumb":       map[string]any{"fileServer": "https://static001.picacomic.com", "path": "l/1.webp"},
					"categories":  []string{"短篇"}, "tags": []string{"全彩", "中文"},
					"likesCount": 8, "epsCount": 2, "pagesCount": 10,
					"updated_at": "2024-05-06T07:21:34.977Z",
				}},
			})

		case strings.Contains(r.URL.Path, "/order/"):
			if !authOK(auth) {
				unauthorized()
				return
			}
			write(http.StatusOK, map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{"pages": map[string]any{
					"pages": 1,
					"docs": []map[string]any{
						{"media": map[string]any{"fileServer": "https://static001.picacomic.com", "path": "p/1.jpg"}},
						{"media": map[string]any{"fileServer": "https://static001.picacomic.com/", "path": "p/2.jpg"}},
					},
				}},
			})

		default:
			write(http.StatusNotFound, map[string]any{"code": 404, "message": "not found"})
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// checkHeaders recomputes and verifies the signature header server-side.
func (f *fakePicacg) checkHeaders(r *http.Request) {
	nonce := r.Header.Get("nonce")
	timeStr := r.Header.Get("time")
	sig := r.Header.Get("signature")
	if len(nonce) != 32 || timeStr == "" {
		f.t.Errorf("nonce/time headers malformed: %q %q", nonce, timeStr)
		return
	}
	// The app signs the path relative to the API root (no leading slash).
	want := picacgSignature(strings.TrimPrefix(r.URL.RequestURI(), "/"), nonce, timeStr, r.Method)
	if sig != want {
		f.t.Errorf("signature mismatch for %s %s: got %q want %q", r.Method, r.URL.RequestURI(), sig, want)
	}
	if r.Header.Get("api-key") != picacgAPIKey {
		f.t.Errorf("api-key = %q", r.Header.Get("api-key"))
	}
	if r.Header.Get("accept") != picacgAccept {
		f.t.Errorf("accept = %q", r.Header.Get("accept"))
	}
	for _, name := range []string{"app-channel", "app-version", "app-build-version", "app-uuid",
		"app-platform", "image-quality", "version", "time", "nonce"} {
		if r.Header.Get(name) == "" {
			f.t.Errorf("picacg header %q missing", name)
		}
	}
	if r.Header.Get("user-agent") != "okhttp/3.8.1" {
		f.t.Errorf("user-agent = %q", r.Header.Get("user-agent"))
	}
	if r.Host != "picaapi.picacomic.com" {
		f.t.Errorf("Host = %q", r.Host)
	}
}

// memStore is an in-memory CredentialStore.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemStore() *memStore { return &memStore{m: map[string]string{}} }

func (s *memStore) GetStore(tenant, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[tenant+"/"+name]
	if !ok {
		return "", &StatusError{Status: 404, Code: "not_found", Message: "no value"}
	}
	return v, nil
}

func (s *memStore) PutStore(tenant, name, value string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[tenant+"/"+name] = value
	return nil
}

func (s *memStore) DeleteStore(tenant, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tenant+"/"+name)
	return nil
}

func newTestPicacg(t *testing.T) (*picacgSource, *fakePicacg) {
	t.Helper()
	f := newFakePicacg(t)
	deps := testDeps(t)
	deps.Store = newMemStore()
	// Point the API base at the fake server.
	picacgAPIBaseOverride = f.srv.URL
	t.Cleanup(func() { picacgAPIBaseOverride = "" })
	return newPicacg(deps), f
}

func TestPicacgLoginAndBrowse(t *testing.T) {
	s, _ := newTestPicacg(t)
	ctx := context.Background()

	if s.NeedLogin() != true {
		t.Fatal("picacg needLogin")
	}
	if s.LoggedIn(ctx) {
		t.Fatal("loggedIn before login")
	}
	// Browsing without credentials is a clean 401.
	if _, err := s.Explore(ctx, "latest", 1); err == nil {
		t.Fatal("explore without login succeeded")
	} else if se, ok := err.(*StatusError); !ok || se.Status != 401 || se.Code != CodeLoginRequired {
		t.Fatalf("explore error = %v", err)
	}
	if _, err := s.Comic(ctx, "L1"); err == nil {
		t.Fatal("comic without login succeeded")
	}
	if _, err := s.Pages(ctx, "L1", "1"); err == nil {
		t.Fatal("pages without login succeeded")
	}

	// Wrong credentials -> 401 invalid_credentials.
	if _, err := s.Login(ctx, "user@example.com", "wrong"); err == nil {
		t.Fatal("bad login accepted")
	} else if se, ok := err.(*StatusError); !ok || se.Status != 401 || se.Code != CodeInvalidCredentials {
		t.Fatalf("bad login error = %v", err)
	}

	// Correct login stores the credential and resolves the display name.
	res, err := s.Login(ctx, "user@example.com", "correct")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Name != "显示名" {
		t.Fatalf("login name = %q", res.Name)
	}
	if !s.LoggedIn(ctx) {
		t.Fatal("not loggedIn after login")
	}
	cred := s.loadCred(ctx)
	if cred.Username != "user@example.com" || cred.Password != "correct" || cred.Token == "" {
		t.Fatalf("stored cred = %+v", cred)
	}

	// Collections (recommend tab).
	rec, err := s.Explore(ctx, "recommend", 1)
	if err != nil {
		t.Fatalf("explore recommend: %v", err)
	}
	if len(rec.Comics) != 2 || rec.Comics[0].ID != "c1" || rec.Comics[1].ID != "c2" {
		t.Fatalf("recommend = %+v", rec.Comics)
	}
	if rec.Comics[0].PageCount != 24 {
		t.Fatalf("recommend pageCount = %d", rec.Comics[0].PageCount)
	}
	if !strings.HasPrefix(rec.Comics[0].Cover, "/api/img/") {
		t.Fatalf("cover not signed: %q", rec.Comics[0].Cover)
	}

	// Latest.
	latest, err := s.Explore(ctx, "latest", 1)
	if err != nil {
		t.Fatalf("explore latest: %v", err)
	}
	if len(latest.Comics) != 1 || latest.Comics[0].ID != "L1" || latest.MaxPage != 3 || latest.Page != 1 {
		t.Fatalf("latest = %+v page=%d max=%d", latest.Comics, latest.Page, latest.MaxPage)
	}

	// Search posts the keyword body.
	sr, err := s.Search(ctx, "keyword", "", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(sr.Comics) != 1 || sr.Comics[0].ID != "S1" || sr.Comics[0].Tags[0] != "中文" {
		t.Fatalf("search = %+v", sr.Comics)
	}
	if _, err := s.Search(ctx, "", "", 1); err == nil {
		t.Fatal("empty query accepted")
	}

	// Comic detail + chapters from eps.
	comic, err := s.Comic(ctx, "L1")
	if err != nil {
		t.Fatalf("comic: %v", err)
	}
	if comic.Title != "最新一" || comic.Author != "作者丙" || comic.Subtitle != "汉化组" {
		t.Fatalf("comic brief = %+v", comic.ComicBrief)
	}
	if comic.Favorites != 8 || comic.PageCount != 10 || len(comic.Chapters) != 2 {
		t.Fatalf("comic counts = %+v", comic.ComicBrief)
	}
	if comic.UploadAt != 1714980094 {
		t.Fatalf("uploadDate = %d", comic.UploadAt)
	}
	if len(comic.Tags["分类"]) != 1 || len(comic.Tags["标签"]) != 2 {
		t.Fatalf("tag groups = %+v", comic.Tags)
	}
	if len(comic.Chapters) != 2 || comic.Chapters[0].ID != "1" || comic.Chapters[0].Order != 1 ||
		comic.Chapters[1].ID != "2" || comic.Chapters[1].Title != "第2话" {
		t.Fatalf("chapters = %+v", comic.Chapters)
	}

	// Pages.
	pages, err := s.Pages(ctx, "L1", "1")
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %v", pages)
	}
	for _, p := range pages {
		if !strings.HasPrefix(p, "/api/img/") {
			t.Fatalf("page url not signed: %q", p)
		}
	}
	if _, err := s.Pages(ctx, "L1", "not-a-number"); err == nil {
		t.Fatal("non-numeric ep accepted")
	}

	// Logout clears everything.
	if err := s.Logout(ctx); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if s.LoggedIn(ctx) {
		t.Fatal("still loggedIn after logout")
	}
	if _, err := s.Explore(ctx, "latest", 1); err == nil {
		t.Fatal("explore after logout succeeded")
	}
}

func TestPicacgDownloadPages(t *testing.T) {
	s, _ := newTestPicacg(t)
	ctx := context.Background()

	// Credentials are still required.
	if _, err := s.DownloadPages(ctx, "L1", "1"); err == nil {
		t.Fatal("DownloadPages without login succeeded")
	} else if se, ok := err.(*StatusError); !ok || se.Status != http.StatusUnauthorized || se.Code != CodeLoginRequired {
		t.Fatalf("DownloadPages error = %v", err)
	}

	if _, err := s.Login(ctx, "user@example.com", "correct"); err != nil {
		t.Fatalf("login: %v", err)
	}

	pages, err := s.DownloadPages(ctx, "L1", "1")
	if err != nil {
		t.Fatalf("DownloadPages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %+v", pages)
	}
	wantURLs := []string{
		"https://static001.picacomic.com/static/p/1.jpg",
		"https://static001.picacomic.com/static/p/2.jpg",
	}
	for i, p := range pages {
		if p.URL != wantURLs[i] {
			t.Fatalf("page %d url = %q, want %q", i, p.URL, wantURLs[i])
		}
		if p.JM != nil {
			t.Fatalf("page %d has jm params: %+v", i, p.JM)
		}
		if len(p.Headers) != 0 {
			t.Fatalf("page %d headers = %+v", i, p.Headers)
		}
	}

	// Empty ep falls back to chapter order 1; invalid ep is a 400.
	fallback, err := s.DownloadPages(ctx, "L1", "")
	if err != nil {
		t.Fatalf("DownloadPages fallback: %v", err)
	}
	if len(fallback) != 2 {
		t.Fatalf("fallback pages = %+v", fallback)
	}
	if _, err := s.DownloadPages(ctx, "L1", "not-a-number"); err == nil {
		t.Fatal("invalid ep accepted")
	} else if se, ok := err.(*StatusError); !ok || se.Status != http.StatusBadRequest || se.Code != "invalid_ep" {
		t.Fatalf("invalid ep error = %v", err)
	}
}

func TestPicacgAutoRelogin(t *testing.T) {
	s, f := newTestPicacg(t)
	ctx := context.Background()

	// Store a stale token with valid credentials.
	if err := s.saveCred(&picacgCred{
		Username: "user@example.com",
		Password: "correct",
		Token:    "stale-token",
	}); err != nil {
		t.Fatal(err)
	}
	if !s.LoggedIn(ctx) {
		t.Fatal("loggedIn should reflect stored token")
	}

	res, err := s.Explore(ctx, "latest", 1)
	if err != nil {
		t.Fatalf("explore with stale token: %v", err)
	}
	if len(res.Comics) != 1 {
		t.Fatalf("comics = %+v", res.Comics)
	}
	if got := f.signInsCount(); got != 1 {
		t.Fatalf("expected exactly one auto re-login, got %d", got)
	}
	// The refreshed token must be persisted.
	cred := s.loadCred(ctx)
	if !strings.HasPrefix(cred.Token, "token-") {
		t.Fatalf("token not refreshed: %+v", cred)
	}
}

func TestPicacgReloginFailureClearsToken(t *testing.T) {
	s, f := newTestPicacg(t)
	ctx := context.Background()

	if err := s.saveCred(&picacgCred{
		Username: "user@example.com",
		Password: "no-longer-valid",
		Token:    "stale-token",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Explore(ctx, "latest", 1)
	if err == nil {
		t.Fatal("explore succeeded with unrecoverable credentials")
	}
	se, ok := err.(*StatusError)
	if !ok || se.Status != 401 || se.Code != CodeLoginRequired {
		t.Fatalf("err = %v", err)
	}
	// Token dropped so subsequent requests 401 without hammering sign-in.
	if s.LoggedIn(ctx) {
		t.Fatal("loggedIn should be false after failed relogin")
	}
	if got := f.signInsCount(); got != 1 {
		t.Fatalf("sign-in attempts = %d, want 1", got)
	}
	if _, err := s.Explore(ctx, "latest", 1); err == nil {
		t.Fatal("second explore succeeded")
	}
	if got := f.signInsCount(); got != 1 {
		t.Fatalf("sign-in hammered after failure: %d attempts", got)
	}
}
