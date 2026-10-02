// uhomed - replacement firmware daemon for the Uponor R-167 (U@home)
// gateway in a Smatrix Wave PLUS system.
//
// It talks to the R-167's internal radio modem (/dev/ttyLP2), keeps the
// state of every room, and serves it over a WebSocket and a small web
// page on port 8765. It also feeds the hardware watchdog, replacing the
// original "platform" and "KickWatchdog" processes.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "0.4.3"

//go:embed index.html
var webFiles embed.FS

var (
	logMu   sync.Mutex
	logPath string
)

func logf(format string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, a...) + "\n"
	logMu.Lock()
	defer logMu.Unlock()
	os.Stderr.WriteString(line)
	if logPath == "" {
		return
	}
	if st, err := os.Stat(logPath); err == nil && st.Size() > 512*1024 {
		os.Rename(logPath, logPath+".1")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		f.WriteString(line)
		f.Close()
	}
}

// feedWatchdog keeps the hardware watchdog (60 s timeout) alive and
// disarms it cleanly ("magic close") when the daemon is stopped.
func feedWatchdog(dev string, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		logf("watchdog: cannot open %s: %v (is KickWatchdog still running?)", dev, err)
		return
	}
	logf("watchdog: feeding %s", dev)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			f.Write([]byte("k"))
		case <-stop:
			f.Write([]byte("V"))
			f.Close()
			logf("watchdog: disarmed")
			return
		}
	}
}

// findProcess returns the first running process whose command line
// contains one of the given strings.
func findProcess(needles ...string) string {
	entries, _ := os.ReadDir("/proc")
	self := fmt.Sprint(os.Getpid())
	for _, e := range entries {
		if e.Name() == self || e.Name()[0] < '0' || e.Name()[0] > '9' {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(b), "\x00", " ")
		for _, n := range needles {
			if strings.Contains(cmd, n) {
				return strings.TrimSpace(cmd)
			}
		}
	}
	return ""
}

func main() {
	dev := flag.String("dev", "/dev/ttyLP2", "radio modem tty")
	listen := flag.String("listen", ":8765", "HTTP/WebSocket listen address")
	wdDev := flag.String("watchdog", "/dev/watchdog", "watchdog device (empty = don't feed)")
	replay := flag.String("replay", "", "replay a captured ttyLP2 byte stream instead of using the modem (testing)")
	replayDelay := flag.Duration("replay-delay", 50*time.Millisecond, "delay between replayed frames")
	reqInterval := flag.Duration("request-interval", 8*time.Second, "minimum time between record requests")
	flag.StringVar(&logPath, "log", "/tmp/uhomed.log", "log file (empty = stderr only)")
	force := flag.Bool("force", false, "start even if the original Uponor software is running")
	flag.Parse()

	logf("uhomed %s starting", version)

	// Never run alongside the original software: both would talk to the
	// radio modem at the same time.
	if *replay == "" && !*force {
		if p := findProcess("/mnt/UserFS/platform", "KickWatchdog"); p != "" {
			logf("refusing to start: %s is running (use uhome-mode.sh custom)", p)
			os.Exit(1)
		}
	}

	hub := NewHub(logf)

	var radio Transport
	if *replay != "" {
		radio = &ReplayRadio{path: *replay, delay: *replayDelay, log: logf}
		*wdDev = ""
	} else {
		r, err := OpenRadio(*dev, logf)
		if err != nil {
			logf("cannot open radio %s: %v", *dev, err)
			os.Exit(1)
		}
		radio = r
	}

	ctrl := NewController(radio, logf, hub.Publish, *reqInterval)

	stopWD := make(chan struct{})
	wdStopped := make(chan struct{})
	if *wdDev != "" {
		go feedWatchdog(*wdDev, stopWD, wdStopped)
	} else {
		close(wdStopped)
	}

	go radio.Run(ctrl.Handle)
	go ctrl.Watch()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := wsUpgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hub.Serve(c, encode("snapshot", ctrl.Snapshot()), func(c *wsConn, b []byte) {
			var cmd struct {
				Type  string   `json:"type"`
				ID    any      `json:"id"`
				Room  string   `json:"room"`
				Value *float64 `json:"value"`
			}
			if json.Unmarshal(b, &cmd) != nil {
				return
			}
			reply := func(err error) {
				res := map[string]any{"id": cmd.ID, "success": err == nil}
				if err != nil {
					res["error"] = err.Error()
				}
				select {
				case c.out <- encode("result", res):
				default:
				}
			}
			switch cmd.Type {
			case "get_snapshot":
				c.out <- encode("snapshot", ctrl.Snapshot())
			case "set_setpoint":
				if cmd.Value == nil || cmd.Room == "" {
					reply(fmt.Errorf("set_setpoint needs room and value"))
					return
				}
				ctrl.SetSetpoint(cmd.Room, *cmd.Value, reply)
			default:
				c.out <- encode("error", map[string]any{"id": cmd.ID, "message": "unsupported command: " + cmd.Type})
			}
		})
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(ctrl.Snapshot())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := webFiles.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		logf("listening on %s", *listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logf("http server: %v", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	s := <-sig
	logf("got %v, shutting down", s)
	close(stopWD)
	select {
	case <-wdStopped:
	case <-time.After(2 * time.Second):
	}
	srv.Close()
}
