#!/usr/bin/env python3
"""E2E for the v2 additions: server-side download submit + favorites read/write.

Runs against the vite dev server on :5180 (proxying /api to the real gateway
127.0.0.1:18081). jm detail/favorites use the real gateway; the new
POST /api/src/{src}/comic/{id}/download endpoint is used when it is live and
mocked otherwise (reported in the output). picacg detail is login-gated, so
multi-chapter / 401 flows use a mocked detail response.
"""
import json
import re
import sys
import time
import urllib.error
import urllib.request

from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:5180/v2/"
GW = "http://127.0.0.1:18081"
SHOTS = "/GLM/pica-web/webui/e2e-shots"
FOLDER = "Vue测试"

results = []


def ok(name, cond, extra=""):
    results.append((name, bool(cond), extra))
    print(("PASS " if cond else "FAIL ") + name + ((" | " + str(extra)) if extra else ""))


# ---------------------------------------------------------------------------
# gateway store helpers (GET wraps the blob as {ok,key,value}; PUT takes raw)
# ---------------------------------------------------------------------------

def store_get(key):
    with urllib.request.urlopen(f"{GW}/api/store/{key}", timeout=5) as r:
        return json.loads(json.loads(r.read().decode())["value"])


def store_put(key, blob):
    blob["updated"] = int(time.time())
    req = urllib.request.Request(
        f"{GW}/api/store/{key}",
        data=json.dumps(blob).encode(),
        method="PUT",
        headers={"Content-Type": "application/json"},
    )
    urllib.request.urlopen(req, timeout=5).read()


def cleanup_folder():
    """Remove FOLDER from the shared blob so the run is repeatable."""
    blob = store_get("favorites")
    folders = blob["data"].get("folders") or {}
    if FOLDER in folders:
        del folders[FOLDER]
        blob["data"]["folderOrder"] = [
            f for f in blob["data"].get("folderOrder", [])
            if f.get("folder_name") != FOLDER
        ]
        store_put("favorites", blob)
        print(f"[setup] removed stale folder {FOLDER!r}")
    return blob


def download_endpoint_ready():
    """OPTIONS on a POST-only route -> 405 when the endpoint exists, 404 when not."""
    req = urllib.request.Request(
        f"{GW}/api/src/jm/comic/0/download",
        data=b'{"ep":"0","epName":"probe"}',
        method="OPTIONS",
        headers={"Content-Type": "application/json"},
    )
    try:
        urllib.request.urlopen(req, timeout=5)
        return True
    except urllib.error.HTTPError as e:
        return e.code != 404


def wait_toast(page, text, timeout=8000):
    page.wait_for_function(
        "t => Array.from(document.querySelectorAll('[data-testid=toast]'))"
        ".some(e => (e.textContent || '').includes(t))",
        arg=text,
        timeout=timeout,
    )


def toast_texts(page):
    return page.locator("[data-testid=toast]").all_inner_texts()


def mock_job(comic_id, title, ep="1", ep_name="第1話", source="jm", status="running"):
    return {
        "id": f"mock-{source}-{comic_id}-{ep}",
        "title": title,
        "source": source,
        "comicId": comic_id,
        "ep": ep,
        "epName": ep_name,
        "total": 12,
        "done": 4,
        "failed": 0,
        "status": status,
        "dir": f"mock/{source}/{comic_id}/{ep}",
        "files": [],
    }


