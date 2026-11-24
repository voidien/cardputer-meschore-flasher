package nodecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

const (
	adminPW = "Tr0ub4dor&3x"
	guestPW = "gu3st-Pa55"
)

func f64(x float64) *float64 { return &x }
func iptr(x int) *int        { return &x }

// goodSettings passes Check on any node v1.11.0+ (with the guest password set for a room server).
func goodSettings() Settings {
	return Settings{
		Name:          "Hilltop 2",
		Radio:         Radio{FreqMHz: 910.525, BWkHz: 62.5, SF: 7, CR: 5},
		TxDBm:         20,
		AdminPassword: adminPW,
	}
}

// rig is a fake node on a fake port in virtual time, recording everything.
type rig struct {
	node  *FakeNode
	clock *serialport.VirtualClock
	log   *serialport.Log
	port  *serialport.Fake
}

func newRig(role, version string) *rig {
	r := &rig{node: NewFakeNode(role, version), log: &serialport.Log{}}
	r.node.Banner = true
	r.clock = serialport.NewVirtualClock(r.log)
	r.port = serialport.NewFake(r.node, r.clock, r.log, serialport.Options{})
	return r
}

// reopen is what the caller does after the reboot: close the port, and open it again once the board is back.
func (r *rig) reopen() {
	r.port.Close()
	r.port = serialport.NewFake(r.node, r.clock, r.log, serialport.Options{})
}

