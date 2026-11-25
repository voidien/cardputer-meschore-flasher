package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete; builders C and D both use it for the
// screens' goldens. End-to-end tests use newWorldDriver instead, over the real orchestration.

import (
	"context"
	"strings"
	"sync"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/usbid"
)

// fakeBackend is a Backend with canned answers that a test sets, recording every call. Its fields are read by Cmds
// on other goroutines: set them before the screens ask, or inside set. Assert on calls with calledAny or by counting
// one kind: the full list changes as the other builder's screens land (the frame's scans, the Board's probes).
type fakeBackend struct {
	mu    sync.Mutex
	calls []string // "Scan", "Probe ttyACM0", "UpdateCatalog", "Download 2", "Flash", "DeleteFile <name>", …

	scan    flash.Scan
	scanErr error
	// nodes are Probe's answers by tty name (none: Silent); readNodes ReadNode's (none: Failed, "no answer set").
	nodes, readNodes map[string]flash.Node
	// cat is LoadCatalog's answer; nil gives catErr (catalog.ErrNoCatalog by default).
	cat    *catalog.Catalog
	catErr error
	// updated is UpdateCatalog's answer; nil gives updateErr.
	updated   *catalog.Catalog
	updateErr error
	saved     []catalog.Entry
	savedErr  error
	deleted   []string
	// prepare answers Prepare; nil gives a Problem saying no answer was set.
	prepare func(flash.Request) flash.Prepared
	// download, flashJob and setup give the Jobs; nil gives a Job already finished with "no job set".
	download func([]catalog.File) *flash.Job
	flashJob func(flash.Request) *flash.Job
	setup    func(flash.SetupRequest) *flash.Job
	// requests and setups are what Flash and Setup were given. setups hold the admin password: compare it, never
	// print it.
	requests []flash.Request
	setups   []flash.SetupRequest

	// battery and hasBattery are Battery's answer: -1 with hasBattery is a battery that can't be read now.
	battery       int
	hasBattery    bool
	synced        bool
	messenger     nodecli.MessengerRadio
	messengerNote string
}

// newFakeBackend is no ports, no catalog, no battery reading, a clock not synchronised and no messenger file.
func newFakeBackend() *fakeBackend {
	return &fakeBackend{catErr: catalog.ErrNoCatalog, nodes: map[string]flash.Node{},
		readNodes: map[string]flash.Node{}}
}

// set changes the fake under its lock, for a change while Cmds may be running.
func (f *fakeBackend) set(change func(f *fakeBackend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// called is every call so far, in order.
func (f *fakeBackend) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// calledAny says whether any call so far starts with prefix ("UpdateCatalog", "Download", "Flash").
func (f *fakeBackend) calledAny(prefix string) bool {
	for _, c := range f.called() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeBackend) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeBackend) Scan() (flash.Scan, error) {
	f.record("Scan")
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.scan
	s.Ports = append([]flash.Port(nil), s.Ports...)
	return s, f.scanErr
}

func (f *fakeBackend) Probe(ctx context.Context, p flash.Port) flash.Node {
	f.record("Probe " + p.Name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[p.Name]; ok {
		return n
	}
	if p.Kind == usbid.NRFBootloader {
		return flash.Node{Port: p, Problem: flash.InBootloader}
	}
	return flash.Node{Port: p, Problem: flash.Silent}
}

func (f *fakeBackend) LoadCatalog() (*catalog.Catalog, error) {
	f.record("LoadCatalog")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cat != nil {
		return f.cat, nil
	}
	return nil, f.catErr
}

func (f *fakeBackend) UpdateCatalog(ctx context.Context) (*catalog.Catalog, error) {
	f.record("UpdateCatalog")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updated != nil {
		f.cat = f.updated
		return f.updated, nil
	}
	return nil, f.updateErr
}

func (f *fakeBackend) SavedFiles() ([]catalog.Entry, error) {
	f.record("SavedFiles")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]catalog.Entry(nil), f.saved...), f.savedErr
}

