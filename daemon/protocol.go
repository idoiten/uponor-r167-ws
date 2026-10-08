package main

import (
	"fmt"
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
//   1F  85  len 11  I-167 -> R-167: "send your change for address [8]" (see writes.go)
//
// Bypass is bit 0x01 of byte [16] in the room header and of byte [12]
// in the record (matches the rooms with bypass enabled on the I-167).
//
// The room-in-demand ("heating") flag is bit 0x40 of byte [8] in the
// data frame and of byte [28] in the record.
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
	return rawToC(int16(uint16(b[i])<<8 | uint16(b[i+1])))
}

// rawToC converts the system's 0.1 °F encoding to °C with one decimal,
// truncated toward zero like the I-167 display does (73.0 °F = 22.78 °C
// shows as 22.7). Integer arithmetic, so float error cannot move a value
// across a tenth. Setpoints in half degrees are exact either way.
func rawToC(raw int16) float64 {
	tenths := (int(raw) - 320) * 5 / 9 // Go division truncates toward zero
	return float64(tenths) / 10
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
	Heating     *bool    `json:"heating"`
	Bypass      *bool    `json:"bypass"`
	// RadioAlarm is set when the controller has lost contact with the
	// room's thermostat (I-167: "Term. saknas"), about an hour after the
	// thermostat went silent. Register 3E bit 0x0020.
	RadioAlarm *bool `json:"radio_alarm"`
	// Raw controller registers, hex: 3D status (heating demand, limits,
	// ECO), 3E alarms (technical, tamper, RF, battery, RH sensor),
	// 3F thermostat type / regulation mode. Mapped so far: 3D 0x0040
	// heating demand, 3D 0x0200 room has an active alarm, 3E 0x0020
	// radio alarm, 3F 0x0800 thermostat just started.
	Registers  map[string]string `json:"registers"`
	LastUpdate string            `json:"last_update"`

	addr    byte
	bitmask uint16
	block   []byte // 12 settings bytes echoed back when writing a setpoint
	// setpoint we just wrote ourselves, so its echo is not logged as external
	ownSetpoint *float64
	published   time.Time
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
	Device Device  `json:"device"`
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
	device    Device
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

	// setpoint writes waiting to be sent or confirmed, by room address
	writes map[byte]*pendingWrite
	// writes that timed out recently, so a late confirmation is still
	// recognised as ours
	lateWrites map[byte]lateWrite
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
		writes:          map[byte]*pendingWrite{},
		lateWrites:      map[byte]lateWrite{},
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
	case p[4] == 0x1F && p[5] == 0x85 && len(p) == 11 && p[6] == 0x01:
		c.onWriteQuery(p, now)
	case p[4] == 0x01 && p[5] == 0x17 && len(p) == 31 && p[6] == 0x00:
		c.hdrAddr, c.hdrAt = p[11], now
		bypassChanged := false
		if r := c.rooms[p[11]]; r != nil {
			r.block = append([]byte{}, p[15:27]...)
			bypassChanged = setFlag(&r.Bypass, p[16]&0x01 != 0)
		}
		c.confirmWrite(p[11], u16(p, 27))
		c.updateRoom(p[11], nil, ptr(tempC(p, 27)), ptr(tempC(p, 19)), ptr(tempC(p, 21)), bypassChanged, now)
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
	// A pending setpoint change takes priority; never combine it with a
	// record request in the same acknowledgement.
	wf := c.writeFlag(now)
	rq := byte(0)
	if wf == 0 {
		rq = c.nextRequest(now)
	}
	if err := c.radio.Send([]byte{0x14, 0xFF, 0x3C, 0x1A, 0x1F, 0x80, 0x08, 0, 0, 0, 0, wf, rq, 0, 0}); err != nil {
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
	r.block = append([]byte{}, p[11:23]...)
	c.confirmWrite(addr, u16(p, 23))
	changed := setHeating(r, p[28]&0x40 != 0)
	if c.setRegisters(r, u16(p, 27), u16(p, 29), u16(p, 31)) {
		changed = true
	}
	if setFlag(&r.Bypass, p[12]&0x01 != 0) {
		changed = true
	}
	if r.bitmask != bm {
		r.bitmask, r.Bitmask, changed = bm, fmt.Sprintf("%04x", bm), true
	}
	if ch != 0 && r.Name != c.names[ch] {
		r.Name, changed = c.names[ch], true
	}
	if c.applyValues(r, ptr(tempC(p, 33)), ptr(tempC(p, 23)), ptr(tempC(p, 15)), ptr(tempC(p, 17)), now) {
		changed = true
	}
	if r.Name != "" {
		c.publishRoom(r, changed, now)
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
	heatingChanged := setHeating(r, p[8]&0x40 != 0)
	regsChanged := c.setRegisters(r, u16(p, 7), u16(p, 9), u16(p, 11))
	changed := c.applyValues(r, ptr(tempC(p, 13)), nil, nil, nil, now) || heatingChanged || regsChanged
	if r.Name != "" {
		c.publishRoom(r, changed, now)
	}
}

func (c *Controller) updateRoom(addr byte, temp, sp, min, max *float64, force bool, now time.Time) {
	r := c.rooms[addr]
	if r == nil {
		return // wait for its record (gives channel, name and bitmask)
	}
	changed := c.applyValues(r, temp, sp, min, max, now) || force
	if r.Name != "" {
		c.publishRoom(r, changed, now)
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
	if sp != nil && outsideLimits(r, *sp, min, max) {
		c.log("ignored setpoint %.1f for %s: outside the room's limits", *sp, r.Name)
		sp = nil
	}
	if sp != nil && r.Setpoint != nil && *sp != *r.Setpoint && *sp >= 5 && *sp <= 40 {
		if r.ownSetpoint != nil && *r.ownSetpoint == *sp {
			r.ownSetpoint = nil // our own write, already logged as confirmed
		} else if c.lateConfirm(r.addr, *sp, now) {
			// our write, confirmed after we had given up on it
		} else {
			c.log("setpoint for %s changed from %.1f to %.1f (changed on the system, e.g. I-167 or thermostat)", r.Name, *r.Setpoint, *sp)
		}
	}
	set(&r.Setpoint, sp, 5, 40)
	set(&r.Min, min, 5, 40)
	set(&r.Max, max, 5, 40)
	r.LastUpdate = now.UTC().Format(time.RFC3339)
	return changed
}

// outsideLimits reports a setpoint outside the room's min/max. The limits
// already known for the room are used first, so a frame carrying bogus
// values (seen while the I-167 restarts: 30 °C with a 25 °C maximum)
// cannot vouch for itself; a raised maximum is stored from this frame and
// accepted from the next one.
func outsideLimits(r *Room, sp float64, min, max *float64) bool {
	lo, hi := r.Min, r.Max
	if lo == nil {
		lo = min
	}
	if hi == nil {
		hi = max
	}
	return lo != nil && sp < *lo || hi != nil && sp > *hi
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

// Watch publishes a status message when the radio goes silent, and a
// "stats" message with the current counters every 10 s while frames
// keep arriving. The web page uses "stats" to keep its footer current;
// the HA integration ignores it, so its diagnostic attributes do not
// change (and get recorded) every 10 s.
func (c *Controller) Watch() {
	last, lastFrames := true, -1
	for range time.Tick(10 * time.Second) {
		c.mu.Lock()
		now := time.Now()
		ok := c.radioOK(now)
		if ok != last {
			c.pub("status", c.statusLocked(now))
		}
		last = ok
		if c.frames != lastFrames {
			c.pub("stats", c.statusLocked(now))
			lastFrames = c.frames
		}
		c.mu.Unlock()
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{Status: c.statusLocked(time.Now()), System: c.system, Device: c.device, Rooms: []*Room{}}
	for _, r := range c.rooms {
		if r.Name != "" {
			cp := *r
			cp.Registers = map[string]string{}
			for k, v := range r.Registers {
				cp.Registers[k] = v
			}
			s.Rooms = append(s.Rooms, &cp)
		}
	}
	sort.Slice(s.Rooms, func(i, j int) bool { return s.Rooms[i].Channel < s.Rooms[j].Channel })
	return s
}

func expectedAddr(ch byte) byte { return byte(int(ch)*21 - 304) }

// setHeating updates the "room in demand" flag; returns true if changed.
func setHeating(r *Room, on bool) bool { return setFlag(&r.Heating, on) }

// setFlag updates an optional boolean; returns true if it changed.
func setFlag(dst **bool, on bool) bool {
	if *dst != nil && **dst == on {
		return false
	}
	*dst = &on
	return true
}

// publishRoom sends a room to clients when it changed, and otherwise at
// least once a minute so "last update" reflects when data last arrived.
func (c *Controller) publishRoom(r *Room, changed bool, now time.Time) {
	if !changed && now.Sub(r.published) < time.Minute {
		return
	}
	r.published = now
	c.pub("room", r)
}

var registerNames = []string{"3d", "3e", "3f"}

// radioAlarmBit in register 3E: the controller has lost the thermostat.
const radioAlarmBit = 0x0020

var registerLabels = map[string]string{"3d": "status", "3e": "alarm", "3f": "type"}

// setRegisters stores the raw 3D/3E/3F registers and logs every change,
// so unknown bits (alarms in particular) can be mapped by provoking them.
func (c *Controller) setRegisters(r *Room, vals ...uint16) bool {
	if r.Registers == nil {
		r.Registers = map[string]string{}
	}
	room := r.Name
	if room == "" {
		room = "room " + r.ID
	}
	changed := false
	for i, name := range registerNames {
		v := fmt.Sprintf("%04x", vals[i])
		old, known := r.Registers[name]
		if known && old == v {
			continue
		}
		r.Registers[name] = v
		changed = true
		if known {
			c.log("%s register (%s) for %s changed %s -> %s", registerLabels[name], name, room, old, v)
		} else if name == "3e" && vals[i] != 0 {
			c.log("alarm register (3e) for %s is %s at start", room, v)
		}
	}
	alarm := vals[1]&radioAlarmBit != 0
	if (r.RadioAlarm == nil && alarm) || (r.RadioAlarm != nil && *r.RadioAlarm != alarm) {
		if alarm {
			c.log("radio alarm for %s: the controller has lost contact with the thermostat", room)
		} else if r.RadioAlarm != nil {
			c.log("radio alarm for %s cleared", room)
		}
	}
	if setFlag(&r.RadioAlarm, alarm) {
		changed = true
	}
	return changed
}