// The whole setup against the fake, for both roles at the versions the task names: probe, read the current
// settings, plan, run, reopen, confirm; then what the node stored and what went on the wire.
func TestSetupEveryVersion(t *testing.T) {
	ctx := context.Background()
	for _, ver := range []string{"v1.11.0", "v1.13.0", "v1.14.0", "v1.17.1", "v1.18.0"} {
		for _, role := range []string{RoleRepeater, RoleRoomServer} {
			t.Run(ver+"/"+role, func(t *testing.T) {
				r := newRig(role, ver)
				r.node.Noise = []byte("DEBUG: radio started\r\n")
				start := r.clock.Now()
				info, err := Probe(ctx, r.port, r.clock)
				if err != nil {
					t.Fatal(err)
				}
				if info.Version.String() != ver || info.Role != role || info.Board != "Heltec V3" || !strings.Contains(info.Version.Raw, "(Build: ") {
					t.Fatalf("Probe: %+v", info)
				}
				if el := r.clock.Now().Sub(start); el > time.Second {
					t.Errorf("Probe took %v of virtual time", el)
				}
				if got := strings.Join(r.node.Commands(), "|"); got != "ver|get role|board" {
					t.Errorf("Probe sent %q", got)
				}

				cur, err := ReadSettings(ctx, r.port, r.clock, info)
				if err != nil {
					t.Fatal(err)
				}
				// A new node's 2-minute local advert reads as off: the first save turns it off.
				if cur.Name != r.node.Name || cur.Radio != DefaultRadio || cur.TxDBm != 22 || *cur.AdvertInterval != 0 || *cur.Lat != 0 {
					t.Errorf("ReadSettings: %+v", cur)
				}
				// The node's own values, with the passwords typed, pass Check: the form opens on good defaults.
				def := cur
				def.AdminPassword = adminPW
				if role == RoleRoomServer {
					def.GuestPassword = guestPW
				}
				if p := Check(def, info); p != nil {
					t.Errorf("the node's own settings don't pass Check: %v", p)
				}
				if want := map[bool]int{true: 1, false: 0}[info.Version.AtLeast(1, 14, 0)]; cur.PathHashBytes != want {
					t.Errorf("ReadSettings path hash %d, want %d", cur.PathHashBytes, want)
				}
				if role == RoleRoomServer && cur.GuestPassword != "hello" {
					t.Errorf("ReadSettings guest password wrong")
				}

				s := cur
				s.Name = "Hilltop 2"
				Presets[6].Apply(&s) // Canada: 910.525, 62.5, SF7, CR5, path hash 3 bytes
				s.TxDBm = -5         // printed as 251 by v1.11.0 and v1.18.0, as -5 by v1.13.0-v1.17.1
				s.Lat, s.Lon = f64(51.5074), f64(-0.1278)
				s.AdvertInterval = iptr(120)
				s.AdminPassword = adminPW
				if role == RoleRoomServer {
					s.GuestPassword = guestPW
				}
				s.ClockSynced = true
				steps, err := Plan(s, info)
				if err != nil {
					t.Fatal(err)
				}
				var reports []Progress
				r.node.commands = nil
				if err := Run(ctx, r.port, r.clock, steps, func(p Progress) { reports = append(reports, p) }); err != nil {
					t.Fatal(err)
				}
				for i, st := range steps {
					want := Done
					if st.Command == "" {
						want = Skipped
					}
					var last Progress
					for _, p := range reports {
						if p.Index == i {
							last = p
						}
					}
					if last.Status != want || last.Display != st.Display || last.Total != len(steps) {
						t.Errorf("step %d %q ended %+v", i, st.Display, last)
					}
				}

				// Version gates: path.hash.mode only on v1.14.0+.
				sent := strings.Join(r.node.Commands(), "\n")
				if has := strings.Contains(sent, "path.hash.mode"); has != info.Version.AtLeast(1, 14, 0) {
					t.Errorf("path.hash.mode sent: %v\n%s", has, sent)
				}
				if !info.Version.AtLeast(1, 14, 0) && !strings.Contains(fmt.Sprint(steps), "Path hash: not set (needs v1.14.0 or newer)") {
					t.Error("no note that the path hash was left out")
				}
				if strings.Contains(sent, "guest.password") != (role == RoleRoomServer) {
					t.Errorf("guest password sent on a %s:\n%s", role, sent)
				}
				// Everything ends in CR only, and the last thing sent is reboot.
				tx := r.log.TX()
				if bytes.Contains(tx, []byte("\n")) || !bytes.HasSuffix(tx, []byte("\rreboot\r")) {
					t.Errorf("wire: %q", tx[max(0, len(tx)-60):])
				}

				// The node holds what was sent; the radio runs after the reboot.
				n := r.node
				wantRadio := n.radio(Radio{910.525, 62.5, 7, 5})
				if n.Name != "Hilltop 2" || n.Radio != wantRadio || n.TxPower != -5 || n.Lat != 51.5074 || n.Lon != -0.1278 ||
					n.AdvertInterval != 60 || n.AdminPassword != adminPW {
					t.Errorf("node holds %+v", n)
				}
				if info.Version.AtLeast(1, 14, 0) && n.PathHashMode != 2 {
					t.Errorf("path hash mode %d", n.PathHashMode)
				}
				if role == RoleRoomServer && n.GuestPassword != guestPW {
					t.Error("guest password not stored")
				}
				if want := r.clock.Now().Unix(); n.Clock < want-60 || n.Clock > want {
					t.Errorf("node clock %d, the Zero's %d", n.Clock, want)
				}
				if n.Reboots() != 1 || n.ActiveRadio == wantRadio {
					t.Errorf("reboots %d, running %+v before the reopen", n.Reboots(), n.ActiveRadio)
				}

				r.reopen()
				if err := Confirm(ctx, r.port, r.clock, s.Want(info)); err != nil {
					t.Fatal(err)
				}
				if n.ActiveRadio != wantRadio {
					t.Errorf("running %+v after the reboot", n.ActiveRadio)
				}
				if el := r.clock.Now().Sub(start); el > 30*time.Second {
					t.Errorf("the whole setup took %v of virtual time", el)
				}
			})
		}
	}
}

