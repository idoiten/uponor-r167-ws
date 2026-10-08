package main

import (
	"errors"
	"time"
)

// Switching ECO mode (Home/Away on the I-167), as observed from the
// original firmware on 2026-10-08:
//
//  1. The R-167 sets byte [11] of its next name-frame acknowledgement to
//     0x80 (channel 0: the system rather than a room).
//  2. The I-167 asks for it:
//       14 FF 3C 1A 1F 85 00 00 00 00 0E
//  3. The R-167 answers with the same eleven bytes, the I-167's system
//     registers as last broadcast in its FF 17 frame (bytes 15-42) with
//     the ECO bit (0x0800 of the first register) set or cleared, and four
//     zero bytes.
//  4. The I-167's next FF 17 frame shows the new state, which confirms it.

type pendingEco struct {
	want     bool
	created  time.Time
	sentAt   time.Time
	attempts int
	done     func(error)
}

// SetEcoMode queues an ECO mode change. done is called exactly once, with
// nil when the I-167 reports the new state, or with an error. It may be
// called with the controller lock held, so it must not block.
func (c *Controller) SetEcoMode(on bool, done func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tsRegs) != 28 {
		done(errors.New("the I-167 has not been heard yet, try again shortly"))
		return
	}
	if c.ecoWrite != nil {
		c.ecoWrite.done(errors.New("superseded by a newer ECO mode change"))
		c.ecoWrite = nil
	}
	if c.system.EcoMode != nil && *c.system.EcoMode == on {
		done(nil)
		return
	}
	c.ecoWrite = &pendingEco{want: on, created: time.Now(), done: done}
	c.log("ECO mode %v queued", on)
}

// ecoFlag returns 0x80 when an ECO mode change should be flagged in the
// next acknowledgement. Room setpoints go first.
func (c *Controller) ecoFlag(now time.Time) byte {
	e := c.ecoWrite
	if e == nil {
		return 0
	}
	if now.Sub(e.created) > writeTimeout {
		c.log("ECO mode change timed out")
		c.ecoWrite = nil
		e.done(errors.New("the heating system did not confirm the ECO mode change"))
		return 0
	}
	if !e.sentAt.IsZero() && now.Sub(e.sentAt) < writeResendAfter {
		return 0
	}
	return 0x80
}

// onSystemWriteQuery answers the I-167's request for a system change.
func (c *Controller) onSystemWriteQuery(p []byte, now time.Time) {
	e := c.ecoWrite
	if e == nil || len(c.tsRegs) != 28 || p[8] != 0x00 || p[10] != 0x0E {
		c.log("system write query without a pending change, ignored: % X", p)
		return
	}
	regs := append([]byte{}, c.tsRegs...)
	if e.want {
		regs[0] |= tsEcoModeBit >> 8
	} else {
		regs[0] &^= tsEcoModeBit >> 8
	}
	frame := append([]byte{0x14, 0xFF, 0x3C, 0x1A, 0x1F, 0x85, 0x00, 0x00, 0x00, 0x00, 0x0E}, regs...)
	frame = append(frame, 0, 0, 0, 0)
	if err := c.radio.Send(frame); err != nil {
		c.log("TX ECO mode failed: %v", err)
		return
	}
	e.sentAt = now
	e.attempts++
	c.log("sent ECO mode %v, attempt %d", e.want, e.attempts)
}

// confirmEco completes a pending ECO mode change when the I-167 reports it.
func (c *Controller) confirmEco(on bool) {
	e := c.ecoWrite
	if e == nil || e.sentAt.IsZero() || on != e.want {
		return
	}
	c.ecoWrite = nil
	c.log("ECO mode %v confirmed", on)
	e.done(nil)
}
