// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package imconc

import "github.com/sourcegraph/conc"

type ConcGroup struct {
	routines []Routine
	wg       *conc.WaitGroup
}

func NewConcGroup() *ConcGroup {
	return &ConcGroup{
		wg: &conc.WaitGroup{},
	}
}

func (c *ConcGroup) Add(routine Routine) *ConcGroup {
	c.routines = append(c.routines, routine)
	return c
}

func (c *ConcGroup) Go(fn func()) {
	c.wg.Go(fn)
}

func (c *ConcGroup) Stop(force bool) {
	for _, routine := range c.routines {
		routine.Stop(force)
	}
}

func (c *ConcGroup) Wait() {
	c.wg.Wait()
}
