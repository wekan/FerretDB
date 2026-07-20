#!/usr/bin/env python3
"""
Static documentation site generator for FerretDB v1 (SQLite).

The documentation source is kept as plain Markdown (.md) files in this directory.
This script renders them into a static HTML website (one .html next to each .md,
plus index.html, style.css and a navigation sidebar) that can be published as-is
by GitHub Pages — no Docusaurus, Node or build server required.

Usage:  python3 build.py     (run from the docs/ directory)

It is a small, dependency-free Markdown subset renderer covering what these docs
use: headings (+anchors), paragraphs, fenced code, inline code, bold/italic,
links, images, GitHub tables, blockquotes, ordered/unordered nested lists,
horizontal rules, admonitions (:::note/tip/info/caution) and pass-through inline
HTML (<br/>, <sub>). Re-run it after editing any .md file.
"""

import html
import os
import re

ROOT = os.path.dirname(os.path.abspath(__file__))
SITE_TITLE = "FerretDB v1 (SQLite) Documentation"

# --------------------------------------------------------------------------- #
# helpers
# --------------------------------------------------------------------------- #

def slugify(text):
    text = re.sub(r"`", "", text)
    text = re.sub(r"<[^>]+>", "", text)
    text = text.strip().lower()
    text = re.sub(r"[^\w\s-]", "", text)
    text = re.sub(r"[\s_]+", "-", text)
    return text.strip("-")


def esc(text):
    return html.escape(text, quote=False)


def humanize(name):
    return name.replace("-", " ").replace("_", " ").title()


def rel_prefix(depth):
    return "../" * depth


def rewrite_link(url, depth):
    u = url.strip()
    if u.startswith("<") and u.endswith(">"):  # markdown autolink target
        u = u[1:-1].strip()
    if u.startswith(("http://", "https://", "mailto:", "//", "#")):
        return u
    pre = rel_prefix(depth)
    # split off any anchor first
    anchor = ""
    if "#" in u:
        u, anchor = u.split("#", 1)
        anchor = "#" + anchor
    if u.startswith("/img/"):
        return pre + u[1:] + anchor
    if u in ("", "/"):
        return pre + "index.html" + anchor
    if u.endswith(".md"):
        u = u[:-3] + ".html"
    elif u.endswith("/"):
        # directory / category link -> its index page
        u = pre + u.lstrip("/") + "index.html"
    elif u.startswith("/"):
        # root-absolute internal link -> relative to site root
        u = pre + u.lstrip("/")
        if not u.endswith(".html") and "." not in os.path.basename(u):
            u += ".html"
    return u + anchor


# --------------------------------------------------------------------------- #
# inline markdown
# --------------------------------------------------------------------------- #

def inline(text, depth):
    tokens = []

    def stash(h):
        tokens.append(h)
        return "\x00%d\x00" % (len(tokens) - 1)

    # inline code
    text = re.sub(r"`([^`]+)`", lambda m: stash("<code>" + esc(m.group(1)) + "</code>"), text)
    # images
    text = re.sub(
        r"!\[([^\]]*)\]\(([^)]+)\)",
        lambda m: stash('<img alt="%s" src="%s">' % (esc(m.group(1)), rewrite_link(m.group(2), depth))),
        text,
    )
    # links with an angle-bracket target: [text](<url with (parens)>)
    text = re.sub(
        r"\[([^\]]+)\]\(<([^>]+)>\)",
        lambda m: stash('<a href="%s">%s</a>' % (rewrite_link(m.group(2), depth), esc(m.group(1)))),
        text,
    )
    # links
    text = re.sub(
        r"\[([^\]]+)\]\(([^)]+)\)",
        lambda m: stash('<a href="%s">%s</a>' % (rewrite_link(m.group(2), depth), esc(m.group(1)))),
        text,
    )
    # bare URLs
    text = re.sub(
        r'(?<![\("=])(https?://[^\s<>)\]]+)',
        lambda m: stash('<a href="%s">%s</a>' % (m.group(1), esc(m.group(1)))),
        text,
    )
    # pass-through inline HTML tags (<br/>, <sub>, </sub>, ...)
    text = re.sub(r"</?[a-zA-Z][^>]*>", lambda m: stash(m.group(0)), text)

    # escape everything else
    text = esc(text)

    # emphasis (after escaping; placeholders are \x00..\x00 and survive)
    text = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)
    text = re.sub(r"(?<!\w)_([^_\n]+)_(?!\w)", r"<em>\1</em>", text)
    text = re.sub(r"(?<!\*)\*([^*\n]+)\*(?!\*)", r"<em>\1</em>", text)

    text = re.sub(r"\x00(\d+)\x00", lambda m: tokens[int(m.group(1))], text)
    return text


