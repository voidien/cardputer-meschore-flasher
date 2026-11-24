package usbid

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSys is a sysfs tree in a temporary directory, laid out as Linux lays it out, with real symlinks, so Scan is
// tested on the same walk it does on the device. Adapted from the research's scripts/usbids/fakesys, which has the
// class/tty entries as plain directories; here they are symlinks into /sys/devices, as on a real kernel:
//
//	class/tty/ttyACM0 -> ../../devices/.../usb1/1-1/1-1.3/1-1.3:1.0/tty/ttyACM0
//	    device -> ../../../1-1.3:1.0                (cdc_acm: the tty hangs off the USB interface)
//	class/tty/ttyUSB0 -> ../../devices/.../usb1/1-1/1-1.2/1-1.2:1.0/ttyUSB0/tty/ttyUSB0
//	    device -> ../../../ttyUSB0                  (usb-serial: off a port, which hangs off the interface)
//
// The host controller's root hub (usb1) and a hub (1-1) above every board also have idVendor files, so a walk that
// went too far would read the hub's identity instead of the board's.
type fakeSys struct {
	t    *testing.T
	root string
}

// hubDir is where boards are plugged in: a hub on the first root-hub port, as on a Raspberry Pi.
const hubDir = "devices/platform/soc/3f980000.usb/usb1/1-1"

func newFakeSys(t *testing.T) *fakeSys {
	t.Helper()
	f := &fakeSys{t: t, root: t.TempDir()}
	f.mkdir("class/tty")
	for _, d := range []string{"bus/usb/drivers/cdc_acm", "bus/usb/drivers/cp210x", "bus/usb/drivers/ch341",
		"bus/usb-serial/drivers/cp210x", "bus/usb-serial/drivers/ch341-uart", "bus/usb/drivers/hub"} {
		f.mkdir(d)
	}
	f.write("devices/platform/soc/3f980000.usb/usb1", map[string]string{
		"idVendor": "1d6b", "idProduct": "0002", "bcdDevice": "0612", "manufacturer": "Linux 6.18.39 dwc_otg_hcd",
		"product": "DWC OTG Controller", "serial": "3f980000.usb"})
	f.write(hubDir, map[string]string{"idVendor": "0424", "idProduct": "2514", "bcdDevice": "0b53"})
	// Ttys that aren't USB serial ports, which Scan must skip.
	f.write("devices/virtual/tty/tty0", nil)
	f.link("class/tty/tty0", "devices/virtual/tty/tty0")
	f.write("devices/platform/serial8250/tty/ttyS0", nil)
	f.link("devices/platform/serial8250/tty/ttyS0/device", "devices/platform/serial8250")
	f.link("class/tty/ttyS0", "devices/platform/serial8250/tty/ttyS0")
	return f
}

// usbBoard is a USB device to plug into the tree. An empty string leaves its file out, as the kernel does for a
// string descriptor the device doesn't have.
type usbBoard struct {
	usbPath                       string // the device's name under the hub: "1-1.3"
	vid, pid, bcd                 string // four lower-case hex digits, as sysfs writes them
	manufacturer, product, serial string
	iface                         string // the interface's string
	driver                        string // the interface's driver
}

func (b usbBoard) deviceDir() string { return filepath.Join(hubDir, b.usbPath) }
func (b usbBoard) intfDir() string   { return filepath.Join(b.deviceDir(), b.usbPath+":1.0") }

// plugDevice writes the USB device and its first interface.
func (f *fakeSys) plugDevice(b usbBoard) {
	f.write(b.deviceDir(), map[string]string{"idVendor": b.vid, "idProduct": b.pid, "bcdDevice": b.bcd,
		"manufacturer": b.manufacturer, "product": b.product, "serial": b.serial, "busnum": "1", "devnum": "5"})
	f.write(b.intfDir(), map[string]string{"bInterfaceNumber": "00", "bInterfaceClass": "02", "interface": b.iface})
	f.link(filepath.Join(b.intfDir(), "driver"), filepath.Join("bus/usb/drivers", b.driver))
	// A second interface (CDC data), with no tty, as a real CDC-ACM device has.
	f.write(filepath.Join(b.deviceDir(), b.usbPath+":1.1"), map[string]string{"bInterfaceNumber": "01"})
}

// plugACM plugs b in as a cdc_acm device with tty name tty.
func (f *fakeSys) plugACM(tty string, b usbBoard) {
	f.plugDevice(b)
	ttyDir := filepath.Join(b.intfDir(), "tty", tty)
	f.write(ttyDir, map[string]string{"dev": "166:0"})
	f.rawLink(filepath.Join(ttyDir, "device"), "../../../"+b.usbPath+":1.0")
	f.link(filepath.Join("class/tty", tty), ttyDir)
}

