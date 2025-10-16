package util

import (
	"fmt"
	"strings"
	"sync"
)

// Minimal printer shim preserved as `Default` and `PrintChan` for compatibility.
// This file replaces the legacy safeprint implementation.

type printerShim struct {
	mu        sync.Mutex
	suspended bool
}

var Default = &printerShim{}

var PrintChan chan string
var printChanMu sync.RWMutex

func SetPrintChannel(ch chan string) {
	printChanMu.Lock()
	defer printChanMu.Unlock()
	PrintChan = ch
}

func (p *printerShim) Print(a ...interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	msg := fmt.Sprint(a...)
	fmt.Print(msg)
}

func (p *printerShim) Printf(format string, a ...interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Print(msg)
}

func (p *printerShim) Println(a ...interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	msg := fmt.Sprintln(a...)
	fmt.Print(msg)
}

func (p *printerShim) ClearScreen() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	msg := "\x1b[2J\x1b[H"
	fmt.Print(msg)
}

func (p *printerShim) PrintBlock(block string, clearLine bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	if clearLine {
		block = "\r\x1b[K" + block
	}
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	fmt.Print(block)
}

func (p *printerShim) ClearLine() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.suspended {
		return
	}
	msg := "\r\x1b[K"
	fmt.Print(msg)
}

func (p *printerShim) Suspend() {
	p.mu.Lock()
	p.suspended = true
	p.mu.Unlock()
}

func (p *printerShim) Resume() {
	p.mu.Lock()
	p.suspended = false
	p.mu.Unlock()
}

func (p *printerShim) IsSuspended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.suspended
}
