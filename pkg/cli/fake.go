package cli

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SuccessfulStatusCheckIp = "192.168.1.100"
	TimedOutStatusCheckIp   = "192.168.1.101"
	OffStatusCheckIp        = "192.168.1.102"
)

// NewFakeExecuter builds an Executer with configurable runnerFunc for testing
func NewFakeExecuter(client client.Client, fn runnerFunc, fakeRecorder *record.FakeRecorder) *Executer {
	logger := ctrl.Log.WithName("fakeExecuter")
	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]*routine),
		runner:   fn,
		recorder: fakeRecorder,
	}
}

func ControlTemplateRunner(ctx context.Context, command []string) (string, string, error) {
	// Extract IP if present: look for "--ip" flag and take the next argument
	ip := ""
	for i := 0; i < len(command); i++ {
		if command[i] == "--ip" && i+1 < len(command) {
			ip = command[i+1]
			break
		}
	}

	// Decide behavior based on IP
	switch ip {
	case SuccessfulStatusCheckIp:
		// success
		return "Status: ON\n", "", nil
	case TimedOutStatusCheckIp:
		// emulate timeout by waiting for context cancellation
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	case OffStatusCheckIp:
		// non-ON result
		return "Status: OFF\n", "", nil
	default:
		// default to success to keep other tests green
		return "Status: ON\n", "", nil
	}
}
