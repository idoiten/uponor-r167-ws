package main

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Frame layout (payload, CRC already stripped), all frames start with
// the system id 14 FF 3C 1A:
//
//   [4] [5]
//   1F  80  len 36  I-167 -> R-167 "name frame": channel at [11], name from [16]
//   1F  86  len 49  I-167 -> R-167 record: [6]=01, address at [8]
//                   room: min [15], max [17], setpoint [23], temp [33], bitmask last 2
//                   system (address 0x22): average indoor [29], outdoor [33]
//   01  17  len 31  controller room header: room code [7], address [11],
//                   min [19], max [21], setpoint [27]
//   01  17  len 29  controller room data: temp [13], bitmask last 2 (no address!)
//   01  17  len 37  controller system data: average indoor [17], outdoor [21]
//
// Temperatures are 0.1 °F as a signed 16-bit big-endian value.
//
// The R-167 answers every name frame with
//   14 FF 3C 1A 1F 80 08 00 00 00 00 00 RQ 00 00
// where RQ = 0x80|channel asks the I-167 to send that channel's record
// (0x90 = system record), and answers every record with
//   14 FF 3C 1A 1F 86 01 00 00 00 00

var systemID = []byte{0x14, 0xFF, 0x3C, 0x1A}

const systemRecordAddr = 0x22

func tempC(b []byte, i int) float64 {
	raw := int16(uint16(b[i])<<8 | uint16(b[i+1]))
	c := (float64(raw)/10 - 32) / 1.8
	return math.Round(c*10) / 10
}

func u16(b []byte, i int) uint16 { return uint16(b[i])<<8 | uint16(b[i+1]) }

func latin1(b []byte) string {
	r := make([]rune, 0, len(b))
	for _, x := range b {
		if x == 0 {
			break
		}
		r = append(r, rune(x))
	}
	return string(r)
}

type Room struct {
	ID          string   `json:"id"` // controller address, hex
	Channel     int      `json:"channel"`
	Name        string   `json:"name"`
	Temperature *float64 `json:"temperature"`
	Setpoint    *float64 `json:"setpoint"`
	Min         *float64 `json:"min"`
	Max         *float64 `json:"max"`
	Bitmask     string   `json:"bitmask"`
	LastUpdate  string   `json:"last_update"`

	addr    byte
	bitmask uint16
}

type System struct {
	OutdoorTemperature *float64 `json:"outdoor_temperature"`
	AverageTemperature *float64 `json:"average_temperature"`
}

type Status struct {
	Version   string `json:"version"`
	RadioOK   bool   `json:"radio_ok"`
	LastFrame string `json:"last_frame"`
	Frames    int    `json:"frames"`
	Records   int    `json:"records"`
	Rejected  int    `json:"rejected_data_frames"`
}

type Snapshot struct {
	Status Status  `json:"status"`
	System System  `json:"system"`
	Rooms  []*Room `json:"rooms"`
}

type Controller struct {
	mu    sync.Mutex
	radio Transport
	log   func(string, ...any)
	pub   func(msgType string, data any)

	requestInterval time.Duration

	names     map[byte]string // channel -> name
	chanAddr  map[byte]byte   // channel -> address (learned from records)
	rooms     map[byte]*Room  // address -> room
	system    System
	lastFrame time.Time
	frames    int
	records   int
	rejected  int

	// last controller room header, for pairing with the next data frame
	hdrAddr byte
	hdrAt   time.Time

	// request scheduling
	queue       []byte
	pendingCh   byte
	pendingAt   time.Time
	lastRequest time.Time
	nextSystem  time.Time
	lastMissing byte
}

func NewController(radio Transport, logf func(string, ...any), pub func(string, any), reqInterval time.Duration) *Controller {
	return &Controller{
		radio:           radio,
		log:             logf,
		pub:             pub,
		requestInterval: reqInterval,
		names:           map[byte]string{},
		chanAddr:        map[byte]byte{},
		rooms:           map[byte]*Room{},
	}
}

func ptr(v float64) *float64 { return &v }