// plugUSBSerial plugs b in as a usb-serial bridge (cp210x, ch341) whose port driver is portDriver.
func (f *fakeSys) plugUSBSerial(tty string, b usbBoard, portDriver string) {
	f.plugDevice(b)
	portDir := filepath.Join(b.intfDir(), tty)
	f.write(portDir, map[string]string{"port_number": "0"})
	f.link(filepath.Join(portDir, "driver"), filepath.Join("bus/usb-serial/drivers", portDriver))
	ttyDir := filepath.Join(portDir, "tty", tty)
	f.write(ttyDir, map[string]string{"dev": "188:0"})
	f.rawLink(filepath.Join(ttyDir, "device"), "../../../"+tty)
	f.link(filepath.Join("class/tty", tty), ttyDir)
}

// unplug removes a board and its tty, as the kernel does when it goes away or re-enumerates.
func (f *fakeSys) unplug(tty string, b usbBoard) {
	f.t.Helper()
	for _, p := range []string{filepath.Join("class/tty", tty), b.deviceDir()} {
		if err := os.RemoveAll(filepath.Join(f.root, p)); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fakeSys) scan() []Port {
	f.t.Helper()
	ports, err := Scan(f.root)
	if err != nil {
		f.t.Fatalf("Scan: %v", err)
	}
	return ports
}

func (f *fakeSys) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.root, rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// write makes dir and writes each non-empty attribute as sysfs would: the value and a newline.
func (f *fakeSys) write(dir string, attrs map[string]string) {
	f.t.Helper()
	f.mkdir(dir)
	for name, v := range attrs {
		if v == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(f.root, dir, name), []byte(v+"\n"), 0o444); err != nil {
			f.t.Fatal(err)
		}
	}
}

// link makes a relative symlink at rel pointing to target, both relative to the root, as sysfs's links are.
func (f *fakeSys) link(rel, target string) {
	f.t.Helper()
	from := filepath.Dir(filepath.Join(f.root, rel))
	r, err := filepath.Rel(from, filepath.Join(f.root, target))
	if err != nil {
		f.t.Fatal(err)
	}
	f.rawLink(rel, r)
}

func (f *fakeSys) rawLink(rel, target string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(rel))
	if err := os.Symlink(target, filepath.Join(f.root, rel)); err != nil {
		f.t.Fatal(err)
	}
}

// The owner's boards and two bridges, as they would show on USB (SPEC.md §11; identities from table.go's sources).
// The serial numbers are made up in each chip's own format.
var (
	heltecV4 = usbBoard{usbPath: "1-1.3", vid: "303a", pid: "1001", bcd: "0101", manufacturer: "Espressif",
		product: "USB JTAG/serial debug unit", serial: "60:55:F9:C0:27:18", driver: "cdc_acm"}
	rakApp = usbBoard{usbPath: "1-1.3", vid: "239a", pid: "8029", bcd: "0100", manufacturer: "RAKwireless",
		product: "WisCore RAK4631 Board", serial: "D2A1F3C4B5E69788", iface: "TinyUSB Serial", driver: "cdc_acm"}
	rakBootloader = usbBoard{usbPath: "1-1.3", vid: "239a", pid: "002a", bcd: "0100", manufacturer: "RAKWireless",
		product: "WisBlock RAK4631", serial: "D2A1F3C4B5E69788", iface: "nRF Serial", driver: "cdc_acm"}
	xiaoNRF = usbBoard{usbPath: "1-1.2", vid: "2886", pid: "8044", bcd: "0100", manufacturer: "Seeed Studio",
		product: "XIAO nRF52840", serial: "8A4C2E1F0B3D5A69", iface: "TinyUSB Serial", driver: "cdc_acm"}
	wioL1App = usbBoard{usbPath: "1-1.4", vid: "2886", pid: "1667", bcd: "0100", manufacturer: "Seeed Studio",
		product: "Seeed Wio Tracker L1", serial: "0F1E2D3C4B5A6978", iface: "TinyUSB Serial", driver: "cdc_acm"}
	wioL1Bootloader = usbBoard{usbPath: "1-1.4", vid: "2886", pid: "1667", bcd: "0100", manufacturer: "Seeed",
		product: "Wio Tracker L1", serial: "0F1E2D3C4B5A6978", iface: "nRF Serial", driver: "cdc_acm"}
	cp2102 = usbBoard{usbPath: "1-1.2", vid: "10c4", pid: "ea60", bcd: "0100", manufacturer: "Silicon Labs",
		product: "CP2102 USB to UART Bridge Controller", serial: "0001", driver: "cp210x"}
	ch340 = usbBoard{usbPath: "1-1.4", vid: "1a86", pid: "7523", bcd: "0264", product: "USB2.0-Serial",
		driver: "ch341"}
)