# --------------------------------------------------------------------------- #
# block markdown
# --------------------------------------------------------------------------- #

ADMONITIONS = {"note": "Note", "tip": "Tip", "info": "Info", "caution": "Caution", "warning": "Warning"}


def render_list(lines, depth):
    """Render a block of list lines (already sharing a common base indent)."""
    items = []  # (indent, marker_type, first_line_text, [child_lines])
    i = 0
    item_re = re.compile(r"^(\s*)([-*+]|\d+\.)\s+(.*)$")
    # determine base indent
    base = None
    out = []
    ordered = False

    def flush(item_lines, ordered_flag):
        # item_lines: list of (text) where first is the marker text, rest continuation/children
        first = item_lines[0]
        rest = item_lines[1:]
        # split rest into nested-list lines vs paragraph continuation
        nested = []
        cont = []
        for ln in rest:
            if item_re.match(ln):
                nested.append(ln)
            elif ln.strip() == "":
                cont.append("")
            else:
                (nested if nested else cont).append(ln)
        body = inline(first.strip(), depth)
        if cont and any(c.strip() for c in cont):
            body += " " + inline(" ".join(c.strip() for c in cont if c.strip()), depth)
        if nested:
            body += "\n" + render_list([re.sub(r"^\s{0,2}", "", n) for n in nested], depth)
        return "<li>%s</li>" % body

    # group into items
    grouped = []
    cur = None
    for ln in lines:
        m = item_re.match(ln)
        if m and (base is None or len(m.group(1)) <= base + 1):
            if base is None:
                base = len(m.group(1))
                ordered = bool(re.match(r"\d+\.", m.group(2)))
            if cur is not None:
                grouped.append(cur)
            cur = [m.group(3)]
        else:
            if cur is None:
                cur = [ln]
            else:
                cur.append(ln)
    if cur is not None:
        grouped.append(cur)

    for g in grouped:
        out.append(flush(g, ordered))
    tag = "ol" if ordered else "ul"
    return "<%s>\n%s\n</%s>" % (tag, "\n".join(out), tag)


def render_table(rows, depth):
    header = [c.strip() for c in rows[0].strip().strip("|").split("|")]
    aligns = []
    for c in rows[1].strip().strip("|").split("|"):
        c = c.strip()
        if c.startswith(":") and c.endswith(":"):
            aligns.append("center")
        elif c.endswith(":"):
            aligns.append("right")
        elif c.startswith(":"):
            aligns.append("left")
        else:
            aligns.append("")
    body = rows[2:]

    def cells(line, tag):
        cs = [c.strip() for c in line.strip().strip("|").split("|")]
        out = []
        for idx, c in enumerate(cs):
            a = aligns[idx] if idx < len(aligns) else ""
            style = ' style="text-align:%s"' % a if a else ""
            out.append("<%s%s>%s</%s>" % (tag, style, inline(c, depth), tag))
        return "<tr>%s</tr>" % "".join(out)

    html_rows = ["<thead>", cells(rows[0], "th"), "</thead>", "<tbody>"]
    for r in body:
        if r.strip():
            html_rows.append(cells(r, "td"))
    html_rows.append("</tbody>")
    return "<table>\n%s\n</table>" % "\n".join(html_rows)


