package main

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Device holds readings about the R-167 itself.
type Device struct {
	// Temperature of the SoC (NXP Vybrid) in °C, from its internal sensor
	// read through the ADC driver. Uncalibrated, so only a rough value.
	Temperature *float64 `json:"temperature"`
}

const (
	deviceTempInterval = 60 * time.Second
	deviceTempSamples  = 5
)

// readMilliC reads an IIO in_temp_input file (thousandths of a °C).
func readMilliC(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	return float64(v) / 1000, nil
}

// readDeviceTemp averages a few samples, since single readings jitter by
// around half a degree. Returns nil if the sensor cannot be read.
func readDeviceTemp(path string) *float64 {
	sum, n := 0.0, 0
	for i := 0; i < deviceTempSamples; i++ {
		if v, err := readMilliC(path); err == nil {
			sum += v
			n++
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n == 0 {
		return nil
	}
	v := math.Round(sum/float64(n)*10) / 10
	return &v
}

// WatchDeviceTemp reads the SoC temperature every minute and publishes a
// "device" message when the value changes.
func (c *Controller) WatchDeviceTemp(path string) {
	if _, err := readMilliC(path); err != nil {
		c.log("device temperature not available (%v)", err)
		return
	}
	for {
		c.setDeviceTemp(readDeviceTemp(path))
		time.Sleep(deviceTempInterval)
	}
}

func (c *Controller) setDeviceTemp(v *float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if same(c.device.Temperature, v) {
		return
	}
	c.device.Temperature = v
	c.pub("device", c.device)
}
