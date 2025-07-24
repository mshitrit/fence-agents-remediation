package validation

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

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
)

type OutOfServiceTaintValidator struct {
	isOutOfServiceTaintSupported bool
}

type AgentExists func(string) (bool, error)
type validateAgentExistence struct {
	agentExists AgentExists
}

// isAgentFileExists returns true if the agent name matches a binary, and false otherwise
func isAgentFileExists(agent string) (bool, error) {
	directory := "/usr/sbin/"
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
