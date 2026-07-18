// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package printer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

type Consumer string

const (
	ConsumerHuman   Consumer = "human"
	ConsumerMachine Consumer = "machine"
)

type MachineReadablePrinter[T any] struct {
	w      io.Writer
	format string
}

func NewMachineReadablePrinter[T any](w io.Writer, format string) *MachineReadablePrinter[T] {
	return &MachineReadablePrinter[T]{
		w:      w,
		format: format,
	}
}

func (p *MachineReadablePrinter[T]) Print(v *T) error {
	var data []byte
	var err error
	switch p.format {
	case "json":
		data, err = json.Marshal(v)
		if err != nil {
			return fmt.Errorf("json marshal: %w", err)
		}
	case "yaml":
		intermediate, convertErr := convertRawMessages(v)
		if convertErr != nil {
			return fmt.Errorf("convert raw messages: %w", err)
		}

		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err = enc.Encode(intermediate); err != nil {
			return fmt.Errorf("yaml encode: %w", err)
		}
		data = buf.Bytes()
	default:
		return fmt.Errorf("unsupported format: %s", p.format)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	_, err = p.w.Write(data)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func convertRawMessages(v any) (any, error) {
	jsonData, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var result any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		return nil, err
	}

	return result, nil
}
