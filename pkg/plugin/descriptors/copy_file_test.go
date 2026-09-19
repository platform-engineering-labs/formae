// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package descriptors

import (
	"errors"
	"strings"
	"testing"
)

// failingWriteCloser lets a test drive the write-side Close error path
// without needing a real filesystem failure.
type failingWriteCloser struct {
	writeErr error
	closeErr error
}

func (f *failingWriteCloser) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *failingWriteCloser) Close() error {
	return f.closeErr
}

func TestCopyAndClose_PropagatesCloseError(t *testing.T) {
	closeErr := errors.New("flush failed: no space left on device")
	dst := &failingWriteCloser{closeErr: closeErr}
	src := strings.NewReader("data")

	err := copyAndClose(dst, src)

	if !errors.Is(err, closeErr) {
		t.Fatalf("expected copyAndClose to return the close error, got %v", err)
	}
}

func TestCopyAndClose_CopyErrorWinsOverCloseError(t *testing.T) {
	copyErr := errors.New("write failed")
	dst := &failingWriteCloser{writeErr: copyErr, closeErr: errors.New("close failed")}
	src := strings.NewReader("data")

	err := copyAndClose(dst, src)

	if !errors.Is(err, copyErr) {
		t.Fatalf("expected copyAndClose to return the copy error, got %v", err)
	}
}

func TestCopyAndClose_NoErrors(t *testing.T) {
	dst := &failingWriteCloser{}
	src := strings.NewReader("data")

	if err := copyAndClose(dst, src); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
