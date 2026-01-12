/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
)

// FenceAgentsRemediationTemplateReconciler reconciles a FenceAgentsRemediationTemplate object
type FenceAgentsRemediationTemplateReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Executor *cli.Executer
}

// ParameterValidationResult contains the results of parameter validation
type ParameterValidationResult struct {
	IsSuccessful bool
	Message      string
}

const (
	parameterActionStatusValue    = "status"
	ConditionParametersValidation = "ParametersValidation"

	ReasonValidationInProgress = "ValidationInProgress"
	ReasonValidationSucceeded  = "ValidationSucceeded"
	ReasonValidationFailed     = "ValidationFailed"
)

var (
	// statusValidationTimeout is the maximum time allowed for a single status validation before it would time out.
	// Using 15 sec to be somewhere in the range of the 20 sec which fence agents like fence_ipmilan and fence_idrac default to as power_timeout for status and power-change operations https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/6/html/fence_configuration_guide/s1-software-fence-drac5-ca
	statusValidationTimeout = 15 * time.Second
)

//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the FenceAgentsRemediationTemplate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *FenceAgentsRemediationTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (finalResult ctrl.Result, finalErr error) {
	r.Log.Info("Begin FenceAgentsRemediationTemplate Reconcile")
	defer r.Log.Info("Finish FenceAgentsRemediationTemplate Reconcile")

	// Get the FenceAgentsRemediationTemplate instance
	fart := &v1alpha1.FenceAgentsRemediationTemplate{}
	if err := r.Get(ctx, req.NamespacedName, fart); err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Info("FenceAgentsRemediationTemplate CR was not found", "CR Name", req.Name, "CR Namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to get FenceAgentsRemediationTemplate CR")
		return ctrl.Result{}, err
	}
	orig := fart.DeepCopy()

	// At the end of each Reconcile we try to update CR's status
	defer func() {
		if updateErr := r.Status().Patch(ctx, fart, client.MergeFrom(orig)); updateErr != nil {
			if apiErrors.IsConflict(updateErr) {
				r.Log.Info("Conflict has occurred on updating the CR status")
			}
			finalErr = utilErrors.NewAggregate([]error{updateErr, finalErr})
		}
	}()

	return r.validateFenceStatusForTemplate(ctx, req, fart)
}

// validateFenceStatusForTemplate contains the template validation logic (extracted from Reconcile)
func (r *FenceAgentsRemediationTemplateReconciler) validateFenceStatusForTemplate(ctx context.Context, req ctrl.Request, fart *v1alpha1.FenceAgentsRemediationTemplate) (ctrl.Result, error) {
	if !r.isValidationRequired(fart) {
		return ctrl.Result{}, nil
	}

	spec := &fart.Spec.Template.Spec
	nodeNames := v1alpha1.GetNodeNamesFromSpec(spec)
	if len(nodeNames) == 0 {
		r.Log.Info("status validation skipped, no nodes found")
		return ctrl.Result{}, nil
	}
	sort.Strings(nodeNames)

	// Determine sampled nodes (optional) via spec.StatusValidationSample
	size, sampleErr := calculateSampleSize(len(nodeNames), spec.StatusValidationSample)
	if sampleErr != nil {
		r.Log.Error(sampleErr, "status validation failed, invalid value of StatusValidationSample", "StatusValidationSample", spec.StatusValidationSample)
		meta.SetStatusCondition(&fart.Status.Conditions, metav1.Condition{
			Type:               ConditionParametersValidation,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonValidationFailed,
			Message:            fmt.Sprintf("parameters validation failed invalid value of StatusValidationSample: %v", spec.StatusValidationSample),
			ObservedGeneration: fart.GetGeneration(),
		})
		// Configuration issue so no point to return an error
		return ctrl.Result{}, nil
	}

	selectedNodes := nodeNames[:size]

	cond := meta.FindStatusCondition(fart.Status.Conditions, ConditionParametersValidation)
	// Restart the validation if: 1. it's the first 2.Previous validation was completed and another is triggered by a user change 3.User change occurred when a validation was in progress
	if cond == nil || cond.Reason != ReasonValidationInProgress || cond.ObservedGeneration != fart.GetGeneration() {
		fart.Status.ValidationFailures = map[string]string{}
		fart.Status.ValidationPassed = map[string]string{}
		meta.SetStatusCondition(&fart.Status.Conditions, metav1.Condition{
			Type:               ConditionParametersValidation,
			Status:             metav1.ConditionUnknown,
			Reason:             ReasonValidationInProgress,
			Message:            fmt.Sprintf("validating parameters for %d node(s)", len(selectedNodes)),
			ObservedGeneration: fart.GetGeneration(),
		})
	}

	if fart.Status.ValidationFailures == nil {
		fart.Status.ValidationFailures = map[string]string{}
	}
	if fart.Status.ValidationPassed == nil {
		fart.Status.ValidationPassed = map[string]string{}
	}

	for _, n := range selectedNodes {
		if _, done := fart.Status.ValidationFailures[n]; done {
			continue
		}
		if _, done := fart.Status.ValidationPassed[n]; done {
			continue
		}
		tempFAR := &v1alpha1.FenceAgentsRemediation{
			ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: req.Namespace},
			Spec:       *spec,
		}
		params, _, err := v1alpha1.BuildFenceAgentParams(ctx, r.Client, tempFAR)
		if err != nil {
			fart.Status.ValidationFailures[n] = err.Error()
			return ctrl.Result{Requeue: true}, nil
		}

		res := r.runFenceStatus(ctx, spec.Agent, params)
		if !res.IsSuccessful {
			fart.Status.ValidationFailures[n] = res.Message
		} else {
			fart.Status.ValidationPassed[n] = "Success"
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Clear ValidationPassed; it was only used for tracking during reconciliation.
	// In case validation didn't pass we keep the failures reports only
	fart.Status.ValidationPassed = map[string]string{}
	allOK := len(fart.Status.ValidationFailures) == 0
	if allOK {
		meta.SetStatusCondition(&fart.Status.Conditions, metav1.Condition{
			Type:               ConditionParametersValidation,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonValidationSucceeded,
			Message:            "parameters validation succeeded",
			ObservedGeneration: fart.GetGeneration(),
		})
	} else {
		meta.SetStatusCondition(&fart.Status.Conditions, metav1.Condition{
			Type:               ConditionParametersValidation,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonValidationFailed,
			Message:            fmt.Sprintf("parameters validation failed for %d node(s)", len(fart.Status.ValidationFailures)),
			ObservedGeneration: fart.GetGeneration(),
		})
	}
	return ctrl.Result{}, nil
}

func (r *FenceAgentsRemediationTemplateReconciler) isValidationRequired(fart *v1alpha1.FenceAgentsRemediationTemplate) bool {
	validationStatus := meta.FindStatusCondition(fart.Status.Conditions, ConditionParametersValidation)
	if validationStatus == nil || validationStatus.Status == metav1.ConditionUnknown {
		return true
	}
	// If false, then condition isn't  validated for this spec
	return validationStatus.ObservedGeneration != fart.GetGeneration()
}

func calculateSampleSize(total int, sample *intstr.IntOrString) (int, error) {
	if sample == nil || total == 0 {
		return total, nil
	}
	// Treat -1 or lower (int) as all
	if sample.Type == intstr.Int && sample.IntVal < 0 {
		return total, nil
	}
	// Use k8s helper to scale int-or-percent
	scaled, err := intstr.GetScaledValueFromIntOrPercent(sample, total, true)
	if err != nil {
		return 0, err
	}
	if scaled < 0 || scaled > total {
		return 0, fmt.Errorf("invalid value for StatusValidationSample: %v", sample)
	}

	return scaled, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FenceAgentsRemediationTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FenceAgentsRemediationTemplate{}).
		Complete(r)
}

