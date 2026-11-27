package noderings

import (
	"context"
	"fmt"
	"iter"
	"time"
)

// Collect reads an iterator, such as ListVirtualMachinesAll, into a slice.
// It stops at the first error and returns the items read so far with it.
func Collect[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var out []T
	for v, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, nil
}

// WaitOptions configure the Wait helpers.
type WaitOptions struct {
	// Interval between checks. Zero means 5 seconds.
	Interval time.Duration
	// Timeout for the whole wait. Zero means 15 minutes. The context's own
	// deadline also applies.
	Timeout time.Duration
}

func (o *WaitOptions) defaults() (time.Duration, time.Duration) {
	interval, timeout := 5*time.Second, 15*time.Minute
	if o != nil {
		if o.Interval > 0 {
			interval = o.Interval
		}
		if o.Timeout > 0 {
			timeout = o.Timeout
		}
	}
	return interval, timeout
}

// WaitForVirtualMachine polls a virtual machine until done returns true and
// returns the last state read. A NotFound error is returned as is, so waiting
// for a deletion can check IsNotFound.
func (c *Client) WaitForVirtualMachine(ctx context.Context, id string, opts *WaitOptions, done func(*VirtualMachine) bool) (*VirtualMachine, error) {
	interval, timeout := opts.defaults()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(interval)
	defer t.Stop()
	var last *VirtualMachine
	timedOut := func() (*VirtualMachine, error) {
		status := OperationStatusUnspecified
		if last != nil {
			status = last.OperationStatus
		}
		return last, fmt.Errorf("noderings: waiting for virtual machine %s: %w (last status %s)", id, ctx.Err(), status)
	}
	for {
		vm, err := c.VirtualMachine.GetVirtualMachine(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return timedOut()
			}
			return nil, err
		}
		last = vm
		if done(vm) {
			return vm, nil
		}
		select {
		case <-ctx.Done():
			return timedOut()
		case <-t.C:
		}
	}
}

// WaitForVirtualMachineStatus waits until the virtual machine is in one of
// the given statuses, for example OperationStatusStarted.
func (c *Client) WaitForVirtualMachineStatus(ctx context.Context, id string, opts *WaitOptions, statuses ...OperationStatus) (*VirtualMachine, error) {
	return c.WaitForVirtualMachine(ctx, id, opts, func(vm *VirtualMachine) bool {
		for _, s := range statuses {
			if vm.OperationStatus == s {
				return true
			}
		}
		return false
	})
}

// WaitForVirtualMachineDeleted waits until the virtual machine is gone.
func (c *Client) WaitForVirtualMachineDeleted(ctx context.Context, id string, opts *WaitOptions) error {
	_, err := c.WaitForVirtualMachine(ctx, id, opts, func(vm *VirtualMachine) bool {
		return vm.OperationStatus == OperationStatusDeleted
	})
	if IsNotFound(err) {
		return nil
	}
	return err
}
