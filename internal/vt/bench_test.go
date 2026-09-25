package vt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// plainInput is representative shell output: full-width ASCII lines.
func plainInput(lines int) []byte {
	var b bytes.Buffer
	l := strings.Repeat("the quick brown fox jumps over the lazy dog ", 2)
	for range lines {
		b.WriteString(l)
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// sgrInput is typical prompt/coloured tool output with truecolor sequences.
func sgrInput(lines int) []byte {
	var b bytes.Buffer
	for i := range lines {
		fmt.Fprintf(&b, "\x1b[38;2;122;162;247mPS\x1b[0m \x1b[32mC:\\Users\\Jejo>\x1b[0m cmd %d\r\n", i)
	}
	return b.Bytes()
}

func benchWrite(b *testing.B, data []byte, sb int) {
	b.SetBytes(int64(len(data)))
	term := newTerminal(TerminalInfo{cols: 120, rows: 40})
	term.SetScrollbackSize(sb)
	term.Write(data)
	b.ResetTimer()
	for range b.N {
		term.Write(data)
	}
}

func BenchmarkWritePlain(b *testing.B)             { benchWrite(b, plainInput(2000), 10000) }
func BenchmarkWritePlainNoScrollback(b *testing.B) { benchWrite(b, plainInput(2000), 0) }
func BenchmarkWriteSGR(b *testing.B)               { benchWrite(b, sgrInput(4000), 10000) }

// BenchmarkFrameRead measures the per-frame cost of the renderer's access
// pattern: lock once, walk every visible cell, then clear damage.
func BenchmarkFrameRead(b *testing.B) {
	term := newTerminal(TerminalInfo{cols: 120, rows: 40})
	for i := range 500 {
		term.Write([]byte("row " + string(rune('0'+i%10)) + " of output\r\n"))
	}
	var sink rune
	b.ResetTimer()
	for range b.N {
		term.Lock()
		for y := range 40 {
			line, _ := term.ViewRow(y)
			for _, g := range line {
				sink += g.Char
			}
		}
		term.ClearDamage()
		term.Unlock()
	}
	_ = sink
}