// Plan's commands, exactly, for a room server on v1.18.0 (the passwords are on the wire, and only there).
func TestPlanCommands(t *testing.T) {
	v, _ := ParseVersion("v1.18.0-81dbae5 (Build: 15-Oct-2026)")
	s := goodSettings()
	s.Radio = Radio{FreqMHz: 869.6179809, BWkHz: 62.5, SF: 8, CR: 8} // as read back from a node
	s.PathHashBytes = 2
	s.Lat, s.Lon = f64(-33.8688), f64(151.2093)
	s.AdvertInterval = iptr(0)
	s.GuestPassword = guestPW
	s.ClockSynced = true
	steps, err := Plan(s, Info{Version: v, Role: RoleRoomServer})
	if err != nil {
		t.Fatal(err)
	}
	var cmds, shown []string
	for _, st := range steps {
		cmds = append(cmds, st.Command)
		shown = append(shown, st.Display)
	}
	want := []string{
		"time", "set name Hilltop 2", "set radio 869.618,62.5,8,8", "set tx 20", "set path.hash.mode 1",
		"set lat -33.8688", "set lon 151.2093", "set advert.interval 0", "set guest.password " + guestPW,
		"password " + adminPW, "get name", "get radio", "get tx", "get path.hash.mode", "get guest.password",
		"get lat", "get lon", "get advert.interval", "reboot",
	}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
	wantShown := []string{
		"Clock: set to the Zero's time", "Name: Hilltop 2", "Radio: 869.618 MHz, 62.5 kHz, SF8, CR8", "TX power: 20 dBm",
		"Path hash: 2 bytes", "Latitude: -33.8688", "Longitude: 151.2093", "Local advert: off", "Guest password: ••••",
		"Admin password: ••••", "Check name", "Check radio", "Check TX power", "Check path hash",
		"Check guest password", "Check latitude", "Check longitude", "Check local advert",
		"Reboot to start the new radio",
	}
	if strings.Join(shown, "\n") != strings.Join(wantShown, "\n") {
		t.Errorf("displays:\n%s", strings.Join(shown, "\n"))
	}
	// Without a synchronised clock the step says it was skipped, and sends nothing.
	s.ClockSynced = false
	steps, _ = Plan(s, Info{Version: v, Role: RoleRoomServer})
	if steps[0].Command != "" || steps[0].Display != "Clock: not set (the Zero's clock isn't synchronised)" {
		t.Errorf("clock step %q %q", steps[0].Command, steps[0].Display)
	}
}

// Every bandwidth the radios take is sent exactly as the list writes it, and frequencies cleanly.
func TestRadioArg(t *testing.T) {
	for _, b := range Bandwidths {
		r := Radio{FreqMHz: 869.618, BWkHz: b.KHz, SF: 8, CR: 5}
		if got, want := r.Arg(), "869.618,"+b.Text+",8,5"; got != want {
			t.Errorf("%v: %q, want %q", b.KHz, got, want)
		}
	}
	for in, want := range map[float64]string{869.618: "869.618", 869.6179809: "869.618", 910.5250244: "910.525",
		915.7999877: "915.8", 921: "921", 433.375: "433.375", 2400.5: "2400.5"} {
		if got := freqText(in); got != want {
			t.Errorf("freqText(%v) = %q, want %q", in, got, want)
		}
	}
	if !(Radio{869.6179809, 62.5, 8, 5}).Near(DefaultRadio) || (Radio{869.619, 62.5, 8, 5}).Near(DefaultRadio) {
		t.Error("Near is wrong")
	}
}

