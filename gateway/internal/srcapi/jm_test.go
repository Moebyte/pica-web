package srcapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"picagateway/internal/imgproxy"
	"picagateway/internal/outbound"
)

// testDeps builds Deps against a loopback-allowed policy, mirroring the
// server's raw-transport wiring.
func testDeps(t *testing.T) Deps {
	t.Helper()
	policy := outbound.New(nil, true, nil, nil)
	policy.AllowLoopback = true
	return Deps{
		Policy:    policy,
		Transport: outbound.NewTransport(policy, 5*time.Second),
		Signer:    imgproxy.NewSigner("test-secret"),
		TokenTTL:  time.Hour,
		Timeout:   5 * time.Second,
		Tenant:    "test",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ---- crypto ----

func TestJMDecryptVector(t *testing.T) {
	// Vector generated with the upstream scheme: AES-256-ECB, key =
	// md5hex("<time>185Hcomic3PAPP7R"), PKCS7 padding, base64.
	const vector = "2xa52Eh3BeIBRjMPY4Kv5MdTwX+zqN9/3+X3QQ9BXpkvGTJ4tBqa1DqRCDC3VBAQDMTF1v9M+dM1UeoXtB3o824hE+Q0NMkTa+/LssKTUPFKR7FxI8NKaStiJm9Mt8pNRH1Y49/P6BrbHpudhyUdQExdePSrlGCpuq2FxGjKVKQfcSfbkwENxLsnAtZSUzpV"
	plain, err := jmDecrypt(vector, "1700000000"+jmDataSecret)
	if err != nil {
		t.Fatalf("jmDecrypt: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(plain), &doc); err != nil {
		t.Fatalf("decrypted payload is not JSON: %v\n%s", err, plain)
	}
	if _, ok := doc["content"]; !ok {
		t.Fatalf("decrypted payload missing content: %s", plain)
	}
}

func TestJMDecryptRoundTrip(t *testing.T) {
	cases := []struct {
		name, plain string
	}{
		{"array", `[{"id":1,"name":"a"}]`},
		{"object", `{"total":"35","content":[]}`},
		{"nested", `{"a":{"b":[1,2,3]}}`},
		{"unicode", `{"名前":"テスト","desc":"中文描述"}`},
	}
	for _, tc := range cases {
		enc, err := jmEncrypt(tc.plain, "1700000000"+jmDataSecret)
		if err != nil {
			t.Fatalf("%s: encrypt: %v", tc.name, err)
		}
		got, err := jmDecrypt(enc, "1700000000"+jmDataSecret)
		if err != nil {
			t.Fatalf("%s: decrypt: %v", tc.name, err)
		}
		if got != tc.plain {
			t.Fatalf("%s: round trip mismatch:\n got %q\nwant %q", tc.name, got, tc.plain)
		}
	}
}

func TestJMDomainListDecrypt(t *testing.T) {
	const vector = "xQqBg5fQRC+AqvHpI6krTIdgxo5TGykP6G580PIdhIaKnG3eSdVnQ+8LZ3sFQsovx2dEgaLMcAaFXew/uA2QdQ=="
	plain, err := jmDecrypt(vector, jmDomainSecret)
	if err != nil {
		t.Fatalf("jmDecrypt: %v", err)
	}
	var doc struct {
		Server []string `json:"Server"`
	}
	if err := json.Unmarshal([]byte(plain), &doc); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, plain)
	}
	if len(doc.Server) != 2 || doc.Server[0] != "api1.example.com" || doc.Server[1] != "api2.example.com" {
		t.Fatalf("servers = %v", doc.Server)
	}
}

func TestJMHeaderChain(t *testing.T) {
	const timeSec = 1700000000
	if got := jmToken(timeSec); got != "8be524e958f97f014ddf2a570b011305" {
		t.Fatalf("jmToken = %q", got)
	}
	if got := jmTokenParam(timeSec); got != "1700000000,2.0.11" {
		t.Fatalf("jmTokenParam = %q", got)
	}
	// Recompute the md5 chain independently.
	sum := md5.Sum([]byte(fmt.Sprintf("%d%s", timeSec, jmAuthKey)))
	if got := jmToken(timeSec); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("jmToken does not match independent md5: %q", got)
	}
}