func (f *fakeBackend) DeleteFile(name string) error {
	f.record("DeleteFile " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	for i, e := range f.saved {
		if e.Name == name {
			f.saved = append(f.saved[:i:i], f.saved[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeBackend) Download(ctx context.Context, files []catalog.File) *flash.Job {
	f.record("Download")
	f.mu.Lock()
	fn := f.download
	f.mu.Unlock()
	if fn == nil {
		return noJob(flash.JobDownload)
	}
	return fn(files)
}

func (f *fakeBackend) Prepare(req flash.Request) flash.Prepared {
	f.record("Prepare")
	f.mu.Lock()
	fn := f.prepare
	f.mu.Unlock()
	if fn == nil {
		return flash.Prepared{Problem: "the test set no Prepare answer"}
	}
	return fn(req)
}

func (f *fakeBackend) Flash(ctx context.Context, req flash.Request) *flash.Job {
	f.record("Flash")
	f.mu.Lock()
	f.requests = append(f.requests, req)
	fn := f.flashJob
	f.mu.Unlock()
	if fn == nil {
		return noJob(flash.JobFlash)
	}
	return fn(req)
}

func (f *fakeBackend) ReadNode(ctx context.Context, p flash.Port, firstBoot bool) flash.Node {
	f.record("ReadNode " + p.Name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.readNodes[p.Name]; ok {
		return n
	}
	return flash.Node{Port: p, Problem: flash.Failed, Reason: "the test set no ReadNode answer"}
}

func (f *fakeBackend) Setup(ctx context.Context, req flash.SetupRequest) *flash.Job {
	f.record("Setup")
	f.mu.Lock()
	f.setups = append(f.setups, req)
	fn := f.setup
	f.mu.Unlock()
	if fn == nil {
		return noJob(flash.JobSetup)
	}
	return fn(req)
}

func (f *fakeBackend) Battery() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.battery, f.hasBattery
}

func (f *fakeBackend) ClockSynced() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.synced
}

func (f *fakeBackend) MessengerRadio() (nodecli.MessengerRadio, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messenger, f.messengerNote
}

// newJob is a Job a test moves on itself: Set to publish a picture, Finish to end it, then settle to show it. Its
// pictures' Now is zero unless the test sets it.
func newJob(kind flash.JobKind) *flash.Job { return flash.NewJob(kind, nil, nil) }

// finishedJob is a Job that has already ended with o.
func finishedJob(kind flash.JobKind, o flash.Outcome) *flash.Job {
	j := newJob(kind)
	j.Finish(o)
	return j
}

func noJob(kind flash.JobKind) *flash.Job {
	return finishedJob(kind, flash.Outcome{Title: "No job set", Summary: []string{"The test set no Job for this."}})
}

// portOf is a flash.Port exactly as flash.Backend.Scan makes one (flash.PortOf).
func portOf(p usbid.Port) flash.Port { return flash.PortOf(p) }

// The owner's boards and two bridges as they show on USB (internal/usbid's fakesys_test.go identities), on a tty.
func heltecV4Port(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x303a, PID: 0x1001, BCD: 0x0101,
		Manufacturer: "Espressif", Product: "USB JTAG/serial debug unit", Serial: "60:55:F9:C0:27:18",
		Driver: "cdc_acm", USBPath: "1-1.3"})
}

func rakPort(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x239a, PID: 0x8029, BCD: 0x0100,
		Manufacturer: "RAKwireless", Product: "WisCore RAK4631 Board", Serial: "D2A1F3C4B5E69788",
		Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.3"})
}

func rakBootloaderPort(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x239a, PID: 0x002a, BCD: 0x0100,
		Manufacturer: "RAKWireless", Product: "WisBlock RAK4631", Serial: "D2A1F3C4B5E69788",
		Interface: usbid.InterfaceNRFBootloader, Driver: "cdc_acm", USBPath: "1-1.3"})
}

// rakBootloaderUnconfirmedPort is the RAK's bootloader VID:PID with no interface string: usbid classifies it as a
// bootloader from the table alone, which isn't enough for DFU (flash.Port.InBootloader is false).
func rakBootloaderUnconfirmedPort(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x239a, PID: 0x002a, BCD: 0x0100,
		Manufacturer: "RAKWireless", Product: "WisBlock RAK4631", Serial: "D2A1F3C4B5E69788", Driver: "cdc_acm",
		USBPath: "1-1.3"})
}

func xiaoPort(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x2886, PID: 0x8044, BCD: 0x0100,
		Manufacturer: "Seeed Studio", Product: "XIAO nRF52840", Serial: "8A4C2E1F0B3D5A69",
		Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.2"})
}

func cp2102Port(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x10c4, PID: 0xea60, BCD: 0x0100,
		Manufacturer: "Silicon Labs", Product: "CP2102 USB to UART Bridge Controller", Serial: "0001",
		Driver: "cp210x", USBPath: "1-1.2"})
}

func ch340Port(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x1a86, PID: 0x7523, BCD: 0x0264,
		Product: "USB2.0-Serial", Driver: "ch341", USBPath: "1-1.4"})
}
