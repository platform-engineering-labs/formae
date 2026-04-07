// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"bytes"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// encodeMsgpack serializes v to MessagePack, compresses with zstd, and writes
// the compressed bytes to w.
func encodeMsgpack(w io.Writer, v any) error {
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

	_, err = w.Write(zstdBuf.Bytes())
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
