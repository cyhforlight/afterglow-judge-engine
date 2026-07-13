package sandbox

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

type cpuPool chan int

func newCPUPool() (cpuPool, error) {
	var affinity unix.CPUSet
	if err := unix.SchedGetaffinity(0, &affinity); err != nil {
		return nil, fmt.Errorf("get CPU affinity: %w", err)
	}

	cpus := cpuIDsFromAffinity(&affinity)
	return newCPUPoolFromIDs(cpus)
}

func newCPUPoolFromIDs(cpus []int) (cpuPool, error) {
	if len(cpus) == 0 {
		return nil, errors.New("cpu affinity contains no available CPUs")
	}

	pool := make(cpuPool, len(cpus))
	for _, cpuID := range cpus {
		pool <- cpuID
	}
	return pool, nil
}

func cpuIDsFromAffinity(affinity *unix.CPUSet) []int {
	cpuCount := affinity.Count()
	cpus := make([]int, 0, cpuCount)
	for cpuID := 0; len(cpus) < cpuCount; cpuID++ {
		if affinity.IsSet(cpuID) {
			cpus = append(cpus, cpuID)
		}
	}
	return cpus
}

func (p cpuPool) acquire(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	select {
	case cpuID := <-p:
		return cpuID, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (p cpuPool) release(cpuID int) {
	p <- cpuID
}
