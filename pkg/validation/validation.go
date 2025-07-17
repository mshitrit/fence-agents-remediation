package validation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	loggerValidation = ctrl.Log.WithName("validation")
	leadingDigits    = regexp.MustCompile(`^(\d+)`)
)

const (
	//out of service taint strategy const (supported from 1.26)
	minK8sMajorVersionOutOfServiceTaint = 1
	minK8sMinorVersionOutOfServiceTaint = 26

	// Parameter validation constants
	parameterValidationTimeout = 30 * time.Second
	fenceAgentsDirectory       = "/usr/sbin/"

	ParameterActionName            = "--" + actionName
	actionName                     = "action"
	ParameterActionValue           = "reboot"
	errorParamDefinedMultipleTimes = "invalid multiple definition of FAR param"
)

type ParameterName string
type NodeName string

type OutOfServiceTaintValidator struct {
	isOutOfServiceTaintSupported bool
}

type AgentExists func(string) (bool, error)
type validateAgentExistence struct {
	agentExists AgentExists
}

// ParameterValidationResult contains the results of parameter validation
type ParameterValidationResult struct {
	IsValid      bool
	Errors       []string
	Warnings     []string
	StatusOutput string
}

// FenceAgentParameterValidator validates fence agent parameters
type FenceAgentParameterValidator struct {
	timeout time.Duration
}

// NewFenceAgentParameterValidator creates a new parameter validator
func NewFenceAgentParameterValidator() *FenceAgentParameterValidator {
	return &FenceAgentParameterValidator{
		timeout: parameterValidationTimeout,
	}
}

// ValidateParametersWithStatus validates fence agent parameters by running a status command
func (v *FenceAgentParameterValidator) ValidateParametersWithStatus(agent string, parameters map[string]string) (*ParameterValidationResult, error) {
	//TODO mshitrit make sure this returns only error
	result := &ParameterValidationResult{
		IsValid:  true,
		Errors:   []string{},
		Warnings: []string{},
	}

	if agent == "" {
		result.IsValid = false
		result.Errors = append(result.Errors, "empty agent name")
		return result, nil
	}

	// Build command with status action
	command := []string{agent, "--action", "status"}

	// Add parameters (excluding action parameters to avoid conflicts)
	for paramName, paramValue := range parameters {
		if paramName != "action" && paramName != "--action" {
			command = append(command, fmt.Sprintf("--%s", paramName), paramValue)
		}
	}

	// Run the status command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
	defer cancel()

	loggerValidation.Info("Testing fence agent status command", "agent", agent, "command", command)

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var outBuilder, errBuilder strings.Builder
	cmd.Stdout = &outBuilder
	cmd.Stderr = &errBuilder

	err := cmd.Run()
	stdout := outBuilder.String()
	stderr := errBuilder.String()
	result.StatusOutput = stdout

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Warnings = append(result.Warnings, fmt.Sprintf("status command timed out after %v", v.timeout))
			return result, nil
		}

		// Check if it's a parameter-related error vs connectivity error
		stderrLower := strings.ToLower(stderr)
		stdoutLower := strings.ToLower(stdout)

		// Parameter validation errors (hard failures)
		parameterErrors := []string{
			"unrecognized", "invalid", "unknown option", "unknown argument",
			"required argument", "missing argument", "bad parameter",
		}

		isParameterError := false
		for _, errPattern := range parameterErrors {
			if strings.Contains(stderrLower, errPattern) || strings.Contains(stdoutLower, errPattern) {
				isParameterError = true
				break
			}
		}

		if isParameterError {
			result.IsValid = false
			result.Errors = append(result.Errors, fmt.Sprintf("fence agent parameter validation failed: %v (stderr: %s, stdout: %s)", err, stderr, stdout))
		} else {
			// Connectivity or other runtime errors (warnings only)
			result.Warnings = append(result.Warnings, fmt.Sprintf("fence agent connectivity test failed (this may be expected): %v", err))
		}

		return result, nil
	}

	loggerValidation.Info("Fence agent status command succeeded", "agent", agent, "stdout", stdout)
	return result, nil
}

// ValidateActionParameter validates that action parameters are set correctly
func ValidateActionParameter(paramName, paramVal string, logger logr.Logger) error {
	if (paramName == actionName || paramName == ParameterActionName) && paramVal != "" && paramVal != ParameterActionValue {
		// --action parameter with a different value from reboot is not supported
		err := fmt.Errorf("FAR doesn't support any other action than reboot")
		logger.Error(err, "can't build CR with this action attribute", "action", paramVal)
		return err
	}
	return nil
}

func ValidateUniqueParam(fenceAgentParamNames map[ParameterName]string, paramName ParameterName, logger logr.Logger) error {
	if _, exist := fenceAgentParamNames[paramName]; exist {
		err := errors.New(errorParamDefinedMultipleTimes)
		logger.Error(err, "can't build fence agents params a param is defined multiple times", "param name", paramName)
		return err
	}
	return nil
}