// Each validation refusal names its field, says how to fix it, and never quotes a password.
func TestCheckRefusals(t *testing.T) {
	v1180, _ := ParseVersion("v1.18.0-81dbae5")
	v1140, _ := ParseVersion("v1.14.0-9f1a3ea")
	rep := Info{Version: v1180, Role: RoleRepeater, Board: "Heltec V3"}
	room := Info{Version: v1180, Role: RoleRoomServer, Board: "Heltec V3"}
	for _, c := range []struct {
		name   string
		info   Info
		change func(*Settings)
		field  string
		says   string
	}{
		{"empty name", rep, func(s *Settings) { s.Name = "" }, "Name", "type a name, 1 to 23 bytes"},
		{"leading space", rep, func(s *Settings) { s.Name = " Hill" }, "Name", "space"},
		{"trailing space", rep, func(s *Settings) { s.Name = "Hill " }, "Name", "space"},
		{"slash", rep, func(s *Settings) { s.Name = "Hill/2" }, "Name", `"/"`},
		{"colon", rep, func(s *Settings) { s.Name = "Hill:2" }, "Name", `":"`},
		{"star", rep, func(s *Settings) { s.Name = "Hill*" }, "Name", `"*"`},
		{"line break", rep, func(s *Settings) { s.Name = "Hill\r2" }, "Name", "control character"},
		{"24 bytes", rep, func(s *Settings) { s.Name = strings.Repeat("x", 24) }, "Name", "is 24 bytes"},
		{"24 bytes of emoji", rep, func(s *Settings) { s.Name = strings.Repeat("📡", 6) }, "Name", "is 24 bytes"},
		{"bad UTF-8", rep, func(s *Settings) { s.Name = "Hill\xff" }, "Name", "valid text"},
		{"150 MHz before v1.15", Info{Version: v1140, Role: RoleRepeater}, func(s *Settings) { s.Radio.FreqMHz = 200 }, "Radio", "300 to 2500 MHz"},
		{"140 MHz", rep, func(s *Settings) { s.Radio.FreqMHz = 140 }, "Radio", "150 to 2500 MHz"},
		{"2600 MHz", rep, func(s *Settings) { s.Radio.FreqMHz = 2600 }, "Radio", "150 to 2500 MHz"},
		{"bandwidth 60", rep, func(s *Settings) { s.Radio.BWkHz = 60 }, "Radio", "62.5, 125, 250 or 500 kHz"},
		{"bandwidth 7", rep, func(s *Settings) { s.Radio.BWkHz = 7 }, "Radio", "bandwidth"},
		{"SF 4", rep, func(s *Settings) { s.Radio.SF = 4 }, "Radio", "spreading factor must be 5 to 12"},
		{"SF 13", rep, func(s *Settings) { s.Radio.SF = 13 }, "Radio", "spreading factor"},
		{"CR 9", rep, func(s *Settings) { s.Radio.CR = 9 }, "Radio", "coding rate must be 5 to 8"},
		{"TX 23", rep, func(s *Settings) { s.TxDBm = 23 }, "TX power", "Choose 22 or less"},
		{"TX -10", rep, func(s *Settings) { s.TxDBm = -10 }, "TX power", "below -9"},
		{"Station G2 at 20", rep, func(s *Settings) { s.TxDBm = 20; s.Device = "UnitEng Station G2" }, "TX power", "Choose 19 or less"},
		{"Station G2 board", Info{Version: v1180, Role: RoleRepeater, Board: "Station G2"}, func(s *Settings) { s.TxDBm = 20 }, "TX power", "Station G2"},
		{"Ikoka 33 dBm at 10", Info{Version: v1180, Role: RoleRepeater, Board: "Ikoka Stick-E22-33dBm (Xiao_nrf52)"}, func(s *Settings) { s.TxDBm = 10 }, "TX power", "Choose 9 or less"},
		{"Ikoka 30 dBm at 20", Info{Version: v1180, Role: RoleRepeater, Board: "Ikoka Nano-E22-30dBm (Xiao_nrf52)"}, func(s *Settings) { s.TxDBm = 20 }, "TX power", "Choose 19 or less"},
		{"Ikoka unknown module", rep, func(s *Settings) { s.TxDBm = 10; s.Device = "Ikoka Stick"; s.Name = "x" }, "TX power", "Choose 9 or less"},
		{"Heltec V4 at 23", Info{Version: v1180, Role: RoleRepeater, Board: "Heltec V4 OLED"}, func(s *Settings) { s.TxDBm = 23 }, "TX power", "Heltec V4"},
		{"path hash 4", rep, func(s *Settings) { s.PathHashBytes = 4 }, "Path hash", "1, 2 or 3"},
		{"latitude 91", rep, func(s *Settings) { s.Lat = f64(91) }, "Latitude", "−90 to 90"},
		{"longitude -181", rep, func(s *Settings) { s.Lon = f64(-181) }, "Longitude", "−180 to 180"},
		{"advert 30", rep, func(s *Settings) { s.AdvertInterval = iptr(30) }, "Local advert", "60 to 240"},
		{"advert 250", rep, func(s *Settings) { s.AdvertInterval = iptr(250) }, "Local advert", "60 to 240"},
		{"advert 75", rep, func(s *Settings) { s.AdvertInterval = iptr(75) }, "Local advert", "74 or 76"},
		{"no admin password", rep, func(s *Settings) { s.AdminPassword = "" }, "Admin password", "anyone can log in"},
		{"admin password 16 bytes", rep, func(s *Settings) { s.AdminPassword = adminPW + "abcd" }, "Admin password", "only 15 bytes"},
		{"admin password space", rep, func(s *Settings) { s.AdminPassword = adminPW + " " }, "Admin password", "space"},
		{"admin password tab", rep, func(s *Settings) { s.AdminPassword = "ab\tc" }, "Admin password", "control"},
		{"room server, no guest password", room, func(s *Settings) {}, "Guest password", "blank guest password lets anyone in"},
		{"guest password space", rep, func(s *Settings) { s.GuestPassword = " " + guestPW }, "Guest password", "space"},
	} {
		s := goodSettings()
		c.change(&s)
		p := Check(s, c.info)
		if len(p) != 1 || p[0].Field != c.field || !strings.Contains(p[0].Problem, c.says) {
			t.Errorf("%s: %v", c.name, p)
			continue
		}
		if strings.Contains(p.Error(), adminPW) || strings.Contains(p.Error(), guestPW) {
			t.Errorf("%s: the problem quotes a password: %v", c.name, p)
		}
		// Plan refuses the same settings with the same problems, sending nothing.
		if _, err := Plan(s, c.info); !errors.As(err, new(Problems)) {
			t.Errorf("%s: Plan gave %v", c.name, err)
		}
	}
	// The limits themselves pass.
	for _, c := range []struct {
		info Info
		s    func(*Settings)
	}{
		{rep, func(s *Settings) { s.TxDBm = 22; s.Name = strings.Repeat("x", 23); s.Radio.FreqMHz = 150 }},
		{rep, func(s *Settings) { s.TxDBm = -9; s.AdminPassword = Secret(strings.Repeat("p", 15)) }},
		{Info{Version: v1180, Role: RoleRepeater, Board: "Ikoka Stick-E22-33dBm (Xiao_nrf52)"}, func(s *Settings) { s.TxDBm = 9 }},
		{Info{Version: v1180, Role: RoleRepeater, Board: "Ikoka Stick-E22-22dBm (Xiao_nrf52)"}, func(s *Settings) { s.TxDBm = 22; s.Device = "Ikoka Stick" }},
		{rep, func(s *Settings) { s.TxDBm = 19; s.Device = "UnitEng Station G2" }},
		{rep, func(s *Settings) {
			s.Lat, s.Lon = f64(-90), f64(180)
			s.AdvertInterval = iptr(240)
			s.PathHashBytes = 3
		}},
		{room, func(s *Settings) { s.GuestPassword = "x"; s.Name = "Café ☕" }},
	} {
		s := goodSettings()
		c.s(&s)
		if p := Check(s, c.info); p != nil {
			t.Errorf("%+v refused: %v", s, p)
		}
	}
	// Nodes setup can't configure.
	old, _ := ParseVersion("v1.10.0-9405e8b")
	for _, info := range []Info{{Version: old, Role: RoleRepeater}, {Version: v1180, Role: "sensor"}, {Version: v1180}} {
		if _, err := Plan(goodSettings(), info); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%+v: %v", info, err)
		}
	}
	// Several problems at once come back in form order.
	s := goodSettings()
	s.Name, s.TxDBm, s.AdminPassword = "", 30, ""
	if p := Check(s, rep); len(p) != 3 || p[0].Field != "Name" || p[1].Field != "TX power" || p[2].Field != "Admin password" {
		t.Errorf("several: %v", p)
	}
}

