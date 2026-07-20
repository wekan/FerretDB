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

The `docs/` folder is a location GitHub Pages recognizes, so there are two common
ways to publish it. Pick one.

### Option A — GitHub Actions (recommended, automatic)

This repository ships `.github/workflows/pages.yml`, which regenerates the HTML from
the Markdown with `docs/build.py` and deploys `docs/` on every push to `main-v1`.

One-time setup: in the repository, go to **Settings → Pages → Build and deployment**
and set **Source** to **GitHub Actions**. That's it — each push that touches `docs/`
republishes the site, and the live URL appears on the workflow run and under
**Settings → Pages**.

### Option B — Deploy from the branch's `/docs` folder (no workflow)

Because the rendered `.html` is committed, GitHub can serve it directly with no
build step. In **Settings → Pages → Build and deployment**, set **Source** to
**Deploy from a branch**, choose branch **`main-v1`** and folder **`/docs`**, and
Save. GitHub serves the committed HTML as-is (the `.nojekyll` file disables Jekyll
so the pre-rendered pages are used verbatim).

With Option B, remember to run `python3 docs/build.py` and commit the regenerated
`.html` after editing any `.md`. Option A does that for you.

The published site's home page is `index.html` (rendered from `main.md`).

## Local preview

```sh
python3 -m http.server 3000 --directory docs
# then open http://localhost:3000/
```
