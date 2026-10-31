#!/usr/bin/env python3
"""Flutter Web 冒烟：加载页面、开启 semantics、输出可点击文本、截图。"""
import asyncio, sys
from playwright.async_api import async_playwright

URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18081/"
OUT = sys.argv[2] if len(sys.argv) > 2 else "/tmp/pica_smoke.png"

async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True, args=["--no-sandbox", "--disable-gpu"])
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900}, locale="zh-CN")
        page = await ctx.new_page()
        logs, failed = [], []
        page.on("console", lambda m: logs.append(f"{m.type}: {m.text}"))
        page.on("requestfailed", lambda r: failed.append(f"{r.method} {r.url} :: {r.failure}"))
        await page.goto(URL, wait_until="domcontentloaded", timeout=60000)
        await page.wait_for_timeout(8000)
        # Flutter semantics placeholder
        try:
            await page.evaluate("document.querySelector('flt-semantics-placeholder')?.click()")
        except Exception as e:
            print("semantics click error", e)
        await page.wait_for_timeout(1500)
        texts = await page.evaluate("""
            Array.from(document.querySelectorAll('flt-semantics'))
              .map(e => ({t:(e.getAttribute('aria-label')||e.textContent||'').trim(), r:e.getBoundingClientRect()}))
              .filter(x => x.t && x.r.width>0 && x.r.height>0)
              .slice(0,80)
        """)
        print("=== semantic nodes ===")
        for x in texts:
            r=x["r"]; print(f'"{x["t"][:60]}" @ {int(r["x"])},{int(r["y"])} {int(r["width"])}x{int(r["height"])}')
        await page.screenshot(path=OUT, full_page=False)
        print("=== console (last 30) ===")
        for l in logs[-30:]: print(l)
        print("=== failed requests ===")
        for f in failed[:30]: print(f)
        await browser.close()

asyncio.run(main())
