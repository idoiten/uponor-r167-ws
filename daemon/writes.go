package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Writing a setpoint (as observed from the original firmware):
//
//  1. The R-167 sets byte [11] of its next name-frame acknowledgement to
//     0x80|channel: "I have a change for this channel".
//       14 FF 3C 1A 1F 80 08 00 00 00 00 92 00 00 00
//  2. The I-167 asks for it with a short frame naming the room address:
//       14 FF 3C 1A 1F 85 01 00 4A 00 08
//  3. The R-167 answers with the room's 12 settings bytes (as seen in the
//     controller's room header) followed by the new setpoint, the room's
//     ECO offset (register 3C; zero in this capture) and four zero bytes:
//       14 FF 3C 1A 1F 85 01 00 4A 00 08 88 00 00 64 02 4E 03 02 02 A8 03 14 02 F9 00 00 00 00 00 00
//  4. The controller broadcasts the room header with the new setpoint,
//     which is how the write is confirmed.

const (
	// The I-167 sometimes needs well over 10 s to pass a change on, and
	// flagging the write again restarts its work, so resend sparingly.
	writeTimeout     = 2 * time.Minute
	writeResendAfter = 30 * time.Second
	// A confirmation this long after the timeout is still recognised as
	// our write rather than logged as a change made on the system.
	writeLateWindow = 2 * time.Minute
)

type lateWrite struct {
	value float64
	until time.Time
}

type pendingWrite struct {
	addr      byte
	ch        byte
	raw       uint16
	value     float64
	created   time.Time
	flaggedAt time.Time
	sentAt    time.Time
	attempts  int
	done      func(error)
	// eco, when set, makes this an ECO write: the current setpoint is sent
	// with the ECO bit (register 35 0x0008) set or cleared in the room's
	// settings block. Experimental, see SetEco.
	eco *bool
}

// ecoBit is "ECO commanded for this room" in register 35 (low byte of the
// first settings word), set by the I-167 when the system goes to Away.
const ecoBit = 0x08

func (w *pendingWrite) what() string {
	if w.eco != nil {
		if *w.eco {
			return "ECO on"
		}
		return "ECO off"
	}
	return fmt.Sprintf("setpoint %.1f", w.value)
}

// cToRaw converts °C to the system's 0.1 °F encoding.
func cToRaw(c float64) uint16 {
	return uint16(int16(math.Round((c*1.8 + 32) * 10)))
}

// SetSetpoint queues a setpoint change for a room (id = address in hex).
// done is called exactly once, with nil when the controller confirms the
// new value, or with an error. It may be called with the controller lock
// held, so it must not block.
func (c *Controller) SetSetpoint(id string, value float64, done func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, err := strconv.ParseUint(id, 16, 8)
	if err != nil {
		done(fmt.Errorf("invalid room id %q", id))
		return
	}
	addr := byte(a)
	r := c.rooms[addr]
	if r == nil {
		done(fmt.Errorf("unknown room %q", id))
		return
	}
	if len(r.block) != 12 || r.ecoOffset == nil {
		done(errors.New("room settings not received yet, try again shortly"))
		return
	}
	value = math.Round(value*2) / 2
	if r.Min != nil && value < *r.Min || r.Max != nil && value > *r.Max {
		done(fmt.Errorf("%.1f °C is outside the room's limits", value))
		return
	}
	ch := byte(r.Channel)
	if old := c.writes[addr]; old != nil {
		old.done(errors.New("superseded by a newer setpoint"))
	}
	c.writes[addr] = &pendingWrite{addr: addr, ch: ch, raw: cToRaw(value), value: value, created: time.Now(), done: done}
	c.log("setpoint %.1f queued for %s (0x%02X)", value, r.Name, addr)
}

