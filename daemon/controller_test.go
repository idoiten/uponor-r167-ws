package main

import (
	"encoding/hex"
	"encoding/json"
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
