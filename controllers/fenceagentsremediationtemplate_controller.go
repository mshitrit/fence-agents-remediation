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
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	ConditionParametersValidation = "ParametersValidation"

	ReasonValidationInProgress = "ValidationInProgress"
	ReasonValidationSucceeded  = "ValidationSucceeded"
	ReasonValidationFailed     = "ValidationFailed"
)

//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediationtemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediationtemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fence-agents.medik8s.io,resources=fenceagentsremediationtemplates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the FenceAgentsRemediationTemplate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *FenceAgentsRemediationTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log.Info("Begin FenceAgentsRemediationTemplate Reconcile")
	defer r.Log.Info("Finish FenceAgentsRemediationTemplate Reconcile")

	// Get the FenceAgentsRemediation instance
	fart := &v1alpha1.FenceAgentsRemediationTemplate{}
	if err := r.Get(ctx, req.NamespacedName, fart); err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Info("FenceAgentsRemediationTemplate CR was not found", "CR Name", req.Name, "CR Namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to get FenceAgentsRemediationTemplate CR")
		return ctrl.Result{}, err
	}
	// Collect all unique node names from NodeParameters and NodeSecretNames
	spec := &fart.Spec.Template.Spec
	nodeNames := v1alpha1.GetNodeNamesFromSpec(spec)

	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		r.Log.Info("status validation skipped, no nodes found")
		return ctrl.Result{}, nil
	}

	if fart.Status.ValidationFailures == nil {
		fart.Status.ValidationFailures = make(map[string]string)
	}

	original := fart.DeepCopy()
	meta.SetStatusCondition(&fart.Status.Conditions, metav1.Condition{
		Type:               ConditionParametersValidation,
		Status:             metav1.ConditionUnknown,
		Reason:             ReasonValidationInProgress,
		Message:            fmt.Sprintf("validating parameters for %d node(s)", len(nodeNames)),
		ObservedGeneration: fart.GetGeneration(),
	})
	if err := r.Status().Patch(ctx, fart, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}

	original = fart.DeepCopy()

	// Validate parameters for each node mentioned in NodeParameters
	for _, nodeName := range nodeNames {
		// Generate a temporary FAR CR from the template for this specific node
		tempFAR := &v1alpha1.FenceAgentsRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: req.Namespace,
			},
			Spec: *spec,
		}

		// BuildFenceAgentParams handles secret collection and validation internally
		completeParams, _, err := v1alpha1.BuildFenceAgentParams(ctx, r.Client, tempFAR)
		if err != nil {
			// If BuildFenceAgentParams fails, return the validation error
			return ctrl.Result{}, err
		}

		// Validate the complete parameter set with status command
		result := r.validateParametersWithStatus(ctx, spec.Agent, completeParams)
		if result.IsSuccessful {
			delete(fart.Status.ValidationFailures, nodeName)
		} else {
			fart.Status.ValidationFailures[nodeName] = result.Message
		}
	}

	if len(fart.Status.ValidationFailures) == 0 {
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
	if err := r.Status().Patch(ctx, fart, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FenceAgentsRemediationTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FenceAgentsRemediationTemplate{}).
		Complete(r)
}

// validateParametersWithStatus validates fence agent parameters by running a status command
func (r *FenceAgentsRemediationTemplateReconciler) validateParametersWithStatus(ctx context.Context, agent string, parameters map[v1alpha1.ParameterName]string) *ParameterValidationResult {
	result := &ParameterValidationResult{
		IsSuccessful: true,
		Message:      "",
	}

	// Build command with status action
	command := []string{agent, v1alpha1.ParameterActionName, v1alpha1.ParameterActionStatusValue}

	// Add parameters (excluding action parameters to avoid conflicts)
	for paramName, paramValue := range parameters {
		if string(paramName) != v1alpha1.ActionName && string(paramName) != v1alpha1.ParameterActionName {
			command = append(command, string(paramName), paramValue)
		}
	}

	// Run the status command with timeout
	ctxWithTimeout, cancel := context.WithTimeout(ctx, v1alpha1.StatusValidationTimeout)
	defer cancel()

	r.Log.Info("Testing fence agent status command", "agent", agent, "command", command)

	stdout, stderr, err := r.Executor.RunCommand(ctxWithTimeout, command[0], command[1:]...)

	if err != nil {
		result.IsSuccessful = false
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			result.Message = fmt.Sprintf("status command timed out after %v", v1alpha1.StatusValidationTimeout)
			r.Log.Info("validateParametersWithStatus status command timed out", "result", result)
			return result
		}

		result.Message = fmt.Sprintf("fence agent command failed: %v (stderr: %s, stdout: %s)", err, stderr, stdout)
		r.Log.Info("validateParametersWithStatus status command failed", "result", result)
		return result
	}

	// Command completed successfully, now check if stdout contains "Status: ON"
	if strings.Contains(stdout, "Status: ON") {
		r.Log.Info("Fence agent status command succeeded with Status: ON", "agent", agent, "stdout", stdout)
	} else {
		result.Message = fmt.Sprintf("fence agent command completed but status is not ON (stdout: %s, stderr: %s)", stdout, stderr)
		r.Log.Info("Fence agent status command completed but status not ON", "agent", agent, "stdout", stdout, "stderr", stderr)
	}

	return result
}
