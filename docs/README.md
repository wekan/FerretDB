# FerretDB v1 (SQLite) documentation

This directory holds the documentation **source as plain Markdown (`.md`) files**
and the **rendered static website** (`.html` + `style.css`) generated from them. The
site is static and self-contained, so it can be published directly by **GitHub
Pages** — no Docusaurus, Node, or build server needed.

## Layout

- `*.md`, `*/**.md` — the documentation source (edit these).
- `*.html` — the rendered pages (one next to each `.md`; do not edit by hand).
- `index.html` — the home page (rendered from `main.md`, which has `slug: /`).
- `style.css` — the site stylesheet.
- `img/` — images used by the docs.
- `.nojekyll` — tells GitHub Pages to serve the generated HTML as-is.
- `build.py` — the small, dependency-free Markdown → HTML generator.

## Rebuild after editing

```sh
python3 docs/build.py      # from the repository root
# or
cd docs && python3 build.py
```

Re-run it whenever you add or change a `.md` file, and commit both the `.md` and the
regenerated `.html`.

## Publish on GitHub Pages

In the repository settings, set **Pages → Build and deployment → Source** to
**Deploy from a branch**, branch `main-v1`, folder `/docs`. GitHub serves the
committed HTML directly (the `.nojekyll` file disables Jekyll processing).

## Local preview

```sh
python3 -m http.server 3000 --directory docs
# then open http://localhost:3000/
```
