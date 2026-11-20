package queue

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
)

func item(s string) Item { return Item{Prompt: prompt.Prompt{Instruction: s}, Label: s} }

func TestFIFOAndMax(t *testing.T) {
	q := New(t.TempDir())
	for i := 0; i < 3; i++ {
		if pos, err := q.Push(item(fmt.Sprint(i)), 3); err != nil || pos != i+1 {
			t.Fatalf("push %d: pos=%d err=%v", i, pos, err)
		}
	}
	if _, err := q.Push(item("x"), 3); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	for i := 0; i < 3; i++ {
		c, err := q.Claim()
		if err != nil || c == nil || c.Prompt.Instruction != fmt.Sprint(i) {
			t.Fatalf("claim %d: %+v %v", i, c, err)
		}
		c.Done()
	}
	if c, _ := q.Claim(); c != nil {
		t.Fatal("queue should be empty")
	}
}

func TestReleaseKeepsOrder(t *testing.T) {
	q := New(t.TempDir())
	q.Push(item("a"), 5)
	q.Push(item("b"), 5)
	c, _ := q.Claim()
	c.Release()
	c, _ = q.Claim()
	if c.Prompt.Instruction != "a" {
		t.Fatalf("released item lost its place: %q", c.Prompt.Instruction)
	}
}

func TestConcurrentClaimsAreExclusive(t *testing.T) {
	q := New(t.TempDir())
	const n = 20
	for i := 0; i < n; i++ {
		q.Push(item(fmt.Sprint(i)), n)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := q.Claim()
				if err != nil {
					t.Error(err)
					return
				}
				if c == nil {
					return
				}
				mu.Lock()
				seen[c.Prompt.Instruction]++
				mu.Unlock()
				c.Done()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("claimed %d distinct items, want %d", len(seen), n)
	}
	for k, v := range seen {
		if v != 1 {
			t.Fatalf("item %s claimed %d times", k, v)
		}
	}
}

func TestClear(t *testing.T) {
	q := New(t.TempDir())
	q.Push(item("a"), 5)
	q.Push(item("b"), 5)
	q.Claim() // leave one stale in-flight
	if n := q.Clear(); n != 2 || q.Len() != 0 || q.Inflight() != 0 {
		t.Fatalf("cleared %d, len %d, inflight %d", n, q.Len(), q.Inflight())
	}
}
