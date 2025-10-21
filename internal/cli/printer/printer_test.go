// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package printer

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

type Person struct {
	Name      string    `json:"name" yaml:"name"`
	Age       int       `json:"age" yaml:"age"`
	Addresses []Address `json:"addresses" yaml:"addresses"`
}

type Address struct {
	Street string `json:"street" yaml:"street"`
	City   string `json:"city" yaml:"city"`
	State  string `json:"state" yaml:"state"`
	Zip    string `json:"zip" yaml:"zip"`
}

func TestMachineReadablePrinter(t *testing.T) {
	testObject := Person{
		Name: "John Doe",
		Age:  30,
		Addresses: []Address{
			{
				Street: "123 Main St",
				City:   "Anytown",
				State:  "CA",
				Zip:    "12345",
			},
			{
				Street: "456 Oak St",
				City:   "Othertown",
				State:  "TX",
				Zip:    "67890",
			},
		},
	}

	t.Run("prints json objects", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		printer := NewMachineReadablePrinter[Person](buf, "json")
		err := printer.Print(&testObject)
		assert.NoError(t, err)
		expected := `{"name":"John Doe","age":30,"addresses":[{"street":"123 Main St","city":"Anytown","state":"CA","zip":"12345"},{"street":"456 Oak St","city":"Othertown","state":"TX","zip":"67890"}]}` + "\n"
		assert.Equal(t, expected, buf.String())
	})

	t.Run("prints yaml", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		printer := NewMachineReadablePrinter[Person](buf, "yaml")
		err := printer.Print(&testObject)
		assert.NoError(t, err)

		// Verify it's valid YAML by unmarshaling it back
		var result Person
		err = yaml.Unmarshal(buf.Bytes(), &result)
		assert.NoError(t, err)
		assert.Equal(t, testObject, result)

		// Also verify it ends with a newline
		assert.True(t, bytes.HasSuffix(buf.Bytes(), []byte("\n")))
	})
}