// A node that keeps something else than it was told is caught at the read-back, by step, with ErrMismatch.
func TestReadBackMismatch(t *testing.T) {
	for _, c := range []struct {
		key, value, step, says string
		err                    error
	}{
		{"name", "Hilltop", "Check name", `the node has "Hilltop" stored, not "Hilltop 2".`, ErrMismatch},
		{"radio", "869.525,250,11,5", "Check radio", "the node has 869.525 MHz, 250 kHz, SF11, CR5 stored, not 910.525 MHz, 62.5 kHz, SF7, CR5.", ErrMismatch},
		{"tx", "22", "Check TX power", "the node has 22 dBm stored, not 20.", ErrMismatch},
		{"lat", "51.6", "Check latitude", `the node has "51.5999984" stored, not 51.5074.`, ErrMismatch},
		{"advert.interval", "60", "Check local advert", `the node has "60" stored, not "120".`, ErrMismatch},
		{"guest.password", "hello", "Check guest password", "the node has a different password stored.", ErrMismatch},
		{"password", "password", "Admin password: ••••", "the node didn't confirm the password.", ErrWrongReply},
		{"path.hash.mode", "0", "Check path hash", `the node has "0" stored, not "1".`, ErrMismatch},
	} {
		r := newRig(RoleRoomServer, "v1.18.0")
		r.node.Store = map[string]string{c.key: c.value}
		s := goodSettings()
		s.GuestPassword = guestPW
		s.Lat = f64(51.5074)
		s.AdvertInterval = iptr(120)
		s.PathHashBytes = 2
		info, err := Probe(context.Background(), r.port, r.clock)
		if err != nil {
			t.Fatal(err)
		}
		steps, err := Plan(s, info)
		if err != nil {
			t.Fatal(err)
		}
		err = Run(context.Background(), r.port, r.clock, steps, nil)
		var se *StepError
		if !errors.As(err, &se) || !errors.Is(err, c.err) || se.Display != c.step || se.Reason != c.says {
			t.Errorf("%s: %v (%#v)", c.key, err, se)
			continue
		}
		if r.node.Reboots() != 0 {
			t.Errorf("%s: rebooted after a failed step", c.key)
		}
		if strings.Contains(err.Error(), adminPW) || strings.Contains(err.Error(), guestPW) {
			t.Errorf("%s: the error shows a password: %v", c.key, err)
		}
	}
}

