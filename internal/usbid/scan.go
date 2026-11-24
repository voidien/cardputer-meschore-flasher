package usbid

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Port is one USB serial port and the USB identity of the device behind it, as sysfs shows it.
type Port struct {
	Name string // the tty's name: "ttyACM0"
	Dev  string // its device node: "/dev/ttyACM0"

	VID, PID uint16 // idVendor, idProduct
	BCD      uint16 // bcdDevice: the device's release number, 0x0101 for 1.01

	// The device's string descriptors. Each is "" when the device has none: the kernel then leaves the file out
	// (CH340 bridges have no manufacturer and no serial).
	Manufacturer string
	Product      string
	Serial       string

	// Interface is the string of the USB interface the tty hangs off: "TinyUSB Serial" for an nRF52 app,
	// "nRF Serial" for its bootloader, "TinyUSB CDC" for an ESP32-S3 TinyUSB app, "" for the ESP32's own
	// USB-Serial-JTAG and for most bridges.
	Interface string
	// Driver is the kernel driver bound to that interface: cdc_acm, cp210x, ch341.
	Driver string
	// USBPath is the USB device's sysfs name, which says where it's plugged in ("1-1.3": bus 1, port 1, then port 3
	// of the hub on it). It stays the same across a reset as long as the board stays in the same socket.
	USBPath string
}

// VIDPID is the identity as lsusb prints it, "2886:8044", for the screens.
func (p Port) VIDPID() string { return fmt.Sprintf("%04x:%04x", p.VID, p.PID) }

// Scan lists every USB serial port (ttyACM* and ttyUSB*) under sysRoot, "" meaning "/sys", sorted by name with
// numbers in numeric order (ttyACM2 before ttyACM10). It reads sysfs only and never opens a tty, so it needs no
// permission beyond what every user has and can't disturb a board (research §6.6).
//
// A tty whose USB interface and device can't be found is left out: that happens when the board is unplugged while
// Scan runs, and a port that is going away is nothing to offer. Scan fails only when it can't list the ttys at all.
func Scan(sysRoot string) ([]Port, error) {
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	// Resolve the root first, so the walk up from each tty can stop at it however the root itself was reached.
	root, err := filepath.EvalSymlinks(sysRoot)
	if err != nil {
		return nil, fmt.Errorf("usbid: reading %s: %w", sysRoot, err)
	}
	classDir := filepath.Join(root, "class", "tty")
	entries, err := os.ReadDir(classDir)
	if err != nil {
		return nil, fmt.Errorf("usbid: listing serial ports: %w", err)
	}
	var ports []Port
	for _, e := range entries {
		if !isUSBSerialName(e.Name()) {
			continue
		}
		if p, ok := readPort(root, classDir, e.Name()); ok {
			ports = append(ports, p)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return lessTTY(ports[i].Name, ports[j].Name) })
	return ports, nil
}

// readPort reads one tty's identity. /sys/class/tty/<tty>/device is the USB interface for cdc_acm (ttyACM) and the
// usb-serial port for the bridge drivers (ttyUSB), whose parent is the interface; so the rule that works for both is
// to resolve the link and walk up to the first directory with an idVendor file, which is the USB device (research
// §6.6; linux v6.18 cdc-acm.c:1529-1530, usb-serial.c:1075, bus.c:53-54).
//
// One check more than the research's rule: the device found must be the parent of the interface found on the way.
// When a board goes away while Scan reads it, its files vanish one by one, and a plain walk up would carry on to the
// hub above it and report the hub's identity as the board's.
func readPort(root, classDir, name string) (Port, bool) {
	start, err := filepath.EvalSymlinks(filepath.Join(classDir, name, "device"))
	if err != nil {
		return Port{}, false
	}
	var intf, usb string
	for dir := start; strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if exists(filepath.Join(dir, "idVendor")) {
			usb = dir
			break
		}
		if intf == "" && exists(filepath.Join(dir, "bInterfaceNumber")) {
			intf = dir
		}
	}
	if usb == "" || intf == "" || filepath.Dir(intf) != usb {
		return Port{}, false
	}
	p := Port{
		Name:         name,
		Dev:          "/dev/" + name,
		VID:          readHex(usb, "idVendor"),
		PID:          readHex(usb, "idProduct"),
		BCD:          readHex(usb, "bcdDevice"),
		Manufacturer: readString(usb, "manufacturer"),
		Product:      readString(usb, "product"),
		Serial:       readString(usb, "serial"),
		USBPath:      filepath.Base(usb),
	}
	p.Interface = readString(intf, "interface")
	// The interface's driver, not the usb-serial port's: for a CH340 the port's is "ch341-uart" and the interface's
	// is "ch341", the module's name, which is what the research and the screens use.
	if link, err := os.Readlink(filepath.Join(intf, "driver")); err == nil {
		p.Driver = filepath.Base(link)
	}
	return p, true
}

// readString reads a sysfs string attribute. The kernel writes "%s\n" (core/sysfs.c:124-143) and leaves the file out
// when the device has no such string, which reads as "".
func readString(dir, file string) string {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readHex reads idVendor, idProduct or bcdDevice: four lower-case hex digits and a newline (core/sysfs.c:690-705).
// Anything else reads as 0, which no table row has.
func readHex(dir, file string) uint16 {
	v, err := strconv.ParseUint(readString(dir, file), 16, 16)
	if err != nil {
		return 0
	}
	return uint16(v)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// isUSBSerialName keeps the names cdc-acm (ttyACM<n>) and usb-serial (ttyUSB<n>) give their ttys and skips the
// hundreds of others in /sys/class/tty (tty1, ttyS0, ttyAMA0, ...).
func isUSBSerialName(name string) bool {
	prefix, n := splitTTY(name)
	return (prefix == "ttyACM" || prefix == "ttyUSB") && n >= 0
}

// lessTTY orders ttyACM before ttyUSB, and by number within each, so ttyACM10 comes after ttyACM2.
func lessTTY(a, b string) bool {
	pa, na := splitTTY(a)
	pb, nb := splitTTY(b)
	if pa != pb {
		return pa < pb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

// splitTTY splits "ttyACM12" into "ttyACM" and 12. The number is -1 when the name doesn't end in one.
func splitTTY(name string) (string, int) {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i == len(name) {
		return name, -1
	}
	n, err := strconv.Atoi(name[i:])
	if err != nil {
		return name, -1
	}
	return name[:i], n
}