// ---- fake JM upstream ----

// fakeJM speaks the JM API protocol: encrypted domain list + encrypted
// envelope responses, with header validation.
type fakeJM struct {
	t       *testing.T
	srv     *httptest.Server
	imgSrv  *httptest.Server
	listURL string

	mu      sync.Mutex
	queries []string // "GET /path?query" of every api request
	imgReqs int
}

func newFakeJM(t *testing.T) *fakeJM {
	t.Helper()
	f := &fakeJM{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("/newsvr.txt", func(w http.ResponseWriter, r *http.Request) {
		list := map[string]any{"Server": []string{"http://" + f.srv.Listener.Addr().String()}}
		raw, _ := json.Marshal(list)
		enc, _ := jmEncrypt(string(raw), jmDomainSecret)
		_, _ = io.WriteString(w, enc)
	})
	listSrv := httptest.NewServer(mux)
	t.Cleanup(listSrv.Close)
	f.listURL = listSrv.URL + "/newsvr.txt"

	api := http.NewServeMux()
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		timeStr := f.checkHeaders(w, r)
		f.record(r.Method, r.URL.RequestURI())
		switch {
		case r.URL.Path == "/setting":
			f.writeEncrypted(w, timeStr, map[string]any{"img_host": "http://" + f.imgSrv.Listener.Addr().String()})
		case r.URL.Path == "/latest":
			f.writeEncrypted(w, timeStr, []map[string]any{
				{"id": "1487029", "name": "(C103) comic", "author": "作者A", "description": "描述",
					"category":     map[string]any{"id": "1", "title": "同人"},
					"category_sub": map[string]any{"id": "1", "title": "同人"},
					"update_at":    1793421191},
				{"id": 12345, "name": "numeric id", "author": "作者B",
					"category": map[string]any{"id": "2", "title": "單話"}},
			})
		case r.URL.Path == "/promote":
			f.writeEncrypted(w, timeStr, []map[string]any{
				{"title": "連載更新", "type": "promote", "content": []map[string]any{
					{"id": "111", "name": "home comic", "author": "A",
						"category": map[string]any{"id": "1", "title": "同人"}},
				}},
			})
		case r.URL.Path == "/search":
			if got := r.URL.Query().Get("search_query"); got != "東方" {
				f.writeEncrypted(w, timeStr, map[string]any{"content": []map[string]any{}, "total": "0"})
				return
			}
			f.writeEncrypted(w, timeStr, map[string]any{
				"content": []map[string]any{
					{"id": "999", "name": "search hit", "author": "C",
						"category": map[string]any{"title": "同人"}},
				},
				"total": "160",
			})
		case r.URL.Path == "/album":
			if r.URL.Query().Get("id") != "1487029" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.writeEncrypted(w, timeStr, map[string]any{
				"name": "(C103) comic", "author": []string{"作者A"}, "description": "描述",
				"likes": "39", "total_views": "580", "total_photos": 23,
				"addtime": 1793421178,
				"series": []map[string]any{
					{"id": "1487029", "name": "", "sort": "1"},
					{"id": "1490000", "name": "特別篇", "sort": "2"},
				},
				"tags": []string{"中文", "巨乳"}, "works": []string{"東方Project"}, "actors": []string{"霊夢"},
			})
		case r.URL.Path == "/chapter":
			if r.URL.Query().Get("id") != "1490000" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.writeEncrypted(w, timeStr, map[string]any{
				"id":     "1490000",
				"images": []string{"00001.jpg", "00002.webp"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.srv = httptest.NewServer(api)
	t.Cleanup(f.srv.Close)

	img := http.NewServeMux()
	img.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.imgReqs++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	})
	f.imgSrv = httptest.NewServer(img)
	t.Cleanup(f.imgSrv.Close)
	return f
}

func (f *fakeJM) record(method, uri string) {
	f.mu.Lock()
	f.queries = append(f.queries, method+" "+uri)
	f.mu.Unlock()
}

func (f *fakeJM) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

// checkHeaders validates the token/tokenparam md5 chain server-side and
// returns the client's time string (used to key the response encryption).
func (f *fakeJM) checkHeaders(w http.ResponseWriter, r *http.Request) string {
	token := r.Header.Get("token")
	param := r.Header.Get("tokenparam")
	timeStr, _, ok := strings.Cut(param, ",")
	if !ok {
		f.t.Errorf("tokenparam missing comma: %q", param)
		return fmt.Sprint(time.Now().Unix())
	}
	sum := md5.Sum([]byte(timeStr + jmAuthKey))
	if token != hex.EncodeToString(sum[:]) {
		f.t.Errorf("token header does not match md5(time+%q): got %q", jmAuthKey, token)
	}
	if r.Header.Get("User-Agent") != JMUserAgent {
		f.t.Errorf("user-agent = %q", r.Header.Get("User-Agent"))
	}
	if r.Header.Get("X-Requested-With") != "com.example.app" {
		f.t.Errorf("X-Requested-With missing")
	}
	if r.Header.Get("Referer") != "https://localhost/" {
		f.t.Errorf("referer = %q", r.Header.Get("Referer"))
	}
	return timeStr
}

// writeEncrypted encrypts doc with the client's request time so the response
// decrypts cleanly on the other side.
func (f *fakeJM) writeEncrypted(w http.ResponseWriter, timeStr string, doc any) {
	raw, err := json.Marshal(doc)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	enc, err := jmEncrypt(string(raw), timeStr+jmDataSecret)
	if err != nil {
		f.t.Fatalf("encrypt: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": enc})
}

func newTestJM(t *testing.T) (*jmSource, *fakeJM) {
	t.Helper()
	f := newFakeJM(t)
	s := newJM(testDeps(t))
	s.domainListURLs = []string{f.listURL}
	return s, f
}

// decryptWithRequestTime re-encrypts using the time the client sent. The fake
// needs the envelope time to equal the client's tokenparam time, so the
// handler stores it per request.
func TestJMBrowseFlow(t *testing.T) {
	s, f := newTestJM(t)
	ctx := context.Background()

	// Tabs / login semantics.
	if tabs := s.Tabs(); len(tabs) != 2 || tabs[0].ID != "home" || tabs[1].ID != "latest" {
		t.Fatalf("tabs = %+v", tabs)
	}
	if _, err := s.Login(ctx, "u", "p"); err == nil {
		t.Fatal("jm login should not be supported")
	} else if se, ok := err.(*StatusError); !ok || se.Status != 400 || se.Code != CodeNotSupported {
		t.Fatalf("jm login error = %v", err)
	}

	// latest
	res, err := s.Explore(ctx, "latest", 1)
	if err != nil {
		t.Fatalf("explore latest: %v", err)
	}
	if len(res.Comics) != 2 {
		t.Fatalf("latest comics = %d, want 2", len(res.Comics))
	}
	first := res.Comics[0]
	if first.ID != "1487029" || first.Title != "(C103) comic" || first.Author != "作者A" || first.Subtitle != "描述" {
		t.Fatalf("first brief = %+v", first)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "同人" {
		t.Fatalf("tags should dedupe category/category_sub: %v", first.Tags)
	}
	if !strings.HasPrefix(first.Cover, "/api/img/") {
		t.Fatalf("cover not signed: %q", first.Cover)
	}
	if res.Comics[1].ID != "12345" {
		t.Fatalf("numeric id not converted: %+v", res.Comics[1])
	}
	if res.Page != 1 || res.MaxPage != 2 {
		t.Fatalf("page/maxPage = %d/%d", res.Page, res.MaxPage)
	}

	// home
	home, err := s.Explore(ctx, "home", 1)
	if err != nil {
		t.Fatalf("explore home: %v", err)
	}
	if len(home.Comics) != 1 || home.Comics[0].ID != "111" || home.MaxPage != 1 {
		t.Fatalf("home = %+v", home)
	}

	// search
	sr, err := s.Search(ctx, "東方", "", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(sr.Comics) != 1 || sr.Comics[0].ID != "999" {
		t.Fatalf("search result = %+v", sr)
	}
	if sr.MaxPage != 160 { // 160 total / 1 per page
		t.Fatalf("search maxPage = %d", sr.MaxPage)
	}
	if _, err := s.Search(ctx, "x", "bogus", 1); err == nil {
		t.Fatal("invalid sort accepted")
	}

	// album detail
	comic, err := s.Comic(ctx, "1487029")
	if err != nil {
		t.Fatalf("comic: %v", err)
	}
	if comic.Title != "(C103) comic" || comic.Author != "作者A" {
		t.Fatalf("comic = %+v", comic)
	}
	if comic.Favorites != 39 || comic.UploadAt != 1793421178 || comic.PageCount != 23 {
		t.Fatalf("counts = %+v", comic.ComicBrief)
	}
	if len(comic.Chapters) != 2 || comic.Chapters[0].ID != "1487029" || comic.Chapters[1].ID != "1490000" ||
		comic.Chapters[1].Title != "特別篇" || comic.Chapters[0].Title != "第1話" {
		t.Fatalf("chapters = %+v", comic.Chapters)
	}
	for _, group := range []string{"作者", "作品", "登场人物", "标签"} {
		if len(comic.Tags[group]) == 0 {
			t.Fatalf("tag group %q missing: %+v", group, comic.Tags)
		}
	}

	// pages with jm params
	pages, err := s.Pages(ctx, "1487029", "1490000")
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

	// The requests reached the fake upstream with the expected query strings.
	seen := f.recorded()
	expectPath := func(prefix string) {
		t.Helper()
		for _, q := range seen {
			if strings.HasPrefix(q, prefix) {
				return
			}
		}
		t.Fatalf("no request matching %q; saw %v", prefix, seen)
	}
	expectPath("GET /latest?page=1")
	expectPath("GET /promote?page=0")
	expectPath("GET /search?search_query=")
	expectPath("GET /album?id=1487029")
	expectPath("GET /setting?app_img_shunt=1")
}

func TestJMSearchParams(t *testing.T) {
	s, f := newTestJM(t)
	if _, err := s.Search(context.Background(), "東方", "mv", 3); err != nil {
		t.Fatalf("search: %v", err)
	}
	seen := f.recorded()
	want := "GET /search?search_query=%E6%9D%B1%E6%96%B9&o=mv&page=3"
	found := false
	for _, q := range seen {
		if q == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("search request = %v, want %q", seen, want)
	}
}

func TestJMPagesTokenCarriesJMParams(t *testing.T) {
	s, _ := newTestJM(t)
	pages, err := s.Pages(context.Background(), "1487029", "1490000")
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	signer := s.deps.Signer
	now := time.Now().Add(time.Minute)
	for i, raw := range pages {
		token := strings.TrimPrefix(raw, "/api/img/")
		payload, err := signer.Verify(token, now)
		if err != nil {
			t.Fatalf("page %d token: %v", i, err)
		}
		if payload.JM == nil {
			t.Fatalf("page %d token has no jm params: %+v", i, payload)
		}
		if payload.JM.EpsID != "1490000" {
			t.Fatalf("page %d epsId = %q", i, payload.JM.EpsID)
		}
		if payload.JM.ScrambleID != jmDefaultScramble {
			t.Fatalf("page %d scrambleId = %q", i, payload.JM.ScrambleID)
		}
		wantBook := []string{"00001", "00002"}[i]
		if payload.JM.BookID != wantBook {
			t.Fatalf("page %d bookId = %q, want %q", i, payload.JM.BookID, wantBook)
		}
		if payload.Profile != "jm" {
			t.Fatalf("page %d profile = %q", i, payload.Profile)
		}
	}
}

func TestJMUpstreamError(t *testing.T) {
	s, _ := newTestJM(t)
	_, err := s.Comic(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown album")
	}
	se, ok := err.(*StatusError)
	if !ok || se.Status != http.StatusBadGateway {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(testDeps(t))
	if got := len(r.Sources()); got != 2 {
		t.Fatalf("sources = %d", got)
	}
	if _, ok := r.Get("jm"); !ok {
		t.Fatal("jm missing")
	}
	if _, ok := r.Get("picacg"); !ok {
		t.Fatal("picacg missing")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unknown source resolved")
	}
}
