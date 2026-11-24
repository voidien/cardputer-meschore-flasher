#!/bin/sh
# Fetches the web flasher's own JavaScript at the commit meshflash follows, into reference/.flasher (git-ignored).
# The recordings in internal/*/testdata were made from it; tests never run this or touch the network.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
commit=45a5257bf792
if [ ! -d "$here/.flasher/.git" ]; then
	git clone --quiet https://github.com/meshcore-dev/flasher.meshcore.dev.git "$here/.flasher"
fi
git -C "$here/.flasher" checkout --quiet "$commit"
echo "reference/.flasher at $(git -C "$here/.flasher" log -1 --format='%h %ad' --date=short)"
