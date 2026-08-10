//go:build !linux && !darwin && !windows

package process

import (
	"context"
	"time"
)

// walkProcessTree returns "unknown" on unsupported platforms.
func walkProcessTree(_ context.Context, _ rootIdentity, _ time.Time) string {
	return probeResultUnknown
}

// captureRootIdentity always fails on unsupported platforms.
func captureRootIdentity(_ int) (rootIdentity, bool) {
	return rootIdentity{}, false
}