def md_to_html(body, depth, toc):
    lines = body.split("\n")
    out = []
    i = 0
    n = len(lines)
    item_re = re.compile(r"^(\s*)([-*+]|\d+\.)\s+")

    while i < n:
        line = lines[i]

        # blank
        if line.strip() == "":
            i += 1
            continue

        # fenced code
        m = re.match(r"^```(.*)$", line)
        if m:
            lang = m.group(1).strip()
            i += 1
            code = []
            while i < n and not lines[i].startswith("```"):
                code.append(lines[i])
                i += 1
            i += 1  # closing fence
            cls = ' class="language-%s"' % esc(lang) if lang else ""
            out.append("<pre><code%s>%s</code></pre>" % (cls, esc("\n".join(code))))
            continue

        # HTML comment line
        if line.lstrip().startswith("<!--"):
            while i < n and "-->" not in lines[i]:
                i += 1
            i += 1
            continue

        # admonition
        m = re.match(r"^:::(\w+)\s*(.*)$", line)
        if m and m.group(1) in ADMONITIONS:
            kind = m.group(1)
            title = m.group(2).strip() or ADMONITIONS[kind]
            i += 1
            inner = []
            while i < n and not lines[i].startswith(":::"):
                inner.append(lines[i])
                i += 1
            i += 1  # closing :::
            out.append(
                '<div class="admonition admonition-%s">\n<div class="admonition-title">%s</div>\n%s\n</div>'
                % (kind, esc(title), md_to_html("\n".join(inner), depth, toc))
            )
            continue

        # heading
        m = re.match(r"^(#{1,6})\s+(.*)$", line)
        if m:
            level = len(m.group(1))
            text = m.group(2).strip()
            hid = slugify(text)
            if level in (2, 3):
                toc.append((level, hid, re.sub(r"`", "", text)))
            out.append('<h%d id="%s">%s</h%d>' % (level, hid, inline(text, depth), level))
            i += 1
            continue

        # horizontal rule
        if re.match(r"^(-{3,}|\*{3,}|_{3,})\s*$", line):
            out.append("<hr>")
            i += 1
            continue

        # table (header + separator)
        if "|" in line and i + 1 < n and re.match(r"^\s*\|?[\s:|-]+\|?\s*$", lines[i + 1]) and "-" in lines[i + 1]:
            rows = [line, lines[i + 1]]
            i += 2
            while i < n and "|" in lines[i] and lines[i].strip():
                rows.append(lines[i])
                i += 1
            out.append(render_table(rows, depth))
            continue

        # blockquote
        if line.lstrip().startswith(">"):
            quote = []
            while i < n and lines[i].lstrip().startswith(">"):
                quote.append(re.sub(r"^\s*>\s?", "", lines[i]))
                i += 1
            out.append("<blockquote>\n%s\n</blockquote>" % md_to_html("\n".join(quote), depth, toc))
            continue

        # list
        if item_re.match(line):
            block = []
            while i < n and (item_re.match(lines[i]) or lines[i].startswith((" ", "\t")) or lines[i].strip() == ""):
                # stop if a blank line is followed by a non-indented non-list line
                if lines[i].strip() == "":
                    if i + 1 < n and lines[i + 1].strip() != "" and not item_re.match(lines[i + 1]) and not lines[i + 1].startswith((" ", "\t")):
                        break
                block.append(lines[i])
                i += 1
            out.append(render_list(block, depth))
            continue

        # paragraph
        para = [line]
        i += 1
        while i < n and lines[i].strip() != "" and not re.match(r"^(#{1,6}\s|```|:::|>|\s*\|)", lines[i]) and not item_re.match(lines[i]) and not re.match(r"^(-{3,}|\*{3,})\s*$", lines[i]):
            para.append(lines[i])
            i += 1
        out.append("<p>%s</p>" % inline(" ".join(p.strip() for p in para), depth))

    return "\n".join(out)


# --------------------------------------------------------------------------- #
# frontmatter + page model
# --------------------------------------------------------------------------- #

def parse_frontmatter(text):
    meta = {}
    if text.startswith("---"):
        end = text.find("\n---", 3)
        if end != -1:
            fm = text[3:end].strip()
            body = text[end + 4:]
            for ln in fm.split("\n"):
                if ":" in ln:
                    k, v = ln.split(":", 1)
                    meta[k.strip()] = v.strip()
            return meta, body.lstrip("\n")
    return meta, text


