# FerretDB v1 (SQLite) documentation

This directory holds the documentation as **plain Markdown (`.md`) files only**. There
is **no committed HTML** — the website is rendered from the Markdown on demand (for
local preview) and at deploy time (for GitHub Pages) by the small, dependency-free
generator `build.py`.

## Layout

- `*.md`, `*/**.md` — the documentation source (this is all you edit).
- `main.md` — the home page (it has `slug: /`, so it becomes `index.html`).
- `img/` — images used by the docs.
- `build.py` — the Markdown → HTML generator (build / serve).
- `_site/` — the rendered site when you build it locally; **git-ignored**, never committed.

## View it locally without saving any HTML

```sh
python3 docs/build.py --serve        # from the repo root (default port 3000)
# or:  cd docs && python3 build.py --serve 3000
```

This starts a small local server that renders each Markdown page **in memory on every
request** and writes nothing to disk — edit a `.md`, refresh the browser, and you see
the change. Open http://127.0.0.1:3000/. (Same as `task docs-dev`.)

## Build the static site (optional)

```sh
python3 docs/build.py                 # renders into docs/_site/ (git-ignored)
python3 docs/build.py --out /tmp/site # or a directory of your choice
```

You normally do **not** need this — it is what the GitHub Pages workflow runs at
deploy time.

## Publish on GitHub Pages

The site is published by rendering at deploy time, so no HTML is ever stored in the
repository.

`.github/workflows/pages.yml` renders the Markdown with `build.py` and deploys the
result on every push to `main-v1`. **One-time setup:** in the repository, go to
**Settings → Pages → Build and deployment** and set **Source** to **GitHub Actions**.
After that, every push that touches `docs/` republishes the site.

### Which workflow runs it? (avoid the duplicate)

Two names can show up under the Actions tab, and only **one** should be active:

- **`Pages`** — this repo's workflow (`.github/workflows/pages.yml`), used when
  **Source = GitHub Actions**. It renders the Markdown with `build.py` (admonitions,
  navigation, styling) and deploys it. **This is the one to use.**
- **`pages-build-deployment`** — GitHub's built-in, auto-generated workflow (not a
  file here), which runs only when **Source = Deploy from a branch**. That mode would
  serve this Markdown-only folder through Jekyll and show a broken page (literal
  `:::note`, no sidebar), so it is **not** used here.

Pick **Source = GitHub Actions** — then only `Pages` runs and `pages-build-deployment`
never appears. There is nothing to delete from the repository for it; it is controlled
entirely by the Pages Source setting.

Where to find the published URL: it is shown under **Settings → Pages** ("Your site is
live at …") and on each **Pages** workflow run (the `deploy` job's environment link).
For this fork the default is `https://<owner>.github.io/FerretDB/` (e.g.
`https://wekan.github.io/FerretDB/`) unless a custom domain is configured. The
generated links are all relative, so the site works correctly under that `/FerretDB/`
sub-path with no extra configuration.