// SetEco asks the I-167 to put a room in (or take it out of) ECO by
// writing its current setpoint with the ECO bit of register 35 changed –
// the same write the I-167 accepts for setpoint changes. Experimental:
// whether the I-167 honours the bit is what this is for finding out.
// done is called like for SetSetpoint; success means the controller now
// reports the requested ECO bit for the room.
func (c *Controller) SetEco(id string, on bool, done func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, err := strconv.ParseUint(id, 16, 8)
	if err != nil {
		done(fmt.Errorf("invalid room id %q", id))
		return
	}
	addr := byte(a)
	r := c.rooms[addr]
	if r == nil {
		done(fmt.Errorf("unknown room %q", id))
		return
	}
	if len(r.block) != 12 || r.Setpoint == nil || r.ecoOffset == nil {
		done(errors.New("room settings not received yet, try again shortly"))
		return
	}
	if old := c.writes[addr]; old != nil {
		old.done(errors.New("superseded by a newer write"))
	}
	c.writes[addr] = &pendingWrite{addr: addr, ch: byte(r.Channel), raw: cToRaw(*r.Setpoint), value: *r.Setpoint,
		created: time.Now(), done: done, eco: &on}
	c.log("ECO %v queued for %s (0x%02X)", map[bool]string{true: "on", false: "off"}[on], r.Name, addr)
}

// writeFlag returns the value for byte [11] of the next acknowledgement:
// 0x80|channel when a write is waiting to be picked up, otherwise 0.
func (c *Controller) writeFlag(now time.Time) byte {
	var next *pendingWrite
	for addr, w := range c.writes {
		if now.Sub(w.created) > writeTimeout {
			c.log("%s write for 0x%02X timed out", w.what(), addr)
			w.done(errors.New("the heating system did not confirm the change"))
			delete(c.writes, addr)
			if w.eco == nil {
				c.lateWrites[addr] = lateWrite{value: w.value, until: now.Add(writeLateWindow)}
			}
			continue
		}
		if !w.sentAt.IsZero() && now.Sub(w.sentAt) < writeResendAfter {
			continue // sent, waiting for confirmation
		}
		if next == nil || w.created.Before(next.created) {
			next = w
		}
	}
	if next == nil {
		return 0
	}
	next.flaggedAt = now
	return 0x80 | next.ch
}

// onWriteQuery answers the I-167's request for a pending change.
func (c *Controller) onWriteQuery(p []byte, now time.Time) {
	addr := p[8]
	w := c.writes[addr]
	r := c.rooms[addr]
	if w == nil || r == nil || len(r.block) != 12 || r.ecoOffset == nil {
		c.log("write query for 0x%02X without a pending change, ignored", addr)
		return
	}
	frame := []byte{0x14, 0xFF, 0x3C, 0x1A, 0x1F, 0x85, 0x01, 0x00, addr, 0x00, 0x08}
	block := append([]byte{}, r.block...)
	if w.eco != nil {
		if *w.eco {
			block[1] |= ecoBit
		} else {
			block[1] &^= ecoBit
		}
	}
	frame = append(frame, block...)
	// setpoint (3B), then the ECO offset (3C) as the room has it – sending
	// zero here resets the offset – then four bytes left at zero as the
	// original firmware did
	off := *r.ecoOffset
	frame = append(frame, byte(w.raw>>8), byte(w.raw), byte(off>>8), byte(off), 0, 0, 0, 0)
	if err := c.radio.Send(frame); err != nil {
		c.log("TX %s failed: %v", w.what(), err)
		return
	}
	w.sentAt = now
	w.attempts++
	c.log("sent %s to %s (0x%02X), attempt %d", w.what(), r.Name, addr, w.attempts)
}

// confirmWrite completes a pending write when the system reports the
// requested setpoint for that room.
func (c *Controller) confirmWrite(addr byte, raw uint16) {
	w := c.writes[addr]
	if w == nil || w.sentAt.IsZero() || raw != w.raw {
		return
	}
	if w.eco != nil {
		r := c.rooms[addr]
		if r == nil || len(r.block) < 2 || (r.block[1]&ecoBit != 0) != *w.eco {
			return
		}
	}
	delete(c.lateWrites, addr)
	c.log("%s confirmed for 0x%02X", w.what(), addr)
	if r := c.rooms[addr]; r != nil {
		r.ownSetpoint = ptr(w.value)
	}
	delete(c.writes, addr)
	w.done(nil)
}

// lateConfirm reports whether a setpoint change is a write of ours that
// was confirmed after it had timed out.
func (c *Controller) lateConfirm(addr byte, value float64, now time.Time) bool {
	lw, ok := c.lateWrites[addr]
	if !ok {
		return false
	}
	delete(c.lateWrites, addr)
	if now.After(lw.until) || lw.value != value {
		return false
	}
	c.log("setpoint %.1f confirmed for 0x%02X after the write had timed out", value, addr)
	return true
}
