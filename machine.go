// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// Machine is the whole emulated unit.
type Machine struct {
	CPU  *CPU
	Bus  *Bus
	Chip *Chip
	LCD  *LCD
	Keys *Keys
	Sub  *SubPort

	flashPath string // the image this machine was built from, for PowerCycle
}

// batteryRAMBase is the start of the memory that survives a power cycle on the real unit
// (battery backed SRAM with the system settings and all user songs and patterns).
const batteryRAMBase = 0x400000

// BatteryRAM returns a copy of the battery backed memory.
func (m *Machine) BatteryRAM() []byte {
	return append([]byte(nil), m.Bus.find(batteryRAMBase).Data...)
}

// PowerCycle turns the unit off and on: the machine restarts from the reset vector with
// every register, on-chip RAM and device back at power on state. ram is the battery
// backed memory to keep (nil is blank memory, which makes the firmware run Factory Set).
func (m *Machine) PowerCycle(ram []byte) error {
	nm, err := NewMachine(m.flashPath)
	if err != nil {
		return err
	}
	if ram != nil {
		copy(nm.Bus.find(batteryRAMBase).Data, ram)
	}
	*m = *nm
	return nil
}

// NewMachine builds the memory map and devices from the flash image.
func NewMachine(flashPath string) (*Machine, error) {
	b, err := NewUnitBus(flashPath)
	if err != nil {
		return nil, err
	}
	stubPresetStyleHeaders(b.find(0).Data) // after the image is loaded, so NewUnitBus stays a pristine image
	c := NewCPU(b)
	m := &Machine{CPU: c, Bus: b, flashPath: flashPath}
	m.Chip = AttachChip(c)
	m.LCD = AttachLCD(b)
	m.Keys = AttachKeys(b)
	m.Sub = AttachSubPort(b)
	b.PCFunc = func() uint32 { return c.PC }
	// The protected boot block configures the MIDI serial port before the main program
	// starts (the program only toggles TIE in SCR1): SCI1 async, 31250 baud, RIE TE RE.
	m.Chip.sci[1].regs[sciBRR] = 16
	m.Chip.sci[1].regs[sciSCR] = 0x70
	m.Chip.startBootTimers()
	c.R[7] = 0xFFF45A // the protected boot block sets sp before jumping to the reset handler (assumed)
	c.PC = 0xDCEEA
	return m, nil
}

// Run executes n instructions, stopping early on an error.
func (m *Machine) Run(n int) error {
	for i := 0; i < n; i++ {
		if err := m.CPU.Step(); err != nil {
			return err
		}
	}
	return nil
}

// MIDIIn feeds bytes to the MIDI input (SCI1); they arrive at 31250 baud.
func (m *Machine) MIDIIn(data []byte) {
	for _, b := range data {
		m.Chip.sci[1].Inject(b)
	}
}

// MIDIOut returns the bytes sent on the MIDI output since the last call.
func (m *Machine) MIDIOut() []byte {
	s := m.Chip.sci[1]
	out := append([]byte(nil), s.TxLog...)
	s.TxLog = s.TxLog[:0]
	return out
}

// SendSysEx feeds one message to MIDI IN at wire speed, runs until the serial
// receiver has taken every byte, then settle more instructions (the toolkit
// pauses 0.1 s between blocks; 600000 instructions is about that), and returns
// everything the unit sent meanwhile.
func (m *Machine) SendSysEx(msg []byte, settle int) ([]byte, error) {
	m.MIDIIn(msg)
	s := m.Chip.sci[1]
	for len(s.rxq) > 0 {
		if err := m.Run(100000); err != nil {
			return nil, err
		}
	}
	// keep running until the output (ignoring Active Sensing, FE) has been quiet for settle instructions
	count := func() int {
		n := 0
		for _, b := range s.TxLog {
			if b != 0xFE {
				n++
			}
		}
		return n
	}
	last, quiet := count(), 0
	for quiet < settle {
		if err := m.Run(50000); err != nil {
			return nil, err
		}
		if n := count(); n != last {
			last, quiet = n, 0
		} else {
			quiet += 50000
		}
	}
	return m.MIDIOut(), nil
}

// SplitSysEx cuts a byte stream into F0..F7 messages.
func SplitSysEx(data []byte) [][]byte {
	var out [][]byte
	start := -1
	for i, b := range data {
		switch b {
		case 0xF0:
			start = i
		case 0xF7:
			if start >= 0 {
				out = append(out, data[start:i+1])
				start = -1
			}
		}
	}
	return out
}
