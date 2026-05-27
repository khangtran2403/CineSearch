package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Progress — simple terminal progress bar
type Progress struct {
	total     int64
	current   atomic.Int64
	startTime time.Time
	label     string
}

func NewProgress(total int, label string) *Progress {
	return &Progress{
		total:     int64(total),
		startTime: time.Now(),
		label:     label,
	}
}

func (p *Progress) Inc() {
	p.current.Add(1)
	p.Render()
}

func (p *Progress) Render() {
	cur := p.current.Load()
	pct := float64(cur) / float64(p.total) * 100
	filled := int(pct / 2.5) // 40-char bar
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 40-filled)

	elapsed := time.Since(p.startTime)
	var eta string
	if cur > 0 {
		remaining := time.Duration(float64(elapsed) / float64(cur) * float64(p.total-cur))
		eta = fmt.Sprintf("ETA %s", remaining.Round(time.Second))
	} else {
		eta = "ETA --"
	}

	fmt.Printf("\r%s [%s] %d/%d (%.0f%%) %s     ",
		p.label, bar, cur, p.total, pct, eta)
}

func (p *Progress) Done() {
	cur := p.current.Load()
	elapsed := time.Since(p.startTime).Round(time.Millisecond)
	fmt.Printf("\r%s [%s] %d/%d (100%%) ✅ done in %s\n",
		p.label,
		strings.Repeat("█", 40),
		cur, p.total, elapsed,
	)
}

// printBanner — header khi chạy seed
func printBanner(target int) {
	fmt.Println()
	fmt.Printf("  Target: %d phim\n\n", target)
}

// printStats — tóm tắt sau khi xong
func printStats(state *SeedState) {
	elapsed := time.Since(state.StartedAt).Round(time.Second)
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────")
	fmt.Printf("   Seeded:   %d phim\n", state.TotalSeeded)
	fmt.Printf("   Failed:   %d\n", state.TotalFailed)
	fmt.Printf("    Time:     %s\n", elapsed)
	if elapsed.Seconds() > 0 {
		fmt.Printf("   Rate:     %.1f phim/s\n",
			float64(state.TotalSeeded)/elapsed.Seconds())
	}
	fmt.Println("─────────────────────────────────────────────")
	fmt.Println()
}