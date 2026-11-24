# The config tool's reply parsing, recorded

The web flasher has no setup automation (its console only sends what you type), so the reference for setup is
MeshCore's own "Repeater Setup" tool, config.meshcore.io, which the flasher opens (docs/meshcore-flashing.md 7.9,
9.6). `golden.json` records, for byte streams that `FakeNode` sends back, what that tool's `lib/serial-cli.js` writes
for each command and which reply it takes, fed the stream whole and a byte at a time. `TestConfigTool` checks that
the fake still sends those streams, and then runs nodecli's own write-and-wait (`conn.exchange`) against each
recorded stream, fed whole and a byte at a time: it must write the bytes the tool wrote (`command` + CR) and return
the reply the tool took, and its parser (`findReply`) must take that reply at the first prefix of the stream that
completes it, except where the package comment lists a departure (each case says why).

To re-record, after changing `FakeNode` or the cases in `configtool_test.go` (Node 22):

```bash
git clone https://github.com/meshcore-dev/config.meshcore.io /tmp/config.meshcore.io
git -C /tmp/config.meshcore.io checkout aff42ad8
export GOTOOLCHAIN=go1.25.4
go test ./internal/nodecli -run TestConfigTool -update-streams      # writes testdata/configtool/streams.json
node internal/nodecli/testdata/configtool/record.mjs /tmp/config.meshcore.io \
  internal/nodecli/testdata/configtool/streams.json > internal/nodecli/testdata/configtool/golden.json
rm internal/nodecli/testdata/configtool/streams.json
go test ./internal/nodecli -run TestConfigTool
```

The tool's code is not copied here; `record.mjs` imports it from the checkout, unmodified.
