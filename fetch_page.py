"""Fetch one web page with Scrapling and print it as Markdown.

Run by goharness's fetch_page tool: python - <url> <mode>   (script on stdin)
mode: auto (HTTP, then a headless browser if the page looks thin), http, browser.
"""
import sys

note = ""
url, mode = sys.argv[1], sys.argv[2]

try:
    from scrapling.fetchers import DynamicFetcher, Fetcher
    from scrapling.core.shell import Convertor
except ImportError as e:
    print(f"scrapling is not usable from this Python: {e}", file=sys.stderr)
    sys.exit(2)


def to_markdown(page):
    # main_content_only drops scripts/styles and hidden text used for prompt injection.
    return "".join(Convertor._extract_content(page, "markdown", main_content_only=True)).strip()


def fetch(kind):
    if kind == "http":
        page = Fetcher.get(url, timeout=30, stealthy_headers=True)
    else:
        page = DynamicFetcher.fetch(url, headless=True, network_idle=True, timeout=45000)
    if page.status >= 400:
        raise RuntimeError(f"HTTP {page.status}")
    return page, to_markdown(page)


try:
    if mode == "auto":
        try:
            page, body = fetch("http")
        except Exception as http_err:
            page, body, http_err = None, "", http_err
        if page is None or len(body) < 1000:
            # Thin result: refused, or a JavaScript-rendered shell. Try the browser
            # and keep whichever came back with more text.
            try:
                b_page, b_body = fetch("browser")
                if page is None or len(b_body) > len(body):
                    page, body = b_page, b_body
            except Exception as browser_err:
                if page is None:
                    raise RuntimeError(f"http: {http_err}; browser: {browser_err}")
                note = f"Note: the page looked thin and the browser fallback failed ({type(browser_err).__name__}), so this may be incomplete.\n"
    else:
        page, body = fetch(mode)
except Exception as e:
    print(f"{type(e).__name__}: {e}", file=sys.stderr)
    sys.exit(1)

title = (page.css("title::text").get() or "").strip()
print(f"Title: {title}\nURL: {page.url}\n{note}\n{body}")
