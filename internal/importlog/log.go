package importlog

import (
	"fmt"
	"log"
	"sync"
)

const maxLines = 500

var (
	mu    sync.Mutex
	lines []string
	subs  = map[chan string]struct{}{}
)

// Printf mirrors to std log + ring buffer + live subscribers.
func Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s", msg)

	mu.Lock()
	// drop oldest if full
	if len(lines) >= maxLines {
		copy(lines, lines[1:])
		lines = lines[:maxLines-1]
	}
	lines = append(lines, msg)
	// fan out (non-blocking)
	for ch := range subs {
		select { case ch <- msg: default: }
	}
	mu.Unlock()
}

// Snapshot returns the last n lines (or all if n<=0 or n>=len).
func Snapshot(n int) []string {
	mu.Lock()
	defer mu.Unlock()
	if n <= 0 || n >= len(lines) {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	out := make([]string, n)
	copy(out, lines[len(lines)-n:])
	return out
}

// Subscribe returns a channel that receives future log lines.
// Call Unsubscribe when done.
func Subscribe() chan string {
	ch := make(chan string, 64)
	mu.Lock()
	subs[ch] = struct{}{}
	mu.Unlock()
	return ch
}

func Unsubscribe(ch chan string) {
	mu.Lock()
	delete(subs, ch)
	mu.Unlock()
	close(ch)
}