def page_title(meta, body, relpath):
    if meta.get("title"):
        return meta["title"].strip("'\"")
    m = re.search(r"^#\s+(.*)$", body, re.M)
    if m:
        return re.sub(r"`", "", m.group(1)).strip()
    return humanize(os.path.splitext(os.path.basename(relpath))[0])


# --------------------------------------------------------------------------- #
# collect pages + build nav
# --------------------------------------------------------------------------- #

def collect_pages():
    pages = {}
    for dirpath, _dirs, files in os.walk(ROOT):
        for f in sorted(files):
            if not f.endswith(".md"):
                continue
            full = os.path.join(dirpath, f)
            rel = os.path.relpath(full, ROOT)
            if rel == "README.md":
                continue
            text = open(full, encoding="utf-8").read()
            meta, bodyx = parse_frontmatter(text)
            pos = meta.get("sidebar_position")
            try:
                pos = int(pos)
            except (TypeError, ValueError):
                pos = 999
            is_index = meta.get("slug", "").strip() == "/" or rel == "main.md"
            out_rel = "index.html" if is_index else rel[:-3] + ".html"
            pages[rel] = {
                "rel": rel,
                "out": out_rel,
                "title": page_title(meta, bodyx, rel),
                "desc": meta.get("description", "").strip("'\""),
                "pos": pos,
                "depth": 0 if is_index else rel.count(os.sep),
                "body": bodyx,
                "is_index": is_index,
                "dir": os.path.dirname(rel),
            }
    return pages


def build_nav(pages, current_out, current_depth):
    """Return sidebar HTML relative to the current page's depth."""
    pre = rel_prefix(current_depth)

    def href(p):
        return pre + p["out"]

    # group by top-level directory
    top = [p for p in pages.values() if p["dir"] == ""]
    dirs = {}
    for p in pages.values():
        if p["dir"] != "":
            top_dir = p["dir"].split(os.sep)[0]
            dirs.setdefault(top_dir, []).append(p)

    def li(p):
        cls = ' class="active"' if p["out"] == current_out else ""
        return '<li%s><a href="%s">%s</a></li>' % (cls, href(p), esc(p["title"]))

    parts = ["<ul class='nav-top'>"]
    for p in sorted(top, key=lambda x: (x["pos"], x["title"])):
        parts.append(li(p))
    parts.append("</ul>")

    for d in sorted(dirs, key=lambda dd: min(p["pos"] for p in dirs[dd])):
        parts.append('<div class="nav-cat">%s</div>' % esc(humanize(d)))
        parts.append("<ul>")
        # include nested subdir pages, sorted by (subpath, pos)
        for p in sorted(dirs[d], key=lambda x: (x["dir"], x["pos"], x["title"])):
            parts.append(li(p))
        parts.append("</ul>")

    return "\n".join(parts)


PAGE_TMPL = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title} — FerretDB v1 (SQLite)</title>
<meta name="description" content="{desc}">
<link rel="stylesheet" href="{pre}style.css">
</head>
<body>
<header class="topbar">
  <a class="brand" href="{pre}index.html">FerretDB v1 <span>(SQLite)</span></a>
  <input type="checkbox" id="navtoggle" hidden>
  <label for="navtoggle" class="navtoggle-btn" aria-label="Toggle navigation">☰</label>
</header>
<div class="layout">
  <nav class="sidebar">{nav}</nav>
  <main class="content">
    <article>{content}</article>
    <footer class="pagefoot">
      <p>FerretDB v1 (SQLite) — a general-purpose MongoDB replacement. Source:
      <a href="https://github.com/wekan/FerretDB">github.com/wekan/FerretDB</a>.
      This page is generated from <code>{src}</code> by <code>docs/build.py</code>.</p>
    </footer>
  </main>
