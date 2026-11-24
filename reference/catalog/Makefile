# Catalog golden for internal/catalog (README.md here says what each target does).
#   make record   run the web flasher's own js/catalog.js + js/flash.js over the committed inputs, write the goldens
#   make inputs   re-import the inputs from the research folder's saved live copies, and gzip the hand-written
#                 edge-case catalog in edges/ (then make record)

NODE ?= node
FLASHER ?= ../.flasher
COMMIT := 45a5257
TESTDATA := ../../internal/catalog/testdata
RESEARCH ?= /home/user/meshflash-research

MESHCORE_INPUTS := \
	'/config.json=$(TESTDATA)/meshcore-config.json.gz' \
	'/releases?repo=github=$(TESTDATA)/meshcore-releases-github.json.gz' \
	'/releases?repo=github-ripple=$(TESTDATA)/meshcore-releases-github-ripple.json.gz'
RELEASES_ONLY := \
	'/releases?repo=github=$(TESTDATA)/meshcore-releases-github.json.gz' \
	'/releases?repo=github-ripple=$(TESTDATA)/meshcore-releases-github-ripple.json.gz'
ZEPHCORE_INPUTS := \
	'/config-zephcore.json=$(TESTDATA)/zephcore-config.json.gz' \
	'/releases?repo=github-zephcore=$(TESTDATA)/zephcore-releases-github-zephcore.json.gz'

.PHONY: record inputs edges-inputs check-flasher

check-flasher:
	@test -d $(FLASHER)/js || { echo "no flasher checkout at $(FLASHER): run ../fetch-flasher.sh"; exit 1; }
	@git -C $(FLASHER) rev-parse HEAD | grep -q '^$(COMMIT)' || \
		{ echo "$(FLASHER) is not at $(COMMIT): run ../fetch-flasher.sh"; exit 1; }

record: check-flasher
	$(NODE) run.mjs $(FLASHER) flasher.meshcore.io $(TESTDATA)/golden-meshcore.json.gz $(MESHCORE_INPUTS)
	$(NODE) run.mjs $(FLASHER) zephcore.meshcore.dev $(TESTDATA)/golden-zephcore.json.gz $(ZEPHCORE_INPUTS)
	$(NODE) run.mjs $(FLASHER) flasher.meshcore.io $(TESTDATA)/golden-git.json.gz \
		'/config.json=$(TESTDATA)/git-config.json.gz' $(RELEASES_ONLY)
	$(NODE) run.mjs $(FLASHER) flasher.meshcore.io $(TESTDATA)/golden-edges.json.gz \
		'/config.json=$(TESTDATA)/edges-config.json.gz' '/releases?repo=github=$(TESTDATA)/edges-releases-github.json.gz'

# The saved live copies (fetched 2026-11-24; ripple's list from the 2026-11-22/24 fetch in catalog-live) and the git
# catalog at $(COMMIT). gzip -n leaves out the name and time, so the same input gives the same bytes. Ripple's release
# notes, in its release list and inline in both catalogs, are replaced with a placeholder on the way in
# (blank-notes.mjs says why).
inputs: check-flasher edges-inputs
	$(NODE) blank-notes.mjs < $(RESEARCH)/catalog-live2/flasher.meshcore.io_config.json__cb_20261124 | gzip -9n > $(TESTDATA)/meshcore-config.json.gz
	gzip -9n < $(RESEARCH)/catalog-live2/flasher.meshcore.io_releases_repo_github__cb_20261124 > $(TESTDATA)/meshcore-releases-github.json.gz
	$(NODE) blank-notes.mjs < $(RESEARCH)/catalog-live/flasher.meshcore.io_releases_repo_github-ripple | gzip -9n > $(TESTDATA)/meshcore-releases-github-ripple.json.gz
	gzip -9n < $(RESEARCH)/catalog-live2/flasher.meshcore.io_config-zephcore.json__cb_20261124 > $(TESTDATA)/zephcore-config.json.gz
	gzip -9n < $(RESEARCH)/catalog-zeph/zephcore.meshcore.dev_releases_repo_github-zephcore > $(TESTDATA)/zephcore-releases-github-zephcore.json.gz
	$(NODE) blank-notes.mjs < $(FLASHER)/config.json | gzip -9n > $(TESTDATA)/git-config.json.gz

# A small hand-written catalog of the cases the live ones don't show (anchored literal patterns with decoys,
# integer-like version names, pattern key order, a repeated version, untyped releases, a file without a name, a
# source without files): the reference decides what they resolve to.
edges-inputs:
	gzip -9n < edges/config.json > $(TESTDATA)/edges-config.json.gz
	gzip -9n < edges/releases-github.json > $(TESTDATA)/edges-releases-github.json.gz
