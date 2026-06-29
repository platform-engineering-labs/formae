// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"sync"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

// linkProbe is a minimal actor used to pin the directionality of gen.Process.LinkPID
// in the pinned ergo fork. The fork's godoc claims LinkPID is "bidirectional"; Task 6
// of PLA-202 depends on it being unidirectional (target death terminates the linker,
// but the linker's death does NOT affect the target). This test settles that empirically.
//
// Each probe registers itself in a shared registry keyed by name so the test can:
//   - find a probe's PID,
//   - ask a probe to LinkPID another probe,
//   - observe whether a probe terminated (via Terminate callback recording into the registry).
type linkProbe struct {
	act.Actor
	name string
	reg  *probeRegistry
}

type linkPIDRequest struct {
	target gen.PID
}

type probeRegistry struct {
	mu         sync.Mutex
	pids       map[string]gen.PID
	terminated map[string]bool
}

func newProbeRegistry() *probeRegistry {
	return &probeRegistry{
		pids:       make(map[string]gen.PID),
		terminated: make(map[string]bool),
	}
}

func (r *probeRegistry) setPID(name string, pid gen.PID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pids[name] = pid
}

func (r *probeRegistry) getPID(name string) gen.PID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pids[name]
}

func (r *probeRegistry) markTerminated(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.terminated[name] = true
}

func (r *probeRegistry) isTerminated(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminated[name]
}

func (p *linkProbe) Init(args ...any) error {
	p.name = args[0].(string)
	p.reg = args[1].(*probeRegistry)
	p.reg.setPID(p.name, p.PID())
	return nil
}

func (p *linkProbe) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case linkPIDRequest:
		if err := p.LinkPID(msg.target); err != nil {
			p.Log().Error("linkProbe %s: LinkPID failed: %v", p.name, err)
			return err
		}
	}
	return nil
}

func (p *linkProbe) Terminate(reason error) {
	p.reg.markTerminated(p.name)
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestLinkPIDDirectionality pins the directionality of LinkPID in the pinned ergo fork.
//
// Contract Task 6 relies on (despite the godoc saying "bidirectional"):
//   - Process A calls LinkPID(B). When B terminates, A terminates (target death
//     propagates to the linker).
//   - When the linker A terminates, B SURVIVES (the linker's death does NOT propagate
//     to the target).
//
// If this test ever shows B terminating when A dies, links are bidirectional and Task 6
// must use a monitor-based alternative instead (an operator crash would kill its RU).
func TestLinkPIDDirectionality(t *testing.T) {
	node, err := ergo.StartNode("link_direction_test@localhost", gen.NodeOptions{})
	if err != nil {
		t.Fatalf("failed to start node: %v", err)
	}
	defer node.Stop()

	reg := newProbeRegistry()

	factory := func() gen.ProcessBehavior { return &linkProbe{} }

	// ---- Case 1: target death propagates to linker ----
	// A LinkPID(B); kill B; expect A to terminate.
	aPID, err := node.Spawn(factory, gen.ProcessOptions{}, "A", reg)
	if err != nil {
		t.Fatalf("failed to spawn A: %v", err)
	}
	bPID, err := node.Spawn(factory, gen.ProcessOptions{}, "B", reg)
	if err != nil {
		t.Fatalf("failed to spawn B: %v", err)
	}

	if err := node.Send(aPID, linkPIDRequest{target: bPID}); err != nil {
		t.Fatalf("failed to ask A to link B: %v", err)
	}
	// Give the link time to be established before terminating B.
	time.Sleep(100 * time.Millisecond)

	if err := node.Kill(bPID); err != nil {
		t.Fatalf("failed to kill B: %v", err)
	}

	if !waitFor(func() bool { return reg.isTerminated("B") }, 2*time.Second) {
		t.Fatalf("target B did not terminate after Kill")
	}
	if !waitFor(func() bool { return reg.isTerminated("A") }, 2*time.Second) {
		t.Fatalf("LINK DIRECTIONALITY: linker A did NOT terminate when target B died; " +
			"target-death does not propagate to linker. Task 6 mechanism is invalid.")
	}

	// ---- Case 2: linker death does NOT propagate to target ----
	// Fresh pair: C LinkPID(D); kill C; expect D to SURVIVE.
	cPID, err := node.Spawn(factory, gen.ProcessOptions{}, "C", reg)
	if err != nil {
		t.Fatalf("failed to spawn C: %v", err)
	}
	dPID, err := node.Spawn(factory, gen.ProcessOptions{}, "D", reg)
	if err != nil {
		t.Fatalf("failed to spawn D: %v", err)
	}

	if err := node.Send(cPID, linkPIDRequest{target: dPID}); err != nil {
		t.Fatalf("failed to ask C to link D: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := node.Kill(cPID); err != nil {
		t.Fatalf("failed to kill C: %v", err)
	}

	if !waitFor(func() bool { return reg.isTerminated("C") }, 2*time.Second) {
		t.Fatalf("linker C did not terminate after Kill")
	}

	// Give ample time for any (unwanted) propagation to D to occur.
	if waitFor(func() bool { return reg.isTerminated("D") }, 1*time.Second) {
		t.Fatalf("LINK DIRECTIONALITY: target D terminated when linker C died; " +
			"links are BIDIRECTIONAL. Task 6 must use a monitor instead of a link " +
			"(an operator crash would otherwise kill its RU).")
	}

	// D should still be alive and findable.
	if reg.getPID("D") == (gen.PID{}) {
		t.Fatalf("target D PID missing from registry")
	}
}
