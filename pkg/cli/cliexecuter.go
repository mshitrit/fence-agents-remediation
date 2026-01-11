package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	commonEvents "github.com/medik8s/common/pkg/events"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	FenceAgentContextCanceledMessage = "fence agent context canceled. Nothing to do"
	FenceAgentContextTimedOutMessage = "fence agent context timed out"
	FenceAgentRetryErrorMessage      = "fence agent retry error"
	FenceAgentFailedCommandMessage   = "command failed"
)

type routine struct {
	cancel context.CancelFunc
}

type Executer struct {
	client.Client
	log          logr.Logger
	routines     map[types.UID]*routine
	routinesLock sync.Mutex
	runner       runnerFunc
	recorder     record.EventRecorder
}

// runnerFunc is a function that runs the command and returns the stdout, stderr and error
// it is configurable in Executer for testing purposes
type runnerFunc func(ctx context.Context, command []string) (string, string, error)

// NewExecuter builds the Executer
func NewExecuter(client client.Client, newRecorder record.EventRecorder) (*Executer, error) {
	logger := ctrl.Log.WithName("executer")

	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]*routine),
		runner:   run,
		recorder: newRecorder,
	}, nil
}

// AsyncExecute runs the command in a goroutine mapped to the UID
func (e *Executer) AsyncExecute(ctx context.Context, uid types.UID, command []string, retryCount int, retryInterval, timeout time.Duration) {
	e.routinesLock.Lock()
	defer e.routinesLock.Unlock()
	if _, exist := e.routines[uid]; exist {
		return
	}

	// create a context for the fence agent command that the controller can cancel
	cancellableCtx, cancel := context.WithCancel(ctx)
	routine := routine{
		cancel: cancel,
	}
	e.routines[uid] = &routine

	go e.fenceAgentRoutine(cancellableCtx, uid, command, retryCount, retryInterval, timeout)
}

func (e *Executer) SyncExecute(ctx context.Context, command []string, retryCount int, retryInterval, timeout time.Duration) (string, string, error, error) {
	fakeUid := types.UID("")
	return e.runWithRetry(ctx, fakeUid, command, retryCount, retryInterval, timeout)
}

func (e *Executer) fenceAgentRoutine(ctx context.Context, uid types.UID, command []string, retryCount int, retryInterval, timeout time.Duration) {
	// run the command and update the status
	_, _, retryErr, cmdErr := e.runWithRetry(ctx, uid, command, retryCount, retryInterval, timeout)
	if retryErr != nil {
		switch {
		case errors.Is(retryErr, context.Canceled):
			e.log.Info(FenceAgentContextCanceledMessage)
			return
		case wait.Interrupted(retryErr):
			e.log.Info(FenceAgentContextTimedOutMessage)
		default:
			e.log.Error(retryErr, FenceAgentRetryErrorMessage)
		}
	}

	if err := e.updateStatusWithRetry(ctx, uid, cmdErr); err != nil {
		switch {
		case wait.Interrupted(err):
			e.log.Info("status context timed out")
		default:
			e.log.Error(err, "status retry error")
		}
	}
}

func (e *Executer) runWithRetry(ctx context.Context, uid types.UID, command []string, retryCount int, retryInterval, timeout time.Duration) (stdout, stderr string, retryErr, faErr error) {
	// Run the command with an exponential backoff retry to handle the following cases:
	// - the command fails: the command is retried until the retryCount is reached
	// - the command times out: the command is retried until the retryCount is reached
	// - the FA context times out: the command is cancelled and the status is updated
	// - the FA context is cancelled: the command is cancelled and the status is not updated
	// - the command succeeds: the command is not retried and the status is updated

	backoff := wait.Backoff{
		Steps:    retryCount,
		Duration: retryInterval,
		Factor:   1.0,
	}

	if len(command) == 0 {
		return "", "", nil, fmt.Errorf("command is empty")
	}

	e.log.Info("fence agent start", "uid", uid, "fence_agent", command[0], "retryCount", retryCount, "retryInterval", retryInterval, "timeout", timeout)

	retryErr = wait.ExponentialBackoffWithContext(ctx,
		backoff,
		func(ctx context.Context) (bool, error) {
			ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			stdout, stderr, faErr = e.runner(ctxWithTimeout, command)
			if faErr == nil {
				e.log.Info("command completed", "uid", uid, "response", stdout, "errMessage", stderr, "err", faErr)
				return true, nil
			}

			if wait.Interrupted(faErr) {
				e.log.Error(faErr, "fence agent timeout", "uid", uid)
				return false, faErr
			}

			e.log.Info(FenceAgentFailedCommandMessage, "uid", uid, "response", stdout, "errMessage", stderr, "err", faErr)
			return false, nil
		})

	e.log.Info("fence agent done", "uid", uid, "fence_agent", command[0], "stdout", stdout, "stderr", stderr, "err", faErr)
	return stdout, stderr, retryErr, faErr
}