// A node that refuses a setting (here firmware older than the checks: a name the app would refuse, sent by hand)
// fails the step with the node's own words.
func TestRefusedSetting(t *testing.T) {
	r := newRig(RoleRepeater, "v1.12.0")
	steps := []Step{{Command: "set name a/b", Expect: exact("OK"), Display: "Name: a/b"}}
	err := Run(context.Background(), r.port, r.clock, steps, nil)
	if !errors.Is(err, ErrWrongReply) || err.Error() != `Name: a/b: the node answered "Error, bad chars".` {
		t.Errorf("%v", err)
	}
}

// After the reboot Confirm catches other firmware or another radio.
func TestConfirmCatchesChanges(t *testing.T) {
	ctx := context.Background()
	r := newRig(RoleRepeater, "v1.18.0")
	info, _ := Probe(ctx, r.port, r.clock)
	if err := Confirm(ctx, r.port, r.clock, Want{Version: info.Version, Radio: DefaultRadio}); err != nil {
		t.Errorf("unchanged: %v", err)
	}
	if err := Confirm(ctx, r.port, r.clock, Want{Version: info.Version, Radio: Presets[0].Radio}); !errors.Is(err, ErrChanged) ||
		!strings.Contains(err.Error(), "869.618 MHz, 62.5 kHz, SF8, CR5, not 915.8 MHz, 250 kHz, SF10, CR5") {
		t.Errorf("other radio: %v", err)
	}
	older, _ := ParseVersion("v1.17.1-d929643")
	if err := Confirm(ctx, r.port, r.clock, Want{Version: older, Radio: DefaultRadio}); !errors.Is(err, ErrChanged) ||
		!strings.Contains(err.Error(), "v1.18.0-81dbae5, not v1.17.1-d929643") {
		t.Errorf("other firmware: %v", err)
	}
	// A board that never comes back.
	r.node.Silent = true
	start := r.clock.Now()
	if err := Confirm(ctx, r.port, r.clock, Want{Version: info.Version, Radio: DefaultRadio}); !errors.Is(err, ErrNotCLI) {
		t.Errorf("silent: %v", err)
	}
	if el := r.clock.Now().Sub(start); el < ConfirmWait || el > ConfirmWait+2*time.Second {
		t.Errorf("waited %v", el)
	}
}

