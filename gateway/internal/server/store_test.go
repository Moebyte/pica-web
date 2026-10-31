package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"picagateway/internal/config"
)

func newStoreEnv(t *testing.T) *testEnv {
	return newTestEnv(t, func(c *config.Config) { c.Password = "s3cret" })
}

func TestStoreRoundTrip(t *testing.T) {
	e := newStoreEnv(t)
	login := func() {
		req, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/api/auth/login",
			strings.NewReader("password=s3cret"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	login()

	payload := `{"sources":{"jm":{"account":["u","p"]}},"cookies":[]}`
	req, _ := http.NewRequest(http.MethodPut, e.gw.URL+"/api/store/account", strings.NewReader(payload))
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d body=%s", resp.StatusCode, body)
	}

	resp, err = e.client.Get(e.gw.URL + "/api/store/account")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK    bool   `json:"ok"`
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if !got.OK || got.Key != "account" || got.Value != payload {
		t.Fatalf("get = %+v", got)
	}

	// 落库必须是密文：在 db 文件里不应出现明文。
	var dbPath string
	filepath.Walk(e.cfg.DataDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, "vault.db") {
			dbPath = p
		}
		return nil
	})
	if dbPath == "" {
		t.Fatal("vault.db not found")
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"account":["u","p"]`)) {
		t.Fatal("plaintext account payload found in vault.db")
	}
}

func TestStoreValidation(t *testing.T) {
	e := newStoreEnv(t)

	// 未登录 401
	resp, err := e.client.Get(e.gw.URL + "/api/store/account")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated get = %d", resp.StatusCode)
	}

	// 登录后走同一 cookie jar
	req, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/api/auth/login", strings.NewReader("password=s3cret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := e.client.Do(req); err != nil {
		t.Fatal(err)
	}

	// 非法 key
	resp, _ = e.client.Get(e.gw.URL + "/api/store/BAD_KEY")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad key = %d", resp.StatusCode)
	}

	// 不存在的 key
	resp, _ = e.client.Get(e.gw.URL + "/api/store/missing")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing key = %d", resp.StatusCode)
	}

	// 超限（7 MiB）
	big := strings.Repeat("x", storeValueLimit+1)
	req, _ = http.NewRequest(http.MethodPut, e.gw.URL+"/api/store/account", strings.NewReader(big))
	resp, err = e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize = %d", resp.StatusCode)
	}

	// 删除后再取 404
	req, _ = http.NewRequest(http.MethodPut, e.gw.URL+"/api/store/account", strings.NewReader("v"))
	if _, err := e.client.Do(req); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodDelete, e.gw.URL+"/api/store/account", nil)
	if resp, err = e.client.Do(req); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = e.client.Get(e.gw.URL + "/api/store/account")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete = %d", resp.StatusCode)
	}
}

func TestStoreHandlerUnit(t *testing.T) {
	// 直接打 handler，避免起完整 server（覆盖 key 正则）
	ok := storeKeyPattern.MatchString("account")
	if !ok {
		t.Fatal("account key should be valid")
	}
	for _, bad := range []string{"Account", "a b", "/x", "", strings.Repeat("a", 33)} {
		if storeKeyPattern.MatchString(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
	_ = httptest.NewRecorder()
}
