package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

type flushBuffer struct {
	bytes.Buffer
	flushes int
}

func (f *flushBuffer) Flush() { f.flushes++ }

func TestWriterEmitsOneJSONLinePerEvent(t *testing.T) {
	var buf flushBuffer
	w := NewWriter(&buf)
	w.Emit(Step("build"))
	w.Emit(Done("https://app.test", "r1", 41000))

	sc := bufio.NewScanner(&buf.Buffer)
	var got []Event
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q is not JSON: %v", sc.Text(), err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].V != Protocol || got[0].Type != "step" || got[0].Name != "build" {
		t.Errorf("first event = %+v", got[0])
	}
	if got[1].Type != "done" || got[1].URL != "https://app.test" || got[1].HostPort != 41000 {
		t.Errorf("second event = %+v", got[1])
	}
	if buf.flushes != 2 {
		t.Errorf("flushes = %d, want 2", buf.flushes)
	}
}

func TestWriterIsSafeForConcurrentUse(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.Emit(Log("build", "line")) }()
	}
	wg.Wait()
	sc := bufio.NewScanner(&buf)
	n := 0
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("interleaved output: %q", sc.Text())
		}
		n++
	}
	if n != 50 {
		t.Fatalf("got %d lines, want 50", n)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		e    Event
		want int
	}{
		{Done("", "", 0), 0},
		{Fail("invalid", "bad", nil), 2},
		{Fail("build_failed", "x", nil), 1},
		{Step("build"), 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.e); got != c.want {
			t.Errorf("ExitCode(%+v) = %d, want %d", c.e, got, c.want)
		}
	}
}

func TestRecorder(t *testing.T) {
	var r Recorder
	r.Emit(Step("extract"))
	r.Emit(Warn("tls_pending", "later"))
	r.Emit(Step("build"))
	r.Emit(Done("", "r1", 1))
	if got := r.Steps(); len(got) != 2 || got[0] != "extract" || got[1] != "build" {
		t.Errorf("Steps() = %v", got)
	}
	if !r.Has("warn", "tls_pending") || r.Has("warn", "other") {
		t.Error("Has() gave the wrong answer")
	}
	if r.Last().Type != "done" || r.Last().V != Protocol {
		t.Errorf("Last() = %+v", r.Last())
	}
}