// The admin and guest passwords go on the wire and nowhere else: not in a step's Display, a progress report, an
// error, a problem, or the settings and steps printed or saved as JSON.
func TestPasswordsStayOnTheWire(t *testing.T) {
	var seen []string
	look := func(what string, s string) {
		seen = append(seen, what+": "+s)
	}
	ctx := context.Background()
	r := newRig(RoleRoomServer, "v1.18.0")
	info, err := Probe(ctx, r.port, r.clock)
	if err != nil {
		t.Fatal(err)
	}
	s := goodSettings()
	s.GuestPassword = guestPW
	s.ClockSynced = true
	steps, err := Plan(s, info)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		look("display", st.Display)
	}
	progress := func(p Progress) { look("progress", fmt.Sprintf("%s %s %s", p.Display, p.Status, p.Note)) }
	if err := Run(ctx, r.port, r.clock, steps, progress); err != nil {
		t.Fatal(err)
	}
	look("printed", fmt.Sprintf("%v %+v %#v %s %q %x", s, s, s, s.AdminPassword, s.GuestPassword, s.AdminPassword))
	look("printed", fmt.Sprintf("%v %+v %#v", steps, steps, steps[len(steps)/2]))
	look("printed", fmt.Sprintf("%v %+v", steps[9].Expect, steps[9].Expect))
	js, _ := json.Marshal(s)
	look("json", string(js))
	js, _ = json.Marshal(steps)
	look("json", string(js))

	// Passwords with spaces inside (allowed: only the ends are refused), and ones that are part of a command word,
	// printed and saved as JSON.
	for _, pw := range [][2]Secret{{"my secret pw", "two words"}, {"password", "guest"}, {"set", "get"}} {
		ss := s
		ss.AdminPassword, ss.GuestPassword = pw[0], pw[1]
		st, err := Plan(ss, info)
		if err != nil {
			t.Fatal(err)
		}
		js, _ := json.Marshal(st)
		text := string(js) + fmt.Sprintf("%v %+v %#v %v", st, st, ss, ss)
		for _, word := range []string{"my", "secret", "two", "words"} {
			if strings.Contains(text, word) {
				t.Errorf("%q from a password shows in %s", word, text)
			}
		}
		for _, cmd := range []string{`"password ••••"`, `"set guest.password ••••"`} {
			if !strings.Contains(string(js), cmd) {
				t.Errorf("%s isn't in %s", cmd, js)
			}
		}
	}

	// And when things go wrong: the node keeps another password, or another guest password, or stops answering
	// at the password.
	for _, setup := range []func(*FakeNode){
		func(n *FakeNode) { n.Store = map[string]string{"password": "else"} },
		func(n *FakeNode) { n.Store = map[string]string{"guest.password": "else"} },
		func(n *FakeNode) { n.Store = map[string]string{"name": adminPW} },
		func(n *FakeNode) { n.Hang = "password" },
		func(n *FakeNode) { n.Hang = "set guest.password" },
	} {
		r := newRig(RoleRoomServer, "v1.18.0")
		setup(r.node)
		err := Run(ctx, r.port, r.clock, steps, progress)
		if err == nil {
			t.Fatal("no error")
		}
		look("error", err.Error())
		look("error", fmt.Sprintf("%v %+v %#v", err, err, err))
	}
	// A problem with each password.
	bad := s
	bad.AdminPassword = adminPW + "-too-long"
	bad.GuestPassword = " " + guestPW
	_, err = Plan(bad, info)
	look("problems", fmt.Sprintf("%v %+v", err, err))

	for _, line := range seen {
		for _, pw := range []string{adminPW, guestPW} {
			if strings.Contains(line, pw) {
				t.Errorf("a password shows in %s", line)
			}
		}
	}
	if !strings.Contains(strings.Join(seen, "\n"), "Admin password: ••••") {
		t.Error("the password step isn't shown masked")
	}
	// They did reach the node: the wire is the one place they belong.
	tx := string(r.log.TX())
	if !strings.Contains(tx, "\rpassword "+adminPW+"\r") || !strings.Contains(tx, "set guest.password "+guestPW+"\r") {
		t.Error("the passwords never reached the wire")
	}
}