// runFenceStatus validates fence agent parameters by running a status command
func (r *FenceAgentsRemediationTemplateReconciler) runFenceStatus(ctx context.Context, agent string, parameters map[v1alpha1.ParameterName]string) *ParameterValidationResult {
	result := &ParameterValidationResult{
		IsSuccessful: false,
		Message:      "",
	}

	// Build command with status action
	command := []string{agent, v1alpha1.ParameterActionName, parameterActionStatusValue}

	// Add parameters (excluding action parameters to avoid conflicts)
	for paramName, paramValue := range parameters {
		if string(paramName) != v1alpha1.ActionName && string(paramName) != v1alpha1.ParameterActionName {
			command = append(command, string(paramName))
			if paramValue != "" {
				command = append(command, paramValue)
			}
		}
	}

	r.Log.Info("Testing fence agent status command", "Fence Agent", agent, "Parameters", slices.Collect(maps.Keys(parameters)))

	stdout, stderr, retryErr, cmdErr := r.Executor.SyncExecute(ctx, command, 1, 0, statusValidationTimeout)

	if retryErr != nil {
		if wait.Interrupted(retryErr) {
			result.Message = fmt.Sprintf("status command timed out after %v", statusValidationTimeout)
			r.Log.Info("runFenceStatus status command timed out", "result", result)
			return result
		}

		result.Message = fmt.Sprintf("fence agent command retry failed: %v (stderr: %s, stdout: %s)", retryErr, stderr, stdout)
		r.Log.Error(retryErr, cli.FenceAgentRetryErrorMessage)
		return result
	}

	if cmdErr != nil {
		result.Message = fmt.Sprintf("fence agent command failed: %v (stderr: %s, stdout: %s)", cmdErr, stderr, stdout)
		r.Log.Info(cli.FenceAgentFailedCommandMessage, "response", stdout, "errMessage", stderr, "err", cmdErr)

		return result
	}

	// Check for both "Status: ON" (fence_ipmilan) and standalone "On" (fence_redfish)
	upperOutput := strings.ToUpper(stdout)
	if strings.Contains(upperOutput, "STATUS: ON") ||
		strings.Contains(upperOutput, "STATUS:ON") ||
		strings.HasPrefix(strings.TrimSpace(upperOutput), "ON") {
		result.IsSuccessful = true
		r.Log.Info("Fence agent status command succeeded with Status: ON", "agent", agent, "stdout", stdout)
		return result
	}

	result.Message = fmt.Sprintf("fence agent command completed but status is not ON (stdout: %s, stderr: %s)", stdout, stderr)
	r.Log.Info("Fence agent status command completed but status not ON", "agent", agent, "stdout", stdout, "stderr", stderr)
	return result
}
