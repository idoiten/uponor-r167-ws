package main

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The R-167's internal 868 MHz radio modem sits on /dev/ttyLP2 at
// 38400 8N1. Each frame on the serial line is the radio frame's payload
// (starting with the system id 14 FF 3C 1A) followed by a CRC-16/MODBUS,
// little-endian. There is no length byte; frames are delimited by
// finding the shortest prefix whose CRC matches.

const maxFrameLen = 96

func crc16modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func withCRC(payload []byte) []byte {
	c := crc16modbus(payload)
	return append(append([]byte{}, payload...), byte(c), byte(c>>8))
}

func validFrame(f []byte) bool {
	if len(f) < 8 {
		return false
	}
	c := crc16modbus(f[:len(f)-2])
	return f[len(f)-2] == byte(c) && f[len(f)-1] == byte(c>>8)
}

// Framer turns a byte stream into CRC-valid frames (payload without CRC).
type Framer struct {
	buf []byte
	// Dropped counts bytes that could not be framed.
	Dropped int
}

// Push adds bytes; idle=true means the line has been silent, so an
// incomplete frame at the start of the buffer can be discarded.
func (f *Framer) Push(data []byte, idle bool) [][]byte {
	f.buf = append(f.buf, data...)
	var out [][]byte
	for len(f.buf) >= 8 {
		found := 0
		for l := 8; l <= len(f.buf) && l <= maxFrameLen; l++ {
			if validFrame(f.buf[:l]) {
				found = l
				break
			}
		}
		if found == 0 {
			if len(f.buf) >= maxFrameLen || idle {
				// No frame starts here: skip one byte and resync.
				f.Dropped++
				f.buf = f.buf[1:]
				continue
			}
			break // probably an incomplete frame; wait for more bytes
		}
		out = append(out, append([]byte{}, f.buf[:found-2]...))
		f.buf = f.buf[found:]
	}
	if idle && len(f.buf) > 0 && len(f.buf) < 8 {
		f.Dropped += len(f.buf)
		f.buf = f.buf[:0]
	}
	return out
}

// Radio is the serial connection to the modem.
type Radio struct {
	fd  int
	mu  sync.Mutex
	log func(string, ...any)
}

func setRaw(fd int) error {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL | syscall.B38400
	t.Ispeed = syscall.B38400
	t.Ospeed = syscall.B38400
	t.Cc[syscall.VMIN] = 0
	t.Cc[syscall.VTIME] = 1 // a read returns after 100 ms of silence
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

func OpenRadio(dev string, logf func(string, ...any)) (*Radio, error) {
	fd, err := syscall.Open(dev, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := setRaw(fd); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	syscall.SetNonblock(fd, false)
	return &Radio{fd: fd, log: logf}, nil
}

// Run reads frames forever and hands each payload to handle.
func (r *Radio) Run(handle func([]byte)) {
	var fr Framer
	rd := make([]byte, 256)
	for {
		n, err := syscall.Read(r.fd, rd)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			r.log("radio read error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		for _, p := range fr.Push(rd[:n], n == 0) {
			handle(p)
		}
	}
}

// Send writes one payload (CRC is added here).
func (r *Radio) Send(payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := syscall.Write(r.fd, withCRC(payload))
	return err
}

// ReplayRadio feeds a captured byte stream instead of the real modem
// (for testing). Sending is a no-op.
type ReplayRadio struct {
	path  string
	delay time.Duration
	log   func(string, ...any)
}

func (r *ReplayRadio) Run(handle func([]byte)) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.log("replay: %v", err)
		return
	}
	var fr Framer
	for _, p := range fr.Push(data, true) {
		handle(p)
		time.Sleep(r.delay)
	}
	r.log("replay finished, %d bytes dropped", fr.Dropped)
}

func (r *ReplayRadio) Send(payload []byte) error { return nil }

type Transport interface {
	Run(handle func([]byte))
	Send(payload []byte) error
}