func same(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Handle processes one received payload.
func (c *Controller) Handle(p []byte) {
	if len(p) < 7 || string(p[:4]) != string(systemID) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	wasOK := c.radioOK(now)
	c.lastFrame = now
	c.frames++
	if !wasOK {
		c.pub("status", c.statusLocked(now))
	}

	switch {
	case p[4] == 0x1F && p[5] == 0x80 && len(p) == 36:
		c.onNameFrame(p, now)
	case p[4] == 0x1F && p[5] == 0x86 && len(p) == 49 && p[6] == 0x01:
		c.onRecord(p, now)
	case p[4] == 0x01 && p[5] == 0x17 && len(p) == 31 && p[6] == 0x00:
		c.hdrAddr, c.hdrAt = p[11], now
		c.updateRoom(p[11], nil, ptr(tempC(p, 27)), ptr(tempC(p, 19)), ptr(tempC(p, 21)), now)
	case p[4] == 0x01 && p[5] == 0x17 && len(p) == 29 && p[6] == 0x16:
		c.onData(p, now)
	case p[4] == 0x01 && p[5] == 0x17 && len(p) == 37 && p[6] == 0x1E:
		c.updateSystem(ptr(tempC(p, 17)), ptr(tempC(p, 21)))
	}
}

func (c *Controller) onNameFrame(p []byte, now time.Time) {
	ch := p[11]
	if ch != 0 {
		name := latin1(p[16:])
		if name != "" && c.names[ch] != name {
			c.names[ch] = name
			c.log("channel 0x%02X name %q", ch, name)
			if a, ok := c.chanAddr[ch]; ok {
				if r := c.rooms[a]; r != nil {
					r.Name = name
					c.pub("room", r)
				}
			}
		}
	}
	rq := c.nextRequest(now)
	if err := c.radio.Send([]byte{0x14, 0xFF, 0x3C, 0x1A, 0x1F, 0x80, 0x08, 0, 0, 0, 0, 0, rq, 0, 0}); err != nil {
		c.log("TX ack failed: %v", err)
	}
	if rq != 0 {
		c.log("requested record 0x%02X", rq)
	}
}

// nextRequest decides whether this acknowledgement should also ask for
// a record, and which one.
func (c *Controller) nextRequest(now time.Time) byte {
	if c.pendingCh != 0 && now.Sub(c.pendingAt) < 12*time.Second {
		return 0 // still waiting for an answer
	}
	c.pendingCh = 0
	if now.Sub(c.lastRequest) < c.requestInterval {
		return 0
	}
	if now.After(c.nextSystem) {
		c.nextSystem = now.Add(5 * time.Minute)
		c.lastRequest, c.pendingAt, c.pendingCh = now, now, 0x10
		return 0x90
	}
	// Rooms we have never received a record for go first.
	var missing []byte
	for ch := range c.names {
		if _, ok := c.chanAddr[ch]; !ok {
			missing = append(missing, ch)
		}
	}
	var ch byte
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		ch = missing[0]
		if c.lastMissing == ch && len(missing) > 1 {
			ch = missing[1] // don't get stuck on one room that never answers
		}
		c.lastMissing = ch
	} else {
		if len(c.queue) == 0 {
			for k := range c.names {
				c.queue = append(c.queue, k)
			}
			sort.Slice(c.queue, func(i, j int) bool { return c.queue[i] < c.queue[j] })
		}
		if len(c.queue) == 0 {
			return 0
		}
		ch = c.queue[0]
		c.queue = c.queue[1:]
	}
	c.lastRequest, c.pendingAt, c.pendingCh = now, now, ch
	return 0x80 | ch
}

func (c *Controller) onRecord(p []byte, now time.Time) {
	if err := c.radio.Send([]byte{0x14, 0xFF, 0x3C, 0x1A, 0x1F, 0x86, 0x01, 0x00, 0x00, 0x00, 0x00}); err != nil {
		c.log("TX record ack failed: %v", err)
	}
	c.records++
	addr := p[8]
	if addr == systemRecordAddr {
		if c.pendingCh == 0x10 {
			c.pendingCh = 0
		}
		c.updateSystem(ptr(tempC(p, 29)), ptr(tempC(p, 33)))
		return
	}
	// Which channel is this? Room addresses follow channel*21 - 304
	// (room code channel*21 - 296, minus 8), so a record can be matched
	// to its channel without relying on request/response timing.
	ch := byte(0)
	for k, v := range c.chanAddr {
		if v == addr {
			ch = k
		}
	}
	if ch == 0 {
		for k := range c.names {
			if expectedAddr(k) == addr {
				ch = k
				c.chanAddr[ch] = addr
			}
		}
	}
	if c.pendingCh != 0 && c.pendingCh != 0x10 && expectedAddr(c.pendingCh) == addr {
		c.pendingCh = 0
	}
	r := c.rooms[addr]
	if r == nil {
		if ch == 0 {
			c.log("record for unknown address 0x%02X ignored", addr)
			return
		}
		r = &Room{ID: fmt.Sprintf("%02x", addr), addr: addr, Channel: int(ch)}
		c.rooms[addr] = r
		c.log("new room 0x%02X on channel 0x%02X (%s)", addr, ch, c.names[ch])
	}
	bm := u16(p, len(p)-2)
	changed := false
	if r.bitmask != bm {
		r.bitmask, r.Bitmask, changed = bm, fmt.Sprintf("%04x", bm), true
	}
	if ch != 0 && r.Name != c.names[ch] {
		r.Name, changed = c.names[ch], true
	}
	if c.applyValues(r, ptr(tempC(p, 33)), ptr(tempC(p, 23)), ptr(tempC(p, 15)), ptr(tempC(p, 17)), now) {
		changed = true
	}
	if changed && r.Name != "" {
		c.pub("room", r)
	}
}

