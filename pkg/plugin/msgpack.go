// (c) 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"bytes"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// encodeMsgpack serializes v to MessagePack, compresses the result with zstd,
// and writes the compressed bytes to w.
func encodeMsgpack(w io.Writer, v any) error {
	var buf bytes.Buffer

	enc := msgpack.NewEncoder(&buf)
	enc.SetCustomStructTag("json")

	if err := enc.Encode(v); err != nil {
		return err
	}

	zw, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}

	if _, err := zw.Write(buf.Bytes()); err != nil {
		_ = zw.Close()
		return err
	}

	return zw.Close()
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