// Probe gives ErrNotCLI quickly when nothing answers like the CLI, and sends nothing but ver.
func TestProbeNotCLI(t *testing.T) {
	r := newRig(RoleRepeater, "v1.18.0")
	r.node.Silent = true
	start := r.clock.Now()
	if _, err := Probe(context.Background(), r.port, r.clock); !errors.Is(err, ErrNotCLI) {
		t.Fatalf("silent port: %v", err)
	}
	if el := r.clock.Now().Sub(start); el < 2*time.Second || el > 2500*time.Millisecond {
		t.Errorf("took %v", el)
	}
	if tx := string(r.log.TX()); tx != "ver\rver\r" {
		t.Errorf("sent %q", tx)
	}
	// Binary chatter (a companion's frames, a bootloader's SLIP) is not the CLI either.
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	p := serialport.NewFake(chatter{}, clock, log, serialport.Options{})
	if _, err := Probe(context.Background(), p, clock); !errors.Is(err, ErrNotCLI) {
		t.Fatalf("chatter: %v", err)
	}
	// A reply after an echo that isn't a version isn't the CLI, once ver has been asked for the whole wait.
	log = &serialport.Log{}
	p = serialport.NewFake(echoer{"  -> hello\r\n"}, clock, log, serialport.Options{})
	if _, err := Probe(context.Background(), p, clock); !errors.Is(err, ErrNotCLI) || !strings.Contains(err.Error(), `"hello"`) {
		t.Fatalf("not a version: %v", err)
	}
	if tx := string(log.TX()); tx != "ver\rver\r" {
		t.Errorf("not a version: sent %q", tx)
	}
	// A stray byte in the node's line buffer (line noise at power-up, half a line typed elsewhere) turns the first
	// ver into "\xffver", answered "Unknown command"; the next ver goes into an empty buffer.
	for _, role := range []string{RoleRepeater, RoleRoomServer} {
		r = newRig(role, "v1.18.0")
		r.node.line = []byte{0xff}
		start := r.clock.Now()
		info, err := Probe(context.Background(), r.port, r.clock)
		if err != nil || info.Role != role {
			t.Errorf("%s with a stray byte: %+v %v", role, info, err)
		}
		if el := r.clock.Now().Sub(start); el > QuickProbe {
			t.Errorf("%s with a stray byte took %v", role, el)
		}
	}
	// ProbeFor waits longer for a node that is still starting.
	r = newRig(RoleRepeater, "v1.18.0")
	r.node.Silent = true
	booting := r.clock.Now()
	r.clock.OnTick(func(now time.Time) {
		if now.Sub(booting) > 5*time.Second {
			r.node.mu.Lock()
			r.node.Silent = false
			r.node.mu.Unlock()
		}
	})
	if info, err := ProbeFor(context.Background(), r.port, r.clock, 15*time.Second); err != nil || info.Role != RoleRepeater {
		t.Errorf("ProbeFor: %+v %v", info, err)
	}
}

// chatter answers everything with bytes that aren't text.
type chatter struct{}

func (chatter) Receive(b []byte) []byte              { return []byte{0xc0, 0x01, 0x08, 0x3e, 0x00, 0xc0} }
func (chatter) Lines(dtr, rts bool, baud int) []byte { return nil }

// echoer echoes and adds a fixed reply.
type echoer struct{ reply string }

func (e echoer) Receive(b []byte) []byte              { return append(b, e.reply...) }
func (e echoer) Lines(dtr, rts bool, baud int) []byte { return nil }

// ReadSettings turns what each version prints back into the settings: a negative TX power printed as 256+n, the
// float32 frequency and location, the 2-minute advert unit, the path hash mode as bytes.
func TestReadSettings(t *testing.T) {
	for _, ver := range []string{"v1.11.0", "v1.13.0", "v1.18.0"} {
		r := newRig(RoleRoomServer, ver)
		n := r.node
		n.TxPower, n.Lat, n.Lon, n.AdvertInterval, n.PathHashMode = -5, 51.5074, -0.1278, 45, 1
		n.Radio = n.radio(Radio{915.8, 250, 10, 5})
		n.Name, n.GuestPassword = "Hill Room", ""
		info, err := Probe(context.Background(), r.port, r.clock)
		if err != nil {
			t.Fatal(err)
		}
		s, err := ReadSettings(context.Background(), r.port, r.clock, info)
		if err != nil {
			t.Fatalf("%s: %v", ver, err)
		}
		hash := 0
		if info.Version.AtLeast(1, 14, 0) {
			hash = 2
		}
		if s.Name != "Hill Room" || s.Radio != (Radio{915.8, 250, 10, 5}) || s.TxDBm != -5 || *s.Lat != 51.5074 ||
			*s.Lon != -0.1278 || *s.AdvertInterval != 90 || s.PathHashBytes != hash || s.GuestPassword != "" {
			t.Errorf("%s: %+v (lat %v, lon %v, advert %v)", ver, s, *s.Lat, *s.Lon, *s.AdvertInterval)
		}
		if strings.Contains(strings.Join(n.Commands(), "\n"), "set ") {
			t.Errorf("%s: ReadSettings changed something: %q", ver, n.Commands())
		}
	}
	// A repeater has no guest password field; an unsupported node is refused before anything is sent.
	if _, err := ReadSettings(context.Background(), nil, nil, Info{Role: RoleRepeater}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("old node: %v", err)
	}
}
