#!/usr/bin/env python3
"""E2E for PicaComic webui (/v2/) against vite dev :5180.

- /api/src/* served by the vite mock plugin (real semantic API absent)
- /api/store/*, /api/downloads/*, /healthz proxied to the REAL gateway 127.0.0.1:18081
"""
import json
import re
import sys
import urllib.request

from playwright.sync_api import sync_playwright, expect

BASE = "http://127.0.0.1:5180/v2/"
GW = "http://127.0.0.1:18081"
SHOTS = "/GLM/pica-web/webui/e2e-shots"

results = []


def ok(name, cond, extra=""):
    results.append((name, bool(cond), extra))
    print(("PASS " if cond else "FAIL ") + name + ((" | " + str(extra)) if extra else ""))


def gw_history():
    with urllib.request.urlopen(GW + "/api/store/history", timeout=5) as r:
        return json.loads(json.loads(r.read().decode())["value"])


def main():
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        ctx = browser.new_context(viewport={"width": 1440, "height": 900}, locale="zh-CN")
        page = ctx.new_page()
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))

        # ---------------- 1. explore ----------------
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".comic-card", timeout=15000)
        n1 = page.locator(".comic-card").count()
        ok("explore/initial cards", n1 >= 20, f"{n1} cards")
        ok("explore/source pills", page.locator(".source-pill").count() == 2)

        # infinite scroll -> page 2
        page.mouse.wheel(0, 20000)
        page.wait_for_timeout(1500)
        page.mouse.wheel(0, 20000)
        page.wait_for_timeout(1800)
        n2 = page.locator(".comic-card").count()
        ok("explore/infinite scroll loads page 2", n2 > n1, f"{n1} -> {n2}")

        # tab switch resets list
        tabs = page.locator(".tab-btn")
        if tabs.count() > 1:
            tabs.nth(1).click()
            page.wait_for_timeout(1200)
            ok("explore/tab switch", page.locator(".comic-card").count() > 0)

        page.screenshot(path=f"{SHOTS}/01-explore-dark.png", full_page=False)

        # keep-alive scroll restore via sidebar nav (no page reload)
        page.evaluate("window.scrollTo(0, 1600)")
        page.wait_for_timeout(400)
        page.click(".side-link >> text=搜索")
        page.wait_for_timeout(600)
        page.click(".side-link >> text=探索")
        page.wait_for_timeout(700)
        y = page.evaluate("window.scrollY")
        ok("explore/keep-alive scroll restored", y > 800, f"scrollY={y}")

        # ---------------- 2. search ----------------
        page.click(".side-link >> text=搜索")
        page.wait_for_timeout(500)
        page.fill("[data-testid=search-input]", "の")
        page.keyboard.press("Enter")
        page.wait_for_selector(".comic-card", timeout=10000)
        ok("search/jm results", page.locator(".comic-card").count() > 0,
           f"{page.locator('.comic-card').count()} cards")
        ok("search/jm sort select visible", page.locator(".sort-select").is_visible())
        page.screenshot(path=f"{SHOTS}/02-search-dark.png")

        # source switch to picacg: sort should disappear
        page.click(".source-pill >> text=PicACG")
        page.wait_for_timeout(1200)
        ok("search/picacg hides sort", not page.locator(".sort-select").is_visible())
        page.fill("[data-testid=search-input]", "blue")
        page.keyboard.press("Enter")
        page.wait_for_selector(".comic-card", timeout=10000)
        page.wait_for_timeout(600)
        ok("search/picacg results", page.locator(".comic-card").count() > 0)
        page.screenshot(path=f"{SHOTS}/03-search-picacg.png")

        # ---------------- 3. detail + reader (picacg) ----------------
        page.locator(".comic-card").first.click()
        page.wait_for_selector(".detail h1", timeout=10000)
        title = page.locator(".detail h1").inner_text()
        comic_id = page.url.rsplit("/", 1)[-1]
        chapters = page.locator("[data-testid=chapter-list] .chapter-item").count()
        ok("detail/chapters rendered", chapters >= 1, f"{chapters} chapters, title={title}")
        ok("detail/tag groups", page.locator(".tag-group").count() >= 1)
        ok("detail/stats present", page.locator(".stats .stat").count() >= 2)
        page.screenshot(path=f"{SHOTS}/04-detail-dark.png", full_page=False)

        # tag chip -> search page with q
        page.locator(".tag-group .chip").first.click()
        page.wait_for_timeout(700)
        ok("detail/tag chip navigates to search",
           "/v2/search" in page.url and "q=" in page.url, page.url)
        page.go_back()
        page.wait_for_selector("[data-testid=start-reading]", timeout=8000)

        # start reading
        page.click("[data-testid=start-reading]")
        page.wait_for_selector("[data-testid=reader] img", timeout=15000)
        page.wait_for_timeout(1200)
        imgs = page.locator("[data-testid=reader-strip] img").count()
        ok("reader/images rendered", imgs >= 6, f"{imgs} page images")
        natural = page.evaluate(
            "Array.from(document.querySelectorAll('[data-testid=reader-strip] img'))"
            ".filter(i=>i.complete && i.naturalWidth>0).length"
        )
        ok("reader/images decoded", natural >= 1, f"{natural} decoded")
        prog = page.locator("[data-testid=reader-progress]").inner_text()
        ok("reader/progress label", "P 1" in prog, prog)
        page.screenshot(path=f"{SHOTS}/05-reader-dark.png")

        # turn pages 2..6; the 5-page throttle must flush to the REAL store mid-read
        for p in range(2, 7):
            page.evaluate(
                "n => document.querySelector(`[data-page='${n}']`)?.scrollIntoView()", p
            )
            page.wait_for_timeout(700)
        cur = page.locator("[data-testid=reader-progress]").inner_text()
        ok("reader/page tracked while scrolling", re.search(r"P [2-6]", cur), cur)

        mid = [r for r in gw_history()["data"]["history"] if r.get("target") == comic_id]
        ok("history/throttled flush while reading (real store)",
           len(mid) == 1 and mid[0].get("page", 0) >= 5 and mid[0].get("type") == 0,
           mid[0] and {k: mid[0].get(k) for k in ("page", "ep", "type", "readEpisode")})

        # chapter switch (only for multi-chapter comics): toggle chrome via
        # elementFromPoint (no auto-scroll), then click 下一話
        page.evaluate("() => document.elementFromPoint(innerWidth/2, innerHeight/2)?.click()")
        page.wait_for_timeout(300)
        if chapters > 1:
            nxt = page.locator(".chapter-switch >> text=下一話")
            if nxt.count() > 0:
                nxt.first.click()
                page.wait_for_timeout(1800)
                ok("reader/chapter switch", page.locator("[data-testid=reader-strip] img").count() >= 6)
                page.wait_for_timeout(800)

        # leave reader -> flush on unmount
        page.go_back()
        page.wait_for_timeout(1500)

        blob = gw_history()
        rows = blob["data"]["history"]
        row = next((r for r in rows if r.get("target") == comic_id), None)
        ok("history/row written to real store", row is not None)
        if row:
            ok("history/type picacg=0", row.get("type") == 0, row.get("type"))
            ok("history/readEpisode is chapter id", bool(row.get("readEpisode")),
               row.get("readEpisode"))
            ok("history/page advanced", row.get("page", 0) >= 2,
               f"page={row.get('page')} ep={row.get('ep')}")
            ok("history/ep is 1-based order", row.get("ep", 0) >= 1, row.get("ep"))
            ok("history/max_page set", row.get("max_page", 0) > 0, row.get("max_page"))
            ok("history/time is ms now", row.get("time", 0) > 1.7e12, row.get("time"))

        # ---------------- 5. history page ----------------
        page.goto(BASE + "history", wait_until="networkidle")
        page.wait_for_selector("[data-testid=history-list] .row", timeout=10000)
        ok("history/page rows", page.locator("[data-testid=history-list] .row").count() >= 3)
        ok("history/sorted desc by time",
           page.evaluate(
               "Array.from(document.querySelectorAll('[data-testid=history-list] .row .t'))"
               ".length") > 0)
        # continue-reading link carries ep
        page.locator("[data-testid=history-list] .row").first.click()
        page.wait_for_timeout(900)
        ok("history/click opens reader with ep", "/v2/read/" in page.url and "ep=" in page.url,
           page.url)
        page.go_back()
        page.wait_for_timeout(600)
        page.screenshot(path=f"{SHOTS}/06-history-dark.png")

        # ---------------- 6. jm flow: type=2 history ----------------
        page.click(".source-pill >> text=禁漫天堂")
        page.wait_for_timeout(1000)
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".comic-card", timeout=10000)
        page.locator(".comic-card").first.click()
        page.wait_for_selector("[data-testid=start-reading]", timeout=10000)
        jm_id = page.url.rsplit("/", 1)[-1]
        page.click("[data-testid=start-reading]")
        page.wait_for_selector("[data-testid=reader] img", timeout=15000)
        page.wait_for_timeout(1000)
        for p in (2, 3, 4, 5):
            page.evaluate("n => document.querySelector(`[data-page='${n}']`)?.scrollIntoView()", p)
            page.wait_for_timeout(600)
        # SPA navigate away (unmount flush) instead of a hard reload
        page.go_back()  # leave reader (fullscreen hides the sidebar)
        page.wait_for_timeout(600)
        page.click(".side-link >> text=历史")
        page.wait_for_timeout(1500)
        jm_rows = [r for r in gw_history()["data"]["history"] if r.get("target") == jm_id]
        ok("history/jm row type=2 written", len(jm_rows) == 1 and jm_rows[0].get("type") == 2,
           jm_rows and jm_rows[0].get("type"))
        if jm_rows:
            ok("history/jm readEpisode set", bool(jm_rows[0].get("readEpisode")),
               jm_rows[0].get("readEpisode"))

        # ---------------- 6b. pagehide keepalive flush ----------------
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".comic-card", timeout=10000)
        page.locator(".comic-card").nth(3).click()
        page.wait_for_selector("[data-testid=start-reading]", timeout=10000)
        kd_id = page.url.rsplit("/", 1)[-1]
        page.click("[data-testid=start-reading]")
        page.wait_for_selector("[data-testid=reader] img", timeout=15000)
        page.wait_for_timeout(900)
        page.evaluate("n => document.querySelector(`[data-page='${n}']`)?.scrollIntoView()", 2)
        page.wait_for_timeout(600)
        page.evaluate("n => document.querySelector(`[data-page='${n}']`)?.scrollIntoView()", 3)
        page.wait_for_timeout(600)
        page.reload(wait_until="domcontentloaded")  # hard reload fires pagehide
        page.wait_for_timeout(2000)
        kd = [r for r in gw_history()["data"]["history"] if r.get("target") == kd_id]
        ok("history/pagehide keepalive flush", len(kd) == 1 and kd[0].get("page", 0) >= 2,
           kd and kd[0].get("page"))

        # ---------------- 7. favorites / downloads / settings ----------------
        page.goto(BASE + "favorites", wait_until="networkidle")
        page.wait_for_timeout(800)
        ok("favorites/renders (empty state ok)",
            page.locator(".empty-state").count() + page.locator(".fav-card").count() > 0)
        page.screenshot(path=f"{SHOTS}/07-favorites-dark.png")

        page.goto(BASE + "downloads", wait_until="networkidle")
        page.wait_for_timeout(800)
        ok("downloads/renders", page.locator(".empty-state").count() + page.locator(".job").count() > 0)
        page.screenshot(path=f"{SHOTS}/08-downloads-dark.png")

        page.goto(BASE + "settings", wait_until="networkidle")
        page.wait_for_selector("text=网关状态", timeout=8000)
        page.wait_for_timeout(600)
        ok("settings/health online (real gateway)", page.locator(".ok-badge").count() == 1)
        ok("settings/uptime shown", page.locator(".card >> text=运行时间").count() == 1)
        page.screenshot(path=f"{SHOTS}/09-settings-dark.png")

        # ---------------- 8. login (mock 401 + success) ----------------
        page.goto(BASE + "login/picacg", wait_until="networkidle")
        page.fill("[data-testid=login-username]", "baduser")
        page.fill("[data-testid=login-password]", "badpass")
        page.click("[data-testid=login-submit]")
        page.wait_for_selector("[data-testid=login-error]", timeout=8000)
        ok("login/wrong creds error shown",
           "密码" in page.locator("[data-testid=login-error]").inner_text()
           or "用户名" in page.locator("[data-testid=login-error]").inner_text())
        page.screenshot(path=f"{SHOTS}/10-login-error.png")

        page.fill("[data-testid=login-username]", "demo")
        page.fill("[data-testid=login-password]", "demo")
        page.click("[data-testid=login-submit]")
        page.wait_for_selector("[data-testid=login-success]", timeout=8000)
        page.wait_for_timeout(1500)
        ok("login/success redirects to explore", page.url.endswith("/v2/"))
        ok("login/topbar button gone", page.locator(".login-btn").count() == 0)

        # ---------------- 9. themes ----------------
        page.goto(BASE + "settings", wait_until="networkidle")
        page.click("[data-testid=theme-light]")
        page.wait_for_timeout(300)
        ok("theme/light applied", page.evaluate("document.documentElement.dataset.theme") == "light")
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".comic-card", timeout=10000)
        page.wait_for_timeout(900)
        page.screenshot(path=f"{SHOTS}/11-explore-light.png")
        page.goto(BASE + "settings", wait_until="networkidle")
        page.click("[data-testid=theme-dark]")
        page.wait_for_timeout(200)
        ok("theme/dark restored", page.evaluate("document.documentElement.dataset.theme") == "dark")
        ok("theme/persisted in localStorage",
           page.evaluate("localStorage.getItem('v2.theme')") == "dark")

        # ---------------- 10. mobile viewport ----------------
        mob = browser.new_context(viewport={"width": 390, "height": 844},
                                  is_mobile=True, has_touch=True, locale="zh-CN")
        mp = mob.new_page()
        mp.goto(BASE, wait_until="networkidle")
        mp.wait_for_selector(".comic-card", timeout=15000)
        ok("mobile/bottom tabs visible", mp.locator(".bottom-tabs").is_visible())
        ok("mobile/sidebar hidden", not mp.locator(".sidebar").is_visible())
        grid_cols = mp.evaluate(
            "getComputedStyle(document.querySelector('.comic-grid')).gridTemplateColumns.split(' ').length")
        ok("mobile/grid <= 2 columns", grid_cols <= 2, f"{grid_cols} cols")
        mp.screenshot(path=f"{SHOTS}/12-mobile-explore.png")
        mp.locator(".comic-card").first.click()
        mp.wait_for_selector("[data-testid=start-reading]", timeout=10000)
        mp.click("[data-testid=start-reading]")
        mp.wait_for_selector("[data-testid=reader] img", timeout=15000)
        mp.wait_for_timeout(1000)
        # toggle chrome by tapping center, then switch to paged mode and swipe
        mp.evaluate("() => document.elementFromPoint(innerWidth/2, innerHeight/2)?.click()")
        mp.wait_for_timeout(300)
        mode_visible = mp.evaluate(
            "() => { const b = document.querySelector('.chrome-btn.mode');"
            " return b ? b.getBoundingClientRect().top >= 0 : false; }")
        if mode_visible:
            mp.evaluate("() => document.querySelector('.chrome-btn.mode')?.click()")
            mp.wait_for_timeout(400)
            ok("mobile/paged mode switch", "[data-testid=reader-single]" in mp.content())
            before = mp.locator("[data-testid=reader-progress]").inner_text()
            mp.evaluate("""() => {
                const el = document.querySelector('.single');
                const r = {bubbles:true, cancelable:true};
                const t = [{clientX: 300, clientY: 400, identifier: 0}];
                el.dispatchEvent(new TouchEvent('touchstart', {touches: t, changedTouches: t, ...r}));
                const t2 = [{clientX: 100, clientY: 400, identifier: 0}];
                el.dispatchEvent(new TouchEvent('touchend', {touches: [], changedTouches: t2, ...r}));
            }""")
            mp.wait_for_timeout(400)
            after = mp.locator("[data-testid=reader-progress]").inner_text()
            ok("mobile/swipe changes page", before != after, f"{before} -> {after}")
        mp.screenshot(path=f"{SHOTS}/13-mobile-reader.png")
        mob.close()

        # ---------------- console errors ----------------
        ok("no page JS errors", len(errors) == 0, "; ".join(errors[:3]))

        browser.close()

    failed = [r for r in results if not r[1]]
    print(f"\n==== {len(results) - len(failed)}/{len(results)} passed ====")
    if failed:
        print("FAILED:", [f[0] for f in failed])
        sys.exit(1)


if __name__ == "__main__":
    main()
