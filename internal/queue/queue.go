// Package queue is a per-session FIFO of prompts waiting for an idle session.
//
// Each item is one 0600 file in a tmpfs directory. Claiming an item is an atomic rename
// into "inflight/": exactly one claimer can win, which is what makes delivery exactly-once.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
)

var ErrFull = errors.New("queue is full")

type Item struct {
	Prompt  prompt.Prompt `json:"prompt"`
	Label   string        `json:"label"` // e.g. "explain main.go:3-5", shown in status, never the code
	Created time.Time     `json:"created"`
}

type Queue struct {
	Dir string // <session runtime dir>/queue; inflight lives next to it
}

func New(sessionDir string) Queue { return Queue{Dir: filepath.Join(sessionDir, "queue")} }

func (q Queue) inflightDir() string { return filepath.Join(filepath.Dir(q.Dir), "inflight") }

func (q Queue) names() []string {
	entries, err := os.ReadDir(q.Dir)
	if err != nil {
		return nil
	}
	var ns []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			ns = append(ns, e.Name())
		}
	}
	sort.Strings(ns)
	return ns
}

func (q Queue) Len() int { return len(q.names()) }

// Push appends an item; returns its 1-based position.
func (q Queue) Push(it Item, max int) (int, error) {
	if err := os.MkdirAll(q.Dir, 0o700); err != nil {
		return 0, err
	}
	n := q.Len()
	if n >= max {
		return n, ErrFull
	}
	if it.Created.IsZero() {
		it.Created = time.Now()
	}
	data, err := json.Marshal(it)
	if err != nil {
		return 0, err
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	tmp := filepath.Join(q.Dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, filepath.Join(q.Dir, name)); err != nil {
		return 0, err
	}
	return n + 1, nil
}

// Claimed is an item taken out of the queue. Call Done after it was submitted (or
// submission is unknown), Release to put it back if it was certainly not submitted.
type Claimed struct {
	Item
	name string
	q    Queue
}

// Claim takes the oldest item, or returns nil if the queue is empty.
func (q Queue) Claim() (*Claimed, error) {
	if err := os.MkdirAll(q.inflightDir(), 0o700); err != nil {
		return nil, err
	}
	for _, name := range q.names() {
		dst := filepath.Join(q.inflightDir(), name)
		if err := os.Rename(filepath.Join(q.Dir, name), dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // someone else claimed it
			}
			return nil, err
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			return nil, err
		}
		c := &Claimed{name: name, q: q}
		if err := json.Unmarshal(data, &c.Item); err != nil {
			os.Remove(dst)
			return nil, fmt.Errorf("corrupt queue item %s dropped: %w", name, err)
		}
		return c, nil
	}
	return nil, nil
}

func (c *Claimed) Done() error { return os.Remove(filepath.Join(c.q.inflightDir(), c.name)) }

func (c *Claimed) Release() error {
	return os.Rename(filepath.Join(c.q.inflightDir(), c.name), filepath.Join(c.q.Dir, c.name))
}

// List returns queued items, oldest first.
func (q Queue) List() []Item {
	var items []Item
	for _, name := range q.names() {
		data, err := os.ReadFile(filepath.Join(q.Dir, name))
		if err != nil {
			continue
		}
		var it Item
		if json.Unmarshal(data, &it) == nil {
			items = append(items, it)
		}
	}
	return items
}

// Inflight counts items claimed but not finished (non-zero only during delivery or after a crash).
func (q Queue) Inflight() int {
	entries, _ := os.ReadDir(q.inflightDir())
	return len(entries)
}

// Clear discards all queued and stale in-flight items.
func (q Queue) Clear() int {
	n := 0
	for _, dir := range []string{q.Dir, q.inflightDir()} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if os.Remove(filepath.Join(dir, e.Name())) == nil {
				n++
			}
		}
	}
	return n
}
