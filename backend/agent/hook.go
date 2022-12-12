package agent

import (
	"context"
	"reflect"

	agentapi "isc.org/stork/api"
	"isc.org/stork/hooks/agent/forwardtokeaoverhttpcallouts"
	"isc.org/stork/hooksutil"
)

// Facade for all callout points. It defines the specific calling method for
// each callout point.
type HookManager struct {
	hooksutil.HookManager
}

// Interface checks.
var _ forwardtokeaoverhttpcallouts.BeforeForwardToKeaOverHTTPCallouts = (*HookManager)(nil)

// Constructs new hook manager.
func NewHookManager() *HookManager {
	return &HookManager{
		HookManager: *hooksutil.NewHookManager([]reflect.Type{
			reflect.TypeOf((*forwardtokeaoverhttpcallouts.BeforeForwardToKeaOverHTTPCallouts)(nil)).Elem(),
		}),
	}
}

// Callout point executed before forwarding a command to Kea over HTTP.
func (hm *HookManager) OnBeforeForwardToKeaOverHTTP(ctx context.Context, in *agentapi.ForwardToKeaOverHTTPReq) {
	hooksutil.CallSequential(hm.GetExecutor(), func(callout forwardtokeaoverhttpcallouts.BeforeForwardToKeaOverHTTPCallouts) int {
		callout.OnBeforeForwardToKeaOverHTTP(ctx, in)
		return 0
	})
}
