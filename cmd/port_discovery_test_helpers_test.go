package cmd

import "context"

// fakePortDiscoverer stubs local port discovery for up/guided-flow tests.
type fakePortDiscoverer struct {
	items []discoverItem
	err   error
}

func (f fakePortDiscoverer) ListListeningPorts(context.Context) ([]discoverItem, error) {
	return append([]discoverItem(nil), f.items...), f.err
}
