// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"bytes"
	"encoding/binary"
	"io"

	"ergo.services/ergo/lib"
	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// encodeMsgpack serializes v to MessagePack, compresses with zstd, and writes
// the compressed bytes to w.
//
// IMPORTANT: Ergo's MarshalEDF encoder calls lib.Buffer.Extend(4) to reserve a
// length prefix BEFORE calling MarshalEDF. Extend returns a slice into the
// current backing array. If our Write triggers append-reallocation, that slice
// becomes stale. To prevent this, we pre-grow the buffer by type-asserting w to
// *lib.Buffer and calling Allocate with our payload size before writing.
func encodeMsgpack(w io.Writer, v any) error {
	// Encode to msgpack, then compress with zstd into a temporary buffer.
	var msgpackBuf bytes.Buffer
	enc := msgpack.NewEncoder(&msgpackBuf)
	enc.SetCustomStructTag("json")
	if err := enc.Encode(v); err != nil {
		return err
	}

	var zstdBuf bytes.Buffer
	zw, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		return err
	}
	if _, err := zw.Write(msgpackBuf.Bytes()); err != nil {
		_ = zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}

	compressed := zstdBuf.Bytes()

	// Workaround for Ergo's lib.Buffer reallocation bug:
	// Ergo's MarshalEDF encoder calls Extend(4) before MarshalEDF to reserve a
	// 4-byte length prefix, returning a slice into the current backing array.
	// If our Write triggers append-reallocation, that slice becomes stale and
	// the length prefix is lost (zeroed). We detect this case and patch the
	// length prefix directly in the buffer's backing array after writing.
	if ergoBuf, ok := w.(*lib.Buffer); ok {
		lenPrefixOffset := ergoBuf.Len() - 4 // Extend(4) was called just before us
		oldPtr := &ergoBuf.B[0]

		ergoBuf.Append(compressed)

		if &ergoBuf.B[0] != oldPtr {
			// Buffer was reallocated. Patch the length prefix in the new array.
			binary.BigEndian.PutUint32(ergoBuf.B[lenPrefixOffset:], uint32(len(compressed)))
		}
		return nil
	}

	_, err = w.Write(compressed)
	return err
}

// decodeMsgpack decompresses zstd-compressed data and unmarshals the
// resulting MessagePack bytes into v.
func decodeMsgpack(data []byte, v any) error {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zr.Close()

	decompressed, err := io.ReadAll(zr)
	if err != nil {
		return err
	}

	dec := msgpack.NewDecoder(bytes.NewReader(decompressed))
	dec.SetCustomStructTag("json")

	return dec.Decode(v)
}