// isAgentFileExists returns true if the agent name matches a binary, and false otherwise
func isAgentFileExists(agent string) (bool, error) {
	directory := fenceAgentsDirectory
	// Create the full path by joining the directory and filename
	fullPath := filepath.Join(directory, agent)

	// Check if the file exists
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("error checking file: %w", err)
}

type AgentValidator interface {
	ValidateAgentName(agent string) (bool, error)
}

func NewAgentValidator() AgentValidator {
	return &validateAgentExistence{agentExists: isAgentFileExists}
}

func NewCustomAgentValidator(agentExists AgentExists) AgentValidator {
	return &validateAgentExistence{agentExists: agentExists}
}

func (vfe *validateAgentExistence) ValidateAgentName(agent string) (bool, error) {
	return vfe.agentExists(agent)
}

// NewOutOfServiceTaintValidator returns a validator to check if out-of-service taint
// is supporetd on the cluster
func NewOutOfServiceTaintValidator(config *rest.Config) (*OutOfServiceTaintValidator, error) {
	v := &OutOfServiceTaintValidator{}

	if cs, err := kubernetes.NewForConfig(config); err != nil || cs == nil {
		if cs == nil {
			err = fmt.Errorf("k8s client set is nil")
		}
		loggerValidation.Error(err, "couldn't retrieve k8s client")
		return nil, err
	} else if k8sVersion, err := cs.Discovery().ServerVersion(); err != nil || k8sVersion == nil {
		if k8sVersion == nil {
			err = fmt.Errorf("k8s server version is nil")
		}
		loggerValidation.Error(err, "couldn't retrieve k8s server version")
		return nil, err
	} else {
		if err = v.setOutOfServiceTaintSupportedFlag(k8sVersion); err != nil {
			return nil, err
		}
		return v, nil
	}
}

// IsOutOfServiceTaintSupported returns if the cluster supports out-of-service taint
func (v *OutOfServiceTaintValidator) IsOutOfServiceTaintSupported() bool {
	return v.isOutOfServiceTaintSupported
}

func (v *OutOfServiceTaintValidator) setOutOfServiceTaintSupportedFlag(version *version.Info) error {
	var majorVer, minorVer int
	var err error
	if majorVer, err = strconv.Atoi(version.Major); err != nil {
		loggerValidation.Error(err, "couldn't parse k8s major version", "major version", version.Major)
		return err
	}
	if minorVer, err = strconv.Atoi(leadingDigits.FindString(version.Minor)); err != nil {
		loggerValidation.Error(err, "couldn't parse k8s minor version", "minor version", version.Minor)
		return err
	}

	v.isOutOfServiceTaintSupported = majorVer > minK8sMajorVersionOutOfServiceTaint || (majorVer == minK8sMajorVersionOutOfServiceTaint && minorVer >= minK8sMinorVersionOutOfServiceTaint)
	loggerValidation.Info("out of service taint strategy", "isSupported", v.isOutOfServiceTaintSupported, "k8sMajorVersion", majorVer, "k8sMinorVersion", minorVer)
	return nil
}

// TODO mshitrit add logger here
// ValidateFenceAgentParams validates all fence agent parameters without building the map
func ValidateFenceAgentParams(
	sharedParameters map[ParameterName]string,
	nodeParameters map[ParameterName]map[NodeName]string,
	secretParams map[string]string,
	nodeName string,
	logger logr.Logger,
) error {
	// Track parameter names for uniqueness validation
	existingParams := make(map[ParameterName]bool)

	// Validate shared parameters
	for paramName, paramVal := range sharedParameters {
		// Verify action must be reboot
		if err := ValidateActionParameter(string(paramName), paramVal, logger); err != nil {
			return err
		}
		// Verify param isn't already defined
		if existingParams[paramName] {
			err := errors.New(errorParamDefinedMultipleTimes)
			logger.Error(err, "can't build fence agents params a param is defined multiple times", "param name", paramName)
			return err
		}
		existingParams[paramName] = true
	}

	// Validate node parameters
	for paramName, nodeMap := range nodeParameters {
		if nodeVal, isFound := nodeMap[NodeName(nodeName)]; isFound {
			// Verify action must be reboot
			if err := ValidateActionParameter(string(paramName), nodeVal, logger); err != nil {
				return err
			}
			// For node params we don't enforce uniqueness as node param value will override shared param
			existingParams[paramName] = true
		}
	}

	//TODO mshitrit merge template validation logic here
	// Validate secret parameters
	for secretKey, secretVal := range secretParams {
		secretParam := ParameterName(secretKey)
		// Verify action must be reboot
		if err := ValidateActionParameter(string(secretParam), secretVal, logger); err != nil {
			return err
		}
		if existingParams[secretParam] {
			err := errors.New(errorParamDefinedMultipleTimes)
			logger.Error(err, "can't build fence agents params a param is defined multiple times", "param name", secretParam)
			return err
		}
		existingParams[secretParam] = true
	}

	return nil
}
