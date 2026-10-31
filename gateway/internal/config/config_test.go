package config

import (
	"path/filepath"
	"testing"
)

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load("../../config.example.json")
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if cfg.Listen != "127.0.0.1:18081" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.Password != "change-me" {
		t.Errorf("password = %q", cfg.Password)
	}
	if len(cfg.AllowHosts) != 56 {
		t.Errorf("allowHosts = %v", cfg.AllowHosts)
	}
	if cfg.WebDir2 != "../webui/dist" {
		t.Errorf("webDir2 = %q", cfg.WebDir2)
	}
	if len(cfg.AllowHosts) >= 2 && (cfg.AllowHosts[0] != "nhentai.net" || cfg.AllowHosts[1] != "*.nhentai.net") {
		t.Errorf("allowHosts order = %v", cfg.AllowHosts[:2])
	}
	for _, host := range []string{"e-hentai.org", "*.exhentai.org", "*.ehgt.org", "hitomi.la", "*.picacomic.com", "*.18comic.vip", "*.18comic.org", "*.wnacg.org", "bytepluses.com", "*.jmapiproxy1.cc", "*.jmapiproxy3.cc", "cdnhjk.net", "*.cdnutc.cc", "*.cdngwc.club", "*.cdnutc.me", "*.jmdanjonproxy.vip", "cdntwice.org", "*.cdnsha.org", "*.cdnntr.cc", "qy0.ru", "*.gold-usergeneratedcontent.net", "*.hath.network"} {
		found := false
		for _, h := range cfg.AllowHosts {
			if h == host {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allowHosts missing %q", host)
		}
	}
	if !cfg.MetaSetCookies {
		t.Error("metaSetCookies should be true in the MVP template")
	}
	if cfg.UserAgent != "" {
		t.Errorf("userAgent = %q, want empty so sources use their own UA", cfg.UserAgent)
	}
	if _, ok := cfg.HeaderProfiles["ehentai"]; !ok {
		t.Errorf("headerProfiles missing ehentai: %v", cfg.HeaderProfiles)
	}
	if filepath.Clean(cfg.CacheDir) != filepath.Clean("./data/imgcache") {
		t.Errorf("cacheDir = %q", cfg.CacheDir)
	}
	if filepath.Clean(cfg.DownloadsDir) != filepath.Clean("./data/downloads") {
		t.Errorf("downloadsDir = %q", cfg.DownloadsDir)
	}
	if cfg.BodyLimit() != 8<<20 || cfg.RespLimit() != 200<<20 {
		t.Errorf("limits = %d/%d", cfg.BodyLimit(), cfg.RespLimit())
	}
}

func TestDownloadsDirDefaultsAndEnv(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(cfg.DownloadsDir) != filepath.Join(cfg.DataDir, "downloads") {
		t.Errorf("default downloadsDir = %q, want %q", cfg.DownloadsDir, filepath.Join(cfg.DataDir, "downloads"))
	}

	t.Setenv("PICA_DATA_DIR", "/tmp/pica-data")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadsDir != filepath.Join("/tmp/pica-data", "downloads") {
		t.Errorf("downloadsDir follows dataDir: %q", cfg.DownloadsDir)
	}

	t.Setenv("PICA_DOWNLOADS_DIR", "/srv/downloads")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadsDir != "/srv/downloads" {
		t.Errorf("PICA_DOWNLOADS_DIR override = %q", cfg.DownloadsDir)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("PICA_LISTEN", "127.0.0.1:19999")
	t.Setenv("PICA_ALLOW_HOSTS", "a.example,*.b.example")
	t.Setenv("PICA_RATE_RPS", "5")
	t.Setenv("PICA_RATE_BURST", "10")
	t.Setenv("PICA_ALLOW_ALL", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:19999" || !cfg.AllowAll {
		t.Errorf("listen/allowAll = %q/%v", cfg.Listen, cfg.AllowAll)
	}
	if len(cfg.AllowHosts) != 2 || cfg.AllowHosts[1] != "*.b.example" {
		t.Errorf("allowHosts = %v", cfg.AllowHosts)
	}
	if cfg.RateLimit.RPS != 5 || cfg.RateLimit.Burst != 10 {
		t.Errorf("rateLimit = %+v", cfg.RateLimit)
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	t.Setenv("PICA_ALLOW_HOSTS", "*")
	if _, err := Load(""); err == nil {
		t.Fatal("bare wildcard should be rejected")
	}
}