func (e *Executer) updateStatusWithRetry(ctx context.Context, uid types.UID, fenceAgentErr error) error {
	// Update FAR status with an exponential backoff retry to handle only the updateStatus error cases where:
	// - FAR cannot be found, but it does exist
	// - the status update fails for conflicts

	e.log.Info("updating status", "FAR uid", uid)

	err := wait.ExponentialBackoffWithContext(ctx,
		retry.DefaultBackoff,
		func(ctx context.Context) (bool, error) {
			far, err := e.getFenceAgentsRemediationByUID(ctx, uid)
			if err != nil {
				if apiErrors.IsNotFound(err) {
					e.log.Info("no FAR with the given UID", "FAR uid", uid)
					return false, err
				}

				if wait.Interrupted(err) {
					e.log.Info("context cancelled while getting FAR to update its status", "FAR uid")
					return false, err
				}

				e.log.Error(err, "could not get FAR by uid", "FAR uid", uid)
				return false, err
			}

			if err := e.updateStatus(ctx, far, fenceAgentErr); err != nil {
				if wait.Interrupted(err) {
					e.log.Info("context cancelled while updating the status", "FAR uid", uid)
					return false, err
				}
				if apiErrors.IsConflict(err) {
					e.log.Error(err, "conflict while updating the status", "FAR uid", uid)
					return false, nil
				}
				e.log.Error(err, "failed to update status", "FAR uid", uid)
				return false, err
			}

			e.log.Info("status updated", "FAR uid", uid)
			return true, nil
		})
	return err
}

// Exists checks if there is already a running Fence Agent command mapped to the UID
func (e *Executer) Exists(uid types.UID) bool {
	e.routinesLock.Lock()
	defer e.routinesLock.Unlock()
	_, exist := e.routines[uid]
	return exist
}

func (e *Executer) Remove(uid types.UID) {
	e.routinesLock.Lock()
	defer e.routinesLock.Unlock()
	if routine, exist := e.routines[uid]; exist {
		e.log.Info("cancelling fence agent routine", "uid", uid)
		routine.cancel()
		delete(e.routines, uid)
	}
}

// run runs the command in the container and updates the status of the FAR instance maching the UID
func run(ctx context.Context, command []string) (stdout, stderr string, err error) {
	if len(command) == 0 {
		return "", "", fmt.Errorf("command is empty")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	var outBuilder, errBuilder strings.Builder
	cmd.Stdout = &outBuilder
	cmd.Stderr = &errBuilder

	err = cmd.Run()

	return outBuilder.String(), errBuilder.String(), err
}

func (e *Executer) getFenceAgentsRemediationByUID(ctx context.Context, uid types.UID) (*v1alpha1.FenceAgentsRemediation, error) {
	farList := &v1alpha1.FenceAgentsRemediationList{}
	if err := e.List(ctx, farList, &client.ListOptions{}); err != nil || len(farList.Items) == 0 {
		e.log.Error(err, "failed to list FAR", "FAR uid", uid)
		return nil, err
	}

	for _, far := range farList.Items {
		if far.UID == uid {
			return &far, nil
		}
	}

	err := fmt.Errorf("could not find any FAR matching the UID")
	e.log.Error(err, "failed to get far", "uid", uid)

	return nil, err
}

func (e *Executer) updateStatus(ctx context.Context, far *v1alpha1.FenceAgentsRemediation, err error) error {
	var reason utils.ConditionsChangeReason

	if err == nil {
		reason = utils.FenceAgentSucceeded
		commonEvents.NormalEvent(e.recorder, far, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
	} else if wait.Interrupted(err) {
		reason = utils.FenceAgentTimedOut
	} else {
		reason = utils.FenceAgentFailed
	}

	utils.UpdateConditions(reason, far, e.log)
	return e.Status().Update(ctx, far)
}
