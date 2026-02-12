// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package printer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

type HumanReadablePrinter[T any] struct {
	w io.Writer
}

func NewHumanReadablePrinter[T any](w io.Writer) *HumanReadablePrinter[T] {
	return &HumanReadablePrinter[T]{
		w: w,
	}
}

type PrintOptions struct {
	Summary    bool
	MaxResults int
}

func (p *HumanReadablePrinter[T]) Print(v any, opts PrintOptions) error {
	switch v := any(v).(type) {
	case *apimodel.Simulation:
		output, err := renderer.RenderSimulation(v)
		if err != nil {
			return fmt.Errorf("render simulation: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *apimodel.ListCommandStatusResponse:
		var output string
		var err error
		if opts.Summary {
			output, err = renderer.RenderStatusSummary(v)
		} else {
			output, err = renderer.RenderStatus(v)
		}
		if err != nil {
			return fmt.Errorf("render commands status: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *apimodel.Stats:
		output, err := renderer.RenderStats(v)
		if err != nil {
			return fmt.Errorf("render stats: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *pkgmodel.Forma:
		output, err := renderer.RenderInventoryResources(v.Resources, opts.MaxResults)
		if err != nil {
			return fmt.Errorf("render inventory resources: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *[]*pkgmodel.Target:
		output, err := renderer.RenderInventoryTargets(*v, opts.MaxResults)
		if err != nil {
			return fmt.Errorf("render inventory targets: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *[]*pkgmodel.Stack:
		output, err := renderer.RenderInventoryStacks(*v, opts.MaxResults)
		if err != nil {
			return fmt.Errorf("render inventory stacks: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *[]apimodel.PolicyInventoryItem:
		output, err := renderer.RenderInventoryPolicies(*v, opts.MaxResults)
		if err != nil {
			return fmt.Errorf("render inventory policies: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	case *apimodel.CancelCommandResponse:
		output, err := renderer.RenderCancelCommandResponse(v)
		if err != nil {
			return fmt.Errorf("render cancel command response: %w", err)
		}
		_, err = p.w.Write([]byte(output))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}

	return nil
}