</div>
</body>
</html>
"""

STYLE = """/* Generated static docs styling — no framework. */
:root{--fg:#1c1e21;--muted:#606770;--bg:#fff;--side:#f5f6f7;--border:#e3e6ea;--accent:#0b7285;--code:#f2f4f6}
*{box-sizing:border-box}
body{margin:0;font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:var(--fg);background:var(--bg)}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
.topbar{position:sticky;top:0;z-index:10;display:flex;align-items:center;gap:12px;padding:10px 16px;background:#0b7285;color:#fff;border-bottom:1px solid #095a68}
.brand{color:#fff;font-weight:700;font-size:18px}
.brand span{opacity:.8;font-weight:400}
.navtoggle-btn{margin-left:auto;color:#fff;font-size:22px;cursor:pointer;display:none}
.layout{display:flex;align-items:flex-start;max-width:1200px;margin:0 auto}
.sidebar{flex:0 0 260px;position:sticky;top:52px;max-height:calc(100vh - 52px);overflow:auto;padding:20px 12px;background:var(--side);border-right:1px solid var(--border);font-size:14px}
.sidebar ul{list-style:none;margin:0 0 10px;padding:0}
.sidebar li a{display:block;padding:5px 10px;border-radius:6px;color:var(--fg)}
.sidebar li a:hover{background:#e9ecef;text-decoration:none}
.sidebar li.active a{background:var(--accent);color:#fff}
.nav-cat{margin:14px 8px 4px;font-size:12px;font-weight:700;letter-spacing:.04em;text-transform:uppercase;color:var(--muted)}
.content{flex:1 1 auto;min-width:0;padding:28px 40px 60px}
.content article{max-width:820px}
h1,h2,h3,h4{line-height:1.25;margin-top:1.6em}
h1{margin-top:.2em;font-size:2em}
h2{border-bottom:1px solid var(--border);padding-bottom:.2em}
code{background:var(--code);padding:.1em .35em;border-radius:4px;font-size:.9em;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre{background:#1e2125;color:#e6e6e6;padding:14px 16px;border-radius:8px;overflow:auto}
pre code{background:none;padding:0;color:inherit;font-size:.88em}
table{border-collapse:collapse;width:100%;margin:1em 0;display:block;overflow:auto}
th,td{border:1px solid var(--border);padding:6px 10px;text-align:left;vertical-align:top}
th{background:var(--side)}
blockquote{margin:1em 0;padding:.2em 1em;border-left:4px solid var(--border);color:var(--muted)}
img{max-width:100%}
hr{border:0;border-top:1px solid var(--border);margin:2em 0}
.admonition{margin:1.2em 0;padding:12px 16px;border-radius:8px;border-left:5px solid;background:var(--code)}
.admonition-title{font-weight:700;margin-bottom:4px}
.admonition-note{border-color:#4098d7;background:#eef6fc}
.admonition-tip{border-color:#37b24d;background:#ebfbee}
.admonition-info{border-color:#7048e8;background:#f3f0ff}
.admonition-caution,.admonition-warning{border-color:#f59f00;background:#fff9db}
.pagefoot{margin-top:50px;padding-top:16px;border-top:1px solid var(--border);color:var(--muted);font-size:13px}
@media(max-width:820px){
 .navtoggle-btn{display:block}
 .sidebar{display:none;position:fixed;top:52px;left:0;right:0;bottom:0;max-height:none;flex-basis:auto;z-index:9}
 #navtoggle:checked ~ .layout .sidebar{display:block}
 .content{padding:20px}
}
"""


def main():
    pages = collect_pages()
    for p in pages.values():
        nav = build_nav(pages, p["out"], p["depth"])
        toc = []
        content = md_to_html(p["body"], p["depth"], toc)
        html_out = PAGE_TMPL.format(
            title=esc(p["title"]),
            desc=esc(p["desc"]),
            pre=rel_prefix(p["depth"]),
            nav=nav,
            content=content,
            src=esc(p["rel"]),
        )
        out_path = os.path.join(ROOT, p["out"])
        os.makedirs(os.path.dirname(out_path) or ROOT, exist_ok=True)
        open(out_path, "w", encoding="utf-8").write(html_out)

    open(os.path.join(ROOT, "style.css"), "w", encoding="utf-8").write(STYLE)
    open(os.path.join(ROOT, ".nojekyll"), "w").write("")
    print("Rendered %d pages." % len(pages))


if __name__ == "__main__":
    main()
