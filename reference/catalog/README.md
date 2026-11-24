# Catalog golden

The golden for `internal/catalog`: the web flasher's own `js/catalog.js` (`loadCatalog`, `roleValue`,
`renderNotice`, `hasVersions`) and `js/flash.js` (`pickFlashFile`, `esp32Address`) from flasher.meshcore.dev at
45a5257 (MIT, (c) 2025 Rastislav Vysoky), run unmodified in Node over saved copies of the catalogs. The Go tests
resolve the same inputs and must give exactly the same devices, order, versions, files, titles and notes, and pick
the same file and address for every version (except the refusals listed in `internal/catalog/golden_test.go`).

## Re-recording

```bash
../fetch-flasher.sh          # once: the flasher's JavaScript at 45a5257 in reference/.flasher (git-ignored)
make record NODE=/opt/node22/bin/node
go test ./internal/catalog/  # from the repository root
```

`make record` writes `internal/catalog/testdata/golden-{meshcore,zephcore,git,edges}.json.gz` from the committed
inputs:

| Golden | Host `js/site.js` sees | Inputs (testdata) |
| --- | --- | --- |
| `golden-meshcore` | flasher.meshcore.io | `meshcore-config`, `meshcore-releases-github`, `meshcore-releases-github-ripple` |
| `golden-zephcore` | zephcore.meshcore.dev | `zephcore-config`, `zephcore-releases-github-zephcore` |
| `golden-git` | flasher.meshcore.io | `git-config` (the flasher repo's own `config.json` at 45a5257, with the broken second "LilyGo T-Lora Pager"), and the same two release lists |
| `golden-edges` | flasher.meshcore.io | `edges-config`, `edges-releases-github`: the hand-written catalog in `edges/` (below) |

Each golden holds the SHA-256 of every input it was made from (the Go test checks them), the requests
`loadCatalog` made, the resolved catalog exactly as `JSON.stringify` prints it, and for every firmware entry with
versions its title, subtitle, rendered notice and what `pickFlashFile`/`esp32Address` choose for each version.

`edges/` is a small hand-written catalog of what the live ones don't show, so the reference, not a guess, decides
what it resolves to: patterns that are a literal between `^` and `$` with decoy names around them (`XAnchored_…`,
`….bin.sig`), integer-like version names (JavaScript lists `2` and `10` before `v1.2.0`), file patterns in reverse
key order, a version two releases share, untyped releases and an untyped source, a release file without a name (it
is tested as "undefined"), a source without file patterns (its inline versions stay), a source that replaces stale
inline versions, a version with only a `download` file, and a board that is dropped. After editing it, run
`make edges-inputs record`.

`make inputs` re-imports the inputs from the research folder's saved live copies (`RESEARCH=/home/user/meshflash-research`
by default; fetched 2026-11-24, Ripple's list in `catalog-live/`) and the git catalog from `reference/.flasher`,
gzipped with `gzip -9n` so the same input gives the same bytes, and gzips `edges/` (`make edges-inputs` does only
that). Run `make record` after it. To follow a newer live
catalog, save the new copies there first; nothing here touches the network.

## How it works

`run.mjs` sets `globalThis.location` (which `js/site.js` reads when it is imported) and a `fetch` that serves each
request from its gzipped input file and answers 404 to anything else, as a missing file would abort the site.
`hooks.mjs` maps the flasher's absolute imports (`/lib/…`, `/js/…`) onto the checkout, so `js/flash.js` loads with
its `lib/esp32.js` and `lib/dfu.js` as they are. Node 22 is needed (`/opt/node22/bin/node`).
