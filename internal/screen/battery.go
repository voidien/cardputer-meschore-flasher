package screen

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PowerSupplies is where Linux lists the batteries and chargers. The CardputerZero's battery is bq27220-0.
const PowerSupplies = "/sys/class/power_supply"

// Battery finds the battery among the power supplies in dir (the first of type "Battery") and returns a function that
// gives its charge as "87%", or "" when there's no battery or it can't be read. It reads only when asked, which is
// when the screen redraws (nothing polls in the background), and keeps a reading for a minute, since the charge moves
// slowly and a passage being written redraws many times a second.
func Battery(dir string, now func() time.Time) func() string {
	capacity := ""
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		kind, err := os.ReadFile(filepath.Join(dir, e.Name(), "type"))
		if err == nil && strings.TrimSpace(string(kind)) == "Battery" {
			capacity = filepath.Join(dir, e.Name(), "capacity")
			break
		}
	}
	if capacity == "" {
		return func() string { return "" }
	}
	var (
		mu   sync.Mutex
		at   time.Time
		last string
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		t := now()
		if !at.IsZero() && t.Sub(at) < time.Minute && t.Sub(at) >= 0 {
			return last
		}
		at, last = t, ""
		b, err := os.ReadFile(capacity)
		if err != nil {
			return last
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n < 0 || n > 100 {
			return last
		}
		last = strconv.Itoa(n) + "%"
		return last
	}
}
