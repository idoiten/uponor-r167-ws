package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRadio answers record requests with records captured from the real
// system, and records everything that would have been transmitted.
type fakeRadio struct {
	mu      sync.Mutex
	records map[byte][]byte // address -> record payload
	sent    [][]byte
	pending [][]byte
}

var chanToAddr = map[byte]byte{0x11: 0x35, 0x12: 0x4A, 0x14: 0x74, 0x15: 0x89, 0x16: 0x9E, 0x17: 0xB3, 0x18: 0xC8, 0x19: 0xDD, 0x1A: 0xF2, 0x10: 0x22}

func (f *fakeRadio) Run(func([]byte)) {}

func (f *fakeRadio) Send(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, append([]byte{}, p...))
	if len(p) == 15 && p[4] == 0x1F && p[5] == 0x80 && p[12] != 0 {
		if rec, ok := f.records[chanToAddr[p[12]&0x7F]]; ok {
			f.pending = append(f.pending, rec)
		}
	}
	return nil
}

func (f *fakeRadio) take() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pending
	f.pending = nil
	return p
}

func loadRecords(t *testing.T) map[byte][]byte {
	b, err := os.ReadFile("testdata_records.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	json.Unmarshal(b, &m)
	out := map[byte][]byte{}
	for k, v := range m {
		a, _ := hex.DecodeString(k)
		p, _ := hex.DecodeString(v)
		out[a[0]] = p
	}
	return out
}

func TestControllerReplay(t *testing.T) {
	data, err := os.ReadFile("testdata_probe.bin")
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRadio{records: loadRecords(t)}
	var msgs []string
	pub := func(typ string, d any) { msgs = append(msgs, string(encode(typ, d))) }
	c := NewController(fr, t.Logf, pub, 0)

	var framer Framer
	frames := framer.Push(data, true)
	if framer.Dropped != 0 {
		t.Fatalf("framer dropped %d bytes", framer.Dropped)
	}
	for _, p := range frames {
		c.Handle(p)
		for _, rec := range fr.take() {
			c.Handle(rec)
		}
		time.Sleep(time.Millisecond)
	}
	snap := c.Snapshot()
	b, _ := json.MarshalIndent(snap, "", " ")
	t.Logf("snapshot:\n%s", b)
	if len(snap.Rooms) != 9 {
		t.Errorf("expected 9 rooms, got %d", len(snap.Rooms))
	}
	for _, r := range snap.Rooms {
		if r.Temperature == nil || r.Setpoint == nil || r.Name == "" {
			t.Errorf("incomplete room %+v", r)
		}
	}
	if snap.System.OutdoorTemperature == nil || snap.System.AverageTemperature == nil {
		t.Errorf("system temperatures missing")
	}
	acks, reqs := 0, 0
	for _, s := range fr.sent {
		if len(s) == 15 {
			acks++
			if s[12] != 0 {
				reqs++
			}
		}
	}
	t.Logf("sent %d acks (%d with requests), %d messages published, rejected data frames %d", acks, reqs, len(msgs), snap.Status.Rejected)
	for _, m := range msgs {
		if strings.Contains(m, `"type":"room"`) && strings.Contains(m, `"name":""`) {
			t.Errorf("room published without name: %s", m)
		}
	}
}

func TestWrongPairingRejected(t *testing.T) {
	fr := &fakeRadio{records: loadRecords(t)}
	c := NewController(fr, t.Logf, func(string, any) {}, 0)
	// Learn two rooms from their records.
	c.names[0x12], c.names[0x14] = "K-E-V", "Sovrum 2"
	c.Handle(fr.records[0x4A])
	c.Handle(fr.records[0x74])
	before := *c.rooms[0x4A].Temperature

	// Header for K-E-V followed by Sovrum 2's data frame (bitmask 0008):
	// exactly the mix-up the original firmware makes. Must be rejected.
	header := mustHex("14 FF 3C 1A 01 17 00 52 00 0B 00 4A 00 08 10 88 00 00 64 02 4E 03 02 02 A8 03 14 03 02 00 00")
	data := mustHex("14 FF 3C 1A 01 17 16 00 41 80 00 04 00 02 F0 7F FF 90 00 00 00 00 00 00 00 00 00 00 08")
	c.Handle(header)
	c.Handle(data)
	if *c.rooms[0x4A].Temperature != before {
		t.Fatalf("K-E-V temperature changed from %.1f to %.1f by another room's data frame", before, *c.rooms[0x4A].Temperature)
	}
	if c.rejected != 1 {
		t.Fatalf("expected 1 rejected frame, got %d", c.rejected)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

func TestSetpointWrite(t *testing.T) {
	fr := &fakeRadio{records: loadRecords(t)}
	c := NewController(fr, t.Logf, func(string, any) {}, time.Hour)
	c.names[0x12] = "K-E-V"
	c.Handle(fr.records[0x4A])
	c.nextSystem = time.Now().Add(time.Hour)

	var result error = errors.New("not called")
	c.SetSetpoint("4a", 24.5, func(err error) { result = err })

	// 1. The next name-frame ack must flag channel 0x12 in byte [11].
	name := mustHex("14 FF 3C 1A 1F 80 1D 00 00 00 00 11 00 00 00 00 4B 6C E4 64 76 E5 72 64 00 03 14 03 02 00 00 41 28 12 CE 00")
	c.Handle(name)
	ack := fr.sent[len(fr.sent)-1]
	if want := mustHex("14 FF 3C 1A 1F 80 08 00 00 00 00 92 00 00 00"); string(ack) != string(want) {
		t.Fatalf("ack = % X, want % X", ack, want)
	}

	// 2-3. The I-167 asks; we must answer exactly like the original firmware did.
	c.Handle(mustHex("14 FF 3C 1A 1F 85 01 00 4A 00 08"))
	got := fr.sent[len(fr.sent)-1]
	want := mustHex("14 FF 3C 1A 1F 85 01 00 4A 00 08 88 00 00 64 02 4E 03 02 02 A8 03 14 02 F9 00 00 00 00 00 00")
	if string(got) != string(want) {
		t.Fatalf("setpoint frame\n got % X\nwant % X", got, want)
	}
	if crc := withCRC(got); crc[len(crc)-2] != 0xBB || crc[len(crc)-1] != 0xA3 {
		t.Fatalf("CRC % X, captured frame had BB A3", crc[len(crc)-2:])
	}
	if result == nil {
		t.Fatal("confirmed too early")
	}

	// 4. The controller's header with the new setpoint confirms it.
	c.Handle(mustHex("14 FF 3C 1A 01 17 00 52 00 0B 00 4A 00 08 10 88 00 00 64 02 4E 03 02 02 A8 03 14 02 F9 00 00"))
	if result != nil {
		t.Fatalf("write not confirmed: %v", result)
	}
	if *c.rooms[0x4A].Setpoint != 24.5 {
		t.Fatalf("setpoint = %v", *c.rooms[0x4A].Setpoint)
	}
	if len(c.writes) != 0 {
		t.Fatal("pending write not cleared")
	}
}

func TestSetpointValidation(t *testing.T) {
	fr := &fakeRadio{records: loadRecords(t)}
	c := NewController(fr, t.Logf, func(string, any) {}, time.Hour)
	c.names[0x12] = "K-E-V"
	c.Handle(fr.records[0x4A])
	for _, tc := range []struct {
		id string
		v  float64
	}{{"4a", 30}, {"4a", 10}, {"99", 22}, {"zz", 22}} {
		var got error
		c.SetSetpoint(tc.id, tc.v, func(err error) { got = err })
		if got == nil {
			t.Errorf("%s %.1f: expected an error", tc.id, tc.v)
		}
	}
}

func TestHeatingFlag(t *testing.T) {
	data, _ := os.ReadFile("testdata_probe.bin")
	fr := &fakeRadio{records: loadRecords(t)}
	c := NewController(fr, t.Logf, func(string, any) {}, 0)
	var framer Framer
	for _, p := range framer.Push(data, true) {
		c.Handle(p)
		for _, rec := range fr.take() {
			c.Handle(rec)
		}
	}
	// From Home Assistant at the time: Klädvård and Sovrum 1 idle, the rest heating.
	want := map[byte]bool{0x35: false, 0x9E: false, 0x4A: true, 0x74: true, 0x89: true, 0xB3: true, 0xC8: true, 0xDD: true, 0xF2: true}
	for addr, w := range want {
		r := c.rooms[addr]
		if r == nil || r.Heating == nil || *r.Heating != w {
			t.Errorf("room 0x%02X heating = %v, want %v", addr, r.Heating, w)
		}
	}
}

func TestExternalSetpointLogged(t *testing.T) {
	fr := &fakeRadio{records: loadRecords(t)}
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	c := NewController(fr, logf, func(string, any) {}, time.Hour)
	c.names[0x12] = "K-E-V"
	c.Handle(fr.records[0x4A])
	c.nextSystem = time.Now().Add(time.Hour)

	// Our own write: confirmed, but not reported as an external change.
	c.SetSetpoint("4a", 24.5, func(error) {})
	c.Handle(mustHex("14 FF 3C 1A 1F 80 1D 00 00 00 00 12 00 00 00 00 4B 2D 45 2D 56 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00"))
	c.Handle(mustHex("14 FF 3C 1A 1F 85 01 00 4A 00 08"))
	c.Handle(mustHex("14 FF 3C 1A 01 17 00 52 00 0B 00 4A 00 08 10 88 00 00 64 02 4E 03 02 02 A8 03 14 02 F9 00 00"))
	for _, l := range logs {
		if strings.Contains(l, "changed on the system") {
			t.Fatalf("own write logged as external: %s", l)
		}
	}
	// Someone changes it on the I-167: 22.0 °C (0x02CC).
	c.Handle(mustHex("14 FF 3C 1A 01 17 00 52 00 0B 00 4A 00 08 10 88 00 00 64 02 4E 03 02 02 A8 03 14 02 CC 00 00"))
	found := false
	for _, l := range logs {
		if strings.Contains(l, "setpoint for K-E-V changed from 24.5 to 22.0") {
			found = true
		}
	}
	if !found {
		t.Fatalf("external change not logged: %v", logs)
	}
}

func TestBypassFlag(t *testing.T) {
	data, _ := os.ReadFile("testdata_probe.bin")
	fr := &fakeRadio{records: loadRecords(t)}
	c := NewController(fr, t.Logf, func(string, any) {}, 0)
	var framer Framer
	for _, p := range framer.Push(data, true) {
		c.Handle(p)
		for _, rec := range fr.take() {
			c.Handle(rec)
		}
	}
	// Bypass is enabled for WC and Badrum only (matches the I-167 and RTL-SDR).
	for addr, r := range c.rooms {
		want := addr == 0x89 || addr == 0xB3
		if r.Bypass == nil || *r.Bypass != want {
			t.Errorf("%s (0x%02X) bypass = %v, want %v", r.Name, addr, r.Bypass, want)
		}
	}
}

func TestAlarmRegisterLogged(t *testing.T) {
	fr := &fakeRadio{records: loadRecords(t)}
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	c := NewController(fr, logf, func(string, any) {}, time.Hour)
	c.names[0x12], c.names[0x14] = "K-E-V", "Sovrum 2"
	c.Handle(fr.records[0x4A])
	c.Handle(fr.records[0x74])
	if got := c.rooms[0x74].Registers["3e"]; got != "8000" {
		t.Fatalf("Sovrum 2 alarm register = %q, want 8000", got)
	}
	// K-E-V header + data frame with a (made-up) alarm bit 0x0020 in 3E.
	c.Handle(mustHex("14 FF 3C 1A 01 17 00 52 00 0B 00 4A 00 08 10 88 00 00 64 02 4E 03 02 02 A8 03 14 03 02 00 00"))
	c.Handle(mustHex("14 FF 3C 1A 01 17 16 00 41 00 20 04 00 02 D0 7F FF 90 00 00 00 00 00 00 00 00 00 04 06"))
	want := "alarm register (3e) for K-E-V changed 0000 -> 0020"
	found := false
	for _, l := range logs {
		if l == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing %q in %v", want, logs)
	}
	if c.Snapshot().Rooms[0].Registers == nil {
		t.Fatal("registers missing from snapshot")
	}
}

func TestRawToCTruncates(t *testing.T) {
	cases := []struct {
		raw  int16
		want float64
	}{
		{730, 22.7},  // 22.78 °C, the I-167 shows 22.7
		{731, 22.8},  // 22.83
		{713, 21.8},  // 21.83
		{714, 21.8},  // 21.89
		{770, 25.0},  // setpoint 25.0, exact
		{761, 24.5},  // setpoint 24.5, exact
		{590, 15.0},  // limit 15.0, exact
		{320, 0.0},   // 32.0 °F
		{310, -0.5},  // 31.0 °F = -0.56 °C
		{140, -10.0}, // 14.0 °F, exact
		{139, -10.0}, // 13.9 °F = -10.06 °C
	}
	for _, c := range cases {
		if got := rawToC(c.raw); got != c.want {
			t.Errorf("rawToC(%d) = %v, want %v", c.raw, got, c.want)
		}
	}
	// every half degree used for setpoints round-trips exactly
	for v := 5.0; v <= 35.0; v += 0.5 {
		if got := rawToC(int16(cToRaw(v))); got != v {
			t.Errorf("setpoint %.1f round-trips to %v", v, got)
		}
	}
}
