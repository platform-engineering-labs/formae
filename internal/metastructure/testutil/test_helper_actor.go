// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

// TestHelperActor is an auxiliary actor for making synchronous calls in tests
type TestHelperActor struct {
	act.Actor
	messages chan<- any
}

func newTestHelper() gen.ProcessBehavior {
	return &TestHelperActor{}
}

type TestCall[T any, R any] struct {
	Request  T
	Response chan<- R
	Errors   chan<- error
	Target   string
	Timeout  int // Optional timeout in seconds (default 5)
}

type TestMessage[T any] struct {
	Message T
	Target  string
}

func (a *TestHelperActor) Init(args ...any) error {
	if len(args) > 0 {
		if messages, ok := args[0].(chan<- any); ok {
			a.messages = messages
		} else {
			return fmt.Errorf("testHelperActor expected a channel of type chan<- any as the first argument")
		}
	}

	a.Log().Info("TestHelperActor initialized")
	return nil
}

func (a *TestHelperActor) HandleMessage(from gen.PID, message any) error {
	a.Log().Warning("TestHelperActor received message: %T", message)

	switch msg := message.(type) {
	case TestCall[any, any]:
		target := gen.ProcessID{
			Name: gen.Atom(msg.Target),
			Node: a.Node().Name(),
		}
		timeout := msg.Timeout
		if timeout <= 0 {
			timeout = 5 // Default timeout
		}
		a.Log().Info("Sending %T to %v (timeout: %ds)", msg.Request, target, timeout)
		res, err := a.CallWithTimeout(target, msg.Request, timeout)
		if err != nil {
			a.Log().Error("Got error sending %T to %v: %s\n", msg.Request, target, err)
			msg.Errors <- err

			return nil
		}
		a.Log().Info("Successfully sent %T to %v", msg.Request, target)
		msg.Response <- res

		return nil
	case TestMessage[any]:
		target := gen.ProcessID{
			Name: gen.Atom(msg.Target),
			Node: a.Node().Name(),
		}
		err := a.Send(target, msg.Message)
		if err != nil {
			a.Log().Error("Failed to send message to %s: %s", msg.Target, err)
			return err
		}
		return nil
	default:
		if a.messages != nil {
			a.messages <- message
		}
		return nil
	}
}

func StartTestHelperActor(node gen.Node, messages chan<- any) (gen.PID, error) {
	a, err := node.SpawnRegister("TestHelperActor", newTestHelper, gen.ProcessOptions{}, messages)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to spawn TestHelperActor: %w", err)
	}

	return a, nil
}

func Call(node gen.Node, target string, request any) (any, error) {
	return CallWithTimeout(node, target, request, 0) // Use default timeout
}

// CallWithTimeout sends a request to a target process with a custom timeout.
// If timeoutSecs is <= 0, the default timeout (5 seconds) is used.
func CallWithTimeout(node gen.Node, target string, request any, timeoutSecs int) (any, error) {
	res := make(chan any)
	errs := make(chan error)

	call := TestCall[any, any]{
		Request:  request,
		Response: res,
		Errors:   errs,
		Target:   target,
		Timeout:  timeoutSecs,
	}

	err := node.Send(testHelperProcess(node), call)
	if err != nil {
		return nil, fmt.Errorf("failed to send message to TestHelperActor: %w", err)
	}

	select {
	case r := <-res:
		return r, nil
	case e := <-errs:
		return nil, e
	}
}

func Send(node gen.Node, target gen.Atom, message any) error {
	err := node.Send(testHelperProcess(node), TestMessage[any]{Message: message, Target: string(target)})
	if err != nil {
		return fmt.Errorf("failed to send message to TestHelperActor: %w", err)
	}

	return nil
}

func ExpectMessageWithPredicate[T any](t *testing.T, ch <-chan any, timeout time.Duration, predicate func(T) bool) {
	// t.Helper() marks this function as a test helper. When t.Fatalf is called,
	// the Go testing framework will report the line number of the caller, not this function.
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case msg := <-ch:
			var zero T
			expectedType := reflect.TypeOf(zero)

			typedMsg, ok := msg.(T)
			if !ok {
				t.Fatalf("Received message of wrong type. Expected %q but got %q", expectedType.String(), reflect.TypeOf(msg).String())
				continue
			}

			// The type is correct, now check if it satisfies the predicate.
			if predicate(typedMsg) {
				// Success! The message is of the correct type and satisfies the predicate.
				return
			}

			t.Fatalf("Received message of type %T, but it did not satisfy the predicate: %+v", typedMsg, typedMsg)
			return

		case <-timer.C:
			var zero T
			t.Fatalf("Timed out after %v waiting for a message of type %s that satisfies predicate", timeout, reflect.TypeOf(zero).Name())
			return
		}
	}
}

func testHelperProcess(node gen.Node) gen.ProcessID {
	return gen.ProcessID{Name: "TestHelperActor", Node: node.Name()}
}