def main():
    cleanup_folder()
    endpoint_ready = download_endpoint_ready()
    print(f"[probe] POST /api/src/{{src}}/comic/{{id}}/download live: {endpoint_ready}")

    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        ctx = browser.new_context(viewport={"width": 1440, "height": 900}, locale="zh-CN")
        page = ctx.new_page()
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))

        # ---------------- 1. jm detail: new actions ----------------
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".comic-card", timeout=15000)
        page.locator(".comic-card").first.click()
        page.wait_for_selector("[data-testid=start-reading]", timeout=15000)
        page.wait_for_selector("[data-testid=fav-btn]", timeout=8000)
        title = page.locator(".detail h1").inner_text().strip()
        comic_id = page.url.rsplit("/", 1)[-1]
        ok(
            "detail/new action buttons visible",
            page.locator("[data-testid=download-btn]").is_visible()
            and page.locator("[data-testid=fav-btn]").is_visible(),
            f"title={title} id={comic_id}",
        )
        page.screenshot(path=f"{SHOTS}/14-detail-actions.png")

        # ---------------- 2. favorite into a new folder ----------------
        page.click("[data-testid=fav-btn]")
        page.wait_for_selector("[data-testid=fav-new-folder]", timeout=6000)
        page.screenshot(path=f"{SHOTS}/15-favorite-dialog.png")
        page.fill("[data-testid=fav-new-folder]", FOLDER)
        page.click("[data-testid=fav-confirm]")
        wait_toast(page, "已收藏")
        ok("favorite/toast shown", any("已收藏" in t for t in toast_texts(page)), toast_texts(page))
        page.wait_for_function(
            "document.querySelector('[data-testid=fav-btn]')?.textContent.includes('已收藏')",
            timeout=6000,
        )
        ok("favorite/button becomes 已收藏", "已收藏" in page.locator("[data-testid=fav-btn]").inner_text())

        blob = store_get("favorites")
        items = blob["data"].get("folders", {}).get(FOLDER, [])
        row = items[0] if items else None
        ok("blob/row written", row is not None, f"{len(items)} rows in {FOLDER}")
        if row:
            print("BLOB ROW:", json.dumps(row, ensure_ascii=False))
            ok("blob/target is comic id", row.get("target") == comic_id, row.get("target"))
            ok("blob/name is title", row.get("name") == title, row.get("name"))
            ok("blob/author present", bool(row.get("author")), row.get("author"))
            ok("blob/type jm=2", row.get("type") == 2, row.get("type"))
            ok("blob/tags is comma string", isinstance(row.get("tags"), str), repr(row.get("tags")))
            ok("blob/cover_path empty (v1)", row.get("cover_path") == "", repr(row.get("cover_path")))
            ok(
                "blob/time local YYYY-MM-DD HH:mm:ss",
                bool(re.fullmatch(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}", str(row.get("time")))),
                row.get("time"),
            )
            ok(
                "blob/display_order = max+1 (empty folder -> 1)",
                isinstance(row.get("display_order"), int) and row.get("display_order") == 1,
                row.get("display_order"),
            )
        order_names = [f.get("folder_name") for f in blob["data"].get("folderOrder", [])]
        ok("blob/folderOrder contains folder", FOLDER in order_names, order_names)
        ok("blob/updated is current unix seconds", abs(int(blob.get("updated", 0)) - int(time.time())) <= 10,
           blob.get("updated"))

        # ---------------- 3. toggle off / on (existing folder pick) ----------------
        page.click("[data-testid=fav-btn]")  # 已收藏 -> remove
        wait_toast(page, "已取消收藏")
        blob = store_get("favorites")
        ok(
            "favorite/toggle-off removes row",
            all(r.get("target") != comic_id for r in blob["data"].get("folders", {}).get(FOLDER, [])),
        )
        page.wait_for_function(
            "document.querySelector('[data-testid=fav-btn]')?.textContent.trim() === '收藏'",
            timeout=6000,
        )

        page.click("[data-testid=fav-btn]")  # 收藏 -> dialog with existing folder
        page.wait_for_selector("[data-testid=fav-folder-list]", timeout=6000)
        page.locator("[data-testid=fav-folder-list] label", has_text=FOLDER).click()
        page.click("[data-testid=fav-confirm]")
        wait_toast(page, "已收藏")
        blob = store_get("favorites")
        ok(
            "favorite/re-add via existing folder",
            any(r.get("target") == comic_id for r in blob["data"].get("folders", {}).get(FOLDER, [])),
        )

        # ---------------- 4. favorites page: list / delete item / delete folder ----------------
        page.goto(BASE + "favorites", wait_until="networkidle")
        page.wait_for_selector(f"[data-folder='{FOLDER}']", timeout=10000)
        fav_card_count = page.locator(f"[data-folder='{FOLDER}'] .fav-card").count()
        ok("favorites/folder + item visible", fav_card_count == 1, f"{fav_card_count} cards")
        page.screenshot(path=f"{SHOTS}/16-favorites.png")

        page.locator(f"[data-folder='{FOLDER}'] [data-testid=fav-delete]").first.click()
        page.wait_for_timeout(900)
        blob = store_get("favorites")
        ok(
            "favorites/delete item removes row from blob",
            all(r.get("target") != comic_id for r in blob["data"].get("folders", {}).get(FOLDER, [])),
        )
        ok(
            "favorites/delete item updates UI",
            page.locator(f"[data-folder='{FOLDER}'] .fav-card").count() == 0,
        )

        page.locator(f"[data-folder='{FOLDER}'] [data-testid=delete-folder]").click()
        page.wait_for_selector(".dialog", timeout=6000)
        page.locator(".dialog .actions .btn.danger").click()
        page.wait_for_timeout(900)
        blob = store_get("favorites")
        ok(
            "favorites/delete folder removes folder + folderOrder",
            FOLDER not in blob["data"].get("folders", {})
            and all(f.get("folder_name") != FOLDER for f in blob["data"].get("folderOrder", [])),
        )

        # ---------------- 5. single-chapter download submit ----------------
        page.goto(BASE + f"comic/jm/{comic_id}", wait_until="networkidle")
        page.wait_for_selector("[data-testid=download-btn]", timeout=10000)
        chapter_count = page.locator("[data-testid=chapter-list] .chapter-item").count()
        print(f"[info] jm comic chapters: {chapter_count}")

        mocked_download = False
        if not endpoint_ready:
            mocked_download = True
            job = mock_job(comic_id, title)
            page.route(
                "**/api/src/*/comic/*/download",
                lambda route: route.fulfill(
                    status=200,
                    content_type="application/json",
                    body=json.dumps({"ok": True, "job": job, "existed": False}),
                ),
            )
            page.route(
                "**/api/downloads/jobs*",
                lambda route: route.fulfill(
                    status=200,
                    content_type="application/json",
                    body=json.dumps({"ok": True, "jobs": [job]}),
                )
                if route.request.method == "GET"
                else route.continue_(),
            )

        page.click("[data-testid=download-btn]")
        wait_toast(page, "已加入下载队列", timeout=10000)
        ok("download/single-chapter toast", any("已加入下载队列" in t for t in toast_texts(page)),
           toast_texts(page))
        ok("download/toast has 查看下载 link",
           page.locator("[data-testid=toast-action]", has_text="查看下载").count() >= 1)

        page.goto(BASE + "downloads", wait_until="networkidle")
        page.wait_for_selector(".job", timeout=15000)
        page.wait_for_timeout(1200)
        job_count = page.locator(".job").count()
        statuses = [page.locator(".job").nth(i).get_attribute("data-status") for i in range(job_count)]
        ok(
            "downloads/job appears after submit",
            job_count >= 1,
            f"{job_count} jobs, statuses={statuses}, mocked={mocked_download}",
        )
        ok(
            "downloads/status queued|running|done|partial",
            any(s in ("queued", "running", "pending", "done", "partial") for s in statuses),
            statuses,
        )
        page.screenshot(path=f"{SHOTS}/18-download-progress.png")

        # ---------------- 6. multi-chapter dialog + serial submit (mock picacg) ----------------
        multi = {
            "id": "mockmulti",
            "title": "多章节测试漫画",
            "subtitle": "",
            "author": "tester",
            "cover": "",
            "pageCount": 9,
            "favorites": 1,
            "uploadDate": int(time.time()),
            "tags": {"标签": ["测试"]},
            "chapters": [
                {"id": "m1", "title": "第1話", "order": 1},
                {"id": "m2", "title": "第2話", "order": 2},
                {"id": "m3", "title": "第3話", "order": 3},
            ],
            "thumbs": [],
        }
        page.route(
            "**/api/src/picacg/comic/mockmulti",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps({"ok": True, "comic": multi}),
            ),
        )
        posted = []

        def handle_multi_submit(route):
            url = route.request.url
            if route.request.method != "POST":
                return route.continue_()
            if "/comic/needlogin/download" in url:
                return route.fulfill(
                    status=401,
                    content_type="application/json",
                    body=json.dumps({"ok": False, "error": "picacg login required"}),
                )
            body = json.loads(route.request.post_data or "{}")
            posted.append(body.get("ep"))
            time.sleep(0.35)  # keep the progress bar on screen long enough to shoot
            return route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps(
                    {"ok": True, "job": mock_job("mockmulti", multi["title"], body.get("ep", "")), "existed": False}
                ),
            )

        page.route("**/api/src/picacg/comic/*/download", handle_multi_submit)

        page.goto(BASE + "comic/picacg/mockmulti", wait_until="networkidle")
        page.wait_for_selector("[data-testid=download-btn]", timeout=10000)
        page.click("[data-testid=download-btn]")
        page.wait_for_selector("[data-testid=download-chapter-list]", timeout=6000)
        ok(
            "download/multi-chapter dialog lists chapters + all",
            page.locator("[data-testid=download-chapter-list] .chapter-option").count() == 4,
            page.locator("[data-testid=download-chapter-list] .chapter-option").count(),
        )
        page.screenshot(path=f"{SHOTS}/17-download-dialog.png")
        page.click("[data-testid=download-all]")
        page.click("[data-testid=download-confirm]")
        page.wait_for_selector("[data-testid=download-progress]", timeout=3000)
        page.screenshot(path=f"{SHOTS}/18-download-progress-bar.png")
        wait_toast(page, "已加入下载队列（3 章）", timeout=12000)
        ok("download/all-chapters toast counts 3", any("已加入下载队列（3 章）" in t for t in toast_texts(page)),
           toast_texts(page))
        ok("download/serial one POST per chapter", posted == ["m1", "m2", "m3"], posted)

        # ---------------- 7. picacg not logged in -> 401 login toast ----------------
        single = dict(multi, id="needlogin", title="需要登录的漫画",
                      chapters=[{"id": "n1", "title": "第1話", "order": 1}])
        page.route(
            "**/api/src/picacg/comic/needlogin",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps({"ok": True, "comic": single}),
            ),
        )
        page.goto(BASE + "comic/picacg/needlogin", wait_until="networkidle")
        page.wait_for_selector("[data-testid=download-btn]", timeout=10000)
        page.click("[data-testid=download-btn]")
        wait_toast(page, "请先登录", timeout=8000)
        ok("download/401 prompts login", any("请先登录" in t for t in toast_texts(page)), toast_texts(page))
        login_link = page.locator("[data-testid=toast-action]", has_text="去登录")
        href = login_link.first.get_attribute("href") if login_link.count() else None
        ok("download/401 login link to /v2/login/picacg", href == "/v2/login/picacg", href)
        page.screenshot(path=f"{SHOTS}/19-login-toast.png")

        # ---------------- 8. console errors ----------------
        ok("no page JS errors", len(errors) == 0, "; ".join(errors[:3]))
        browser.close()

    # final cleanup (idempotent, in case an assertion aborted mid-flow)
    cleanup_folder()

    failed = [r for r in results if not r[1]]
    print(f"\n==== {len(results) - len(failed)}/{len(results)} passed ====")
    if failed:
        print("FAILED:", [f[0] for f in failed])
        sys.exit(1)


if __name__ == "__main__":
    main()
