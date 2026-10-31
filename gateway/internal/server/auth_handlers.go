package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"picagateway/internal/auth"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.devModeBlocked(r) {
		writeError(w, http.StatusForbidden, "dev_mode_loopback_only",
			"no password is configured; /api is restricted to loopback clients")
		return
	}
	if s.cfg.Password == "" {
		writeError(w, http.StatusBadRequest, "dev_mode", "no password is configured; login is disabled")
		return
	}

	password, err := readLoginPassword(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if !auth.CheckPassword(s.cfg.Password, password) {
		writeError(w, http.StatusUnauthorized, "invalid_password", "wrong password")
		return
	}

	now := time.Now()
	token, exp := s.sessions.Issue(now)
	http.SetCookie(w, s.sessions.Cookie(token, s.cookieSecure(r), now))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"expiresAt": exp.UTC().Format(time.RFC3339),
	})
}

var errLoginBodyTooLarge = errors.New("request body too large")

func readLoginPassword(r *http.Request) (string, error) {
	defer r.Body.Close()
	const limit = 64 << 10
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return "", err
	}
	if len(body) > limit {
		return "", errLoginBodyTooLarge
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return "", err
		}
		return values.Get("password"), nil
	}
	var payload struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// Accept a bare password body (curl -d 'password').
		if len(body) > 0 && !strings.ContainsAny(string(body), "{}\"") {
			return strings.TrimSpace(string(body)), nil
		}
		return "", errors.New("body must be {\"password\":\"...\"}")
	}
	return payload.Password, nil
}

// authenticated reports whether the request carries a valid gateway session.
// Dev mode (no password) is always authenticated; access control is handled by
// requireAuth's loopback restriction instead.
func (s *Server) authenticated(r *http.Request) bool {
	if s.cfg.Password == "" {
		return true
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return false
	}
	_, ok := s.sessions.Validate(cookie.Value, time.Now())
	return ok
}

// handleLoginPage serves a minimal password form. The form posts to the JSON
// login endpoint with fetch, then navigates to the sanitized `next` path.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Password == "" || s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, loginPageHTML)
}

const loginPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>PicaComic 登录</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
         background: #f1f2f6; color: #1f2430; }
  main { width: min(92vw, 360px); padding: 32px 28px; border-radius: 18px;
         background: #fff; box-shadow: 0 10px 40px rgba(20, 30, 60, .12); }
  h1 { margin: 0 0 4px; font-size: 24px; letter-spacing: .5px; }
  h1 span { color: #ef5b6b; }
  p.hint { margin: 0 0 24px; font-size: 13px; color: #667085; }
  label { display: block; font-size: 13px; color: #475467; margin-bottom: 6px; }
  input { width: 100%; padding: 11px 12px; border: 1px solid #d0d5dd; border-radius: 10px;
          font-size: 15px; outline: none; }
  input:focus { border-color: #5b6cff; box-shadow: 0 0 0 3px rgba(91,108,255,.15); }
  button { width: 100%; margin-top: 16px; padding: 11px 12px; border: 0; border-radius: 10px;
           background: #5b6cff; color: #fff; font-size: 15px; font-weight: 600; cursor: pointer; }
  button:disabled { opacity: .6; cursor: default; }
  #err { min-height: 20px; margin: 12px 0 0; font-size: 13px; color: #d92d20; }
</style>
</head>
<body>
<main>
  <h1><span>pica</span> web</h1>
  <p class="hint">输入网关密码后继续</p>
  <form id="f">
    <label for="password">密码</label>
    <input id="password" name="password" type="password" autocomplete="current-password" autofocus required>
    <button id="submit" type="submit">登录</button>
    <p id="err" role="alert"></p>
  </form>
</main>
<script>
  let next = new URLSearchParams(location.search).get('next') || '/';
  if (!next.startsWith('/') || next.startsWith('//')) next = '/';
  const form = document.getElementById('f');
  const err = document.getElementById('err');
  const submit = document.getElementById('submit');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    err.textContent = '';
    submit.disabled = true;
    try {
      const body = new URLSearchParams(new FormData(form));
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body
      });
      if (res.ok) {
        location.href = next;
      } else if (res.status === 401) {
        err.textContent = '密码错误';
      } else {
        err.textContent = '登录失败（' + res.status + '）';
      }
    } catch (e) {
      err.textContent = '网络错误，请重试';
    } finally {
      submit.disabled = false;
    }
  });
</script>
</body>
</html>
`

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.devModeBlocked(r) {
		writeError(w, http.StatusForbidden, "dev_mode_loopback_only",
			"no password is configured; /api is restricted to loopback clients")
		return
	}
	http.SetCookie(w, s.sessions.ClearCookie(s.cookieSecure(r)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if s.devModeBlocked(r) {
		writeError(w, http.StatusForbidden, "dev_mode_loopback_only",
			"no password is configured; /api is restricted to loopback clients")
		return
	}
	base := map[string]any{
		"ok":      true,
		"devMode": s.cfg.Password == "",
		"tenant":  s.tenant,
	}
	if s.cfg.Password == "" {
		base["authenticated"] = true
		writeJSON(w, http.StatusOK, base)
		return
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		base["authenticated"] = false
		writeJSON(w, http.StatusOK, base)
		return
	}
	exp, ok := s.sessions.Validate(cookie.Value, time.Now())
	if !ok {
		base["authenticated"] = false
		writeJSON(w, http.StatusOK, base)
		return
	}
	base["authenticated"] = true
	base["expiresAt"] = exp.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, base)
}