func (c *Controller) onData(p []byte, now time.Time) {
	// The data frame has no address. Only accept it if it directly
	// follows a header and its output bitmask matches that room.
	if now.Sub(c.hdrAt) > 2*time.Second {
		c.rejected++
		return
	}
	r := c.rooms[c.hdrAddr]
	bm := u16(p, len(p)-2)
	if r == nil || r.bitmask == 0 || r.bitmask != bm {
		c.rejected++
		return
	}
	c.hdrAt = time.Time{}
	c.updateRoom(r.addr, ptr(tempC(p, 13)), nil, nil, nil, now)
}

func (c *Controller) updateRoom(addr byte, temp, sp, min, max *float64, now time.Time) {
	r := c.rooms[addr]
	if r == nil {
		return // wait for its record (gives channel, name and bitmask)
	}
	if c.applyValues(r, temp, sp, min, max, now) && r.Name != "" {
		c.pub("room", r)
	}
}

func (c *Controller) applyValues(r *Room, temp, sp, min, max *float64, now time.Time) bool {
	changed := false
	set := func(dst **float64, v *float64, lo, hi float64) {
		if v == nil || *v < lo || *v > hi {
			return
		}
		if !same(*dst, v) {
			*dst = v
			changed = true
		}
	}
	set(&r.Temperature, temp, 0, 50)
	set(&r.Setpoint, sp, 5, 40)
	set(&r.Min, min, 5, 40)
	set(&r.Max, max, 5, 40)
	r.LastUpdate = now.UTC().Format(time.RFC3339)
	return changed
}

func (c *Controller) updateSystem(avg, outdoor *float64) {
	changed := false
	if avg != nil && *avg > 0 && *avg < 50 && !same(c.system.AverageTemperature, avg) {
		c.system.AverageTemperature, changed = avg, true
	}
	if outdoor != nil && *outdoor > -50 && *outdoor < 60 && !same(c.system.OutdoorTemperature, outdoor) {
		c.system.OutdoorTemperature, changed = outdoor, true
	}
	if changed {
		s := c.system
		c.pub("system", s)
	}
}

func (c *Controller) radioOK(now time.Time) bool {
	return !c.lastFrame.IsZero() && now.Sub(c.lastFrame) < 60*time.Second
}

func (c *Controller) statusLocked(now time.Time) Status {
	s := Status{Version: version, RadioOK: c.radioOK(now), Frames: c.frames, Records: c.records, Rejected: c.rejected}
	if !c.lastFrame.IsZero() {
		s.LastFrame = c.lastFrame.UTC().Format(time.RFC3339)
	}
	return s
}

// Watch publishes a status message when the radio goes silent.
func (c *Controller) Watch() {
	last := true
	for range time.Tick(10 * time.Second) {
		c.mu.Lock()
		now := time.Now()
		ok := c.radioOK(now)
		if ok != last {
			c.pub("status", c.statusLocked(now))
		}
		last = ok
		c.mu.Unlock()
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{Status: c.statusLocked(time.Now()), System: c.system, Rooms: []*Room{}}
	for _, r := range c.rooms {
		if r.Name != "" {
			cp := *r
			s.Rooms = append(s.Rooms, &cp)
		}
	}
	sort.Slice(s.Rooms, func(i, j int) bool { return s.Rooms[i].Channel < s.Rooms[j].Channel })
	return s
}

func expectedAddr(ch byte) byte { return byte(int(ch)*21 - 304) }
