package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/client-go/machine/clientset/versioned"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

// Inspired from https://github.com/hybrid-cloud-patterns/patterns-operator/blob/main/controllers/pattern_controller.go#L293-L313
// See OCP API for Machine in https://docs.openshift.com/container-platform/latest/rest_api/machine_apis/machine-machine-openshift-io-v1beta1.html

const (
	clusterPlatformName = "cluster"
	machinesNamespace   = "openshift-machine-api"
	// For GetSecretData
	AWSSecretName            = "aws-cloud-fencing-credentials-secret"
	AWSSecretNamespace       = "openshift-operators"
	AWSAccessKeyID           = "aws_access_key_id"
	AWSSecretAccessKey       = "aws_secret_access_key"
	BMHCredentialNamespace   = "openshift-machine-api"
	BMHCredentialUserKey     = "username"
	BMHCredentialPasswordKey = "password"
)

// GetClusterInfo fetch the cluster's infrastructure object to identify its type
func GetClusterInfo(config configclient.Interface) (*configv1.Infrastructure, error) {
	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.metadata.name}'
	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.spec.platformSpec.type}'

	clusterInfra, err := config.ConfigV1().Infrastructures().Get(context.Background(), clusterPlatformName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return clusterInfra, nil
}

// GetSecretData searches for the platform's secret, and then returns its decoded two data values.
// E.g. on AWS it would be the Access Key and its ID, but on BMH with fence_impilan it would be useranme and password
func GetSecretData(clientSet *kubernetes.Clientset, secretName, secretNamespace, secretData1, secretData2 string) (string, string, error) {
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_access_key_id}' | base64 -d
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_secret_access_key}' | base64 -d

	secret, err := clientSet.CoreV1().Secrets(secretNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	return string(secret.Data[secretData1]), string(secret.Data[secretData2]), nil
}

// getNodeRoleFromMachine return node role "master/control-plane" or "worker" from machine label if present, otherwise "unknown"
func getNodeRoleFromMachine(nodeLabels map[string]string) string {
	machineLabelPrefixRole := "machine.openshift.io/cluster-api-machine-"
	// look for machine.openshift.io/cluster-api-machine-role or machine.openshift.io/cluster-api-machine-type label
	for _, labelKey := range []string{machineLabelPrefixRole + "role", machineLabelPrefixRole + "type"} {
		if labelVal, isFound := nodeLabels[labelKey]; isFound {
			if labelVal == "worker" {
				return "worker"
			}
			if labelVal == "master" {
				return "master/control-plane"
			}
		}
	}
	return "unknown"
}

// GetAWSNodeInfoList returns a list of the node names and their identification, e.g., AWS instance ID
func GetAWSNodeInfoList(machineClient *machineclient.Clientset) (map[v1alpha1.NodeName]string, error) {
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.spec.providerID}'
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.status.nodeRef.name}'

	nodeList := make(map[v1alpha1.NodeName]string)

	// Get the list of Machines in the openshift-machine-api namespace
	machineList, err := machineClient.MachineV1beta1().Machines(machinesNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nodeList, err
	}

	var missNodeMachineErr error
	missNodeMachineNames := ""
	// creates map for nodeName and AWS instance ID
	for _, machine := range machineList.Items {
		if machine.Status.NodeRef == nil || machine.Spec.ProviderID == nil {
			if missNodeMachineErr != nil {
				missNodeMachineNames += ", " + machine.ObjectMeta.GetName()
				missNodeMachineErr = fmt.Errorf("machines %s are not associated with any node or there provider ID is missing", missNodeMachineNames)
			} else {
				missNodeMachineNames = machine.ObjectMeta.GetName()
				missNodeMachineErr = fmt.Errorf("machine %s is not associated with any node or it's provider ID is missing", machine.ObjectMeta.GetName())
			}
		} else {
			nodeName := v1alpha1.NodeName(machine.Status.NodeRef.Name)
			nodeRole := getNodeRoleFromMachine(machine.Labels)
			providerID := *machine.Spec.ProviderID

			// Get the instance ID from the provider ID aws:///us-east-1b/i-082ac37ab919a82c2 -> i-082ac37ab919a82c2
			splitedProviderID := strings.Split(providerID, "/i-")
			instanceID := "i-" + splitedProviderID[1]
			nodeList[nodeName] = instanceID
			fmt.Printf("node: %s, Role: %s, Instance ID: %s \n", nodeName, nodeRole, instanceID)
		}
	}
	return nodeList, missNodeMachineErr
}

// GetBMHNodeInfoList returns a list of the node names and their identification, e.g., ports
func GetBMHNodeInfoList(machineClient *machineclient.Clientset) (map[v1alpha1.NodeName]string, error) {

	//TODO: seacrch for BM and fetch ports

	nodeList := map[v1alpha1.NodeName]string{
		"master-0": "6230",
		"master-1": "6231",
		"master-2": "6232",
		"worker-0": "6233",
		"worker-1": "6234",
		"worker-2": "6235",
	}
	return nodeList, nil
}

// GetPowerStatus returns node power status via machine-api
//
// We avoid using the fence agent's "status" action because it returns exit code 2 when the power is OFF.
// Handling this non-zero code would require extra logic in `execCommandOnPod`(in command.go),
// increasing complexity and risk of errors.
// Using the machine resource provides a simpler and safer way to check power status.
func GetPowerStatus(machineClient *machineclient.Clientset, targetNodename string) (string, error) {
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.status.providerStatus.instanceState}'

	machinename, err := findMachineByNodeName(machineClient, targetNodename)
	if err != nil {
		return "", fmt.Errorf("failed to get machine name %s: %w", targetNodename, err)
	}
	// Trigger reconcile for status
	err = triggerReconcileMachine(machineClient, machinename)

	if err != nil {
		return "", fmt.Errorf("failed to trigger reconcile: %w", err)
	}

	machine, err := machineClient.MachineV1beta1().Machines(machinesNamespace).Get(context.TODO(), machinename, metav1.GetOptions{})

	if err != nil || machine.Status.ProviderStatus == nil || machine.Status.ProviderStatus.Raw == nil {
		return "", fmt.Errorf("providerStatus is nil for machine %s", machinename)
	}

	var status struct {
		InstanceState *string `json:"instanceState,omitempty"`
	}

	if err := json.Unmarshal(machine.Status.ProviderStatus.Raw, &status); err != nil {
		return "", fmt.Errorf("failed to unmarshal providerStatus for machine %q: %w", targetNodename, err)
	}

	return *status.InstanceState, err
}

// findMachineByNodeName finds the Machine that matches the given Node name.
func findMachineByNodeName(machineClient *machineclient.Clientset, nodeName string) (string, error) {
	machineList, err := machineClient.MachineV1beta1().Machines(machinesNamespace).List(context.TODO(), metav1.ListOptions{})

	if err != nil {
		return "", err
	}

	for _, machine := range machineList.Items {
		for _, addr := range machine.Status.Addresses {
			if addr.Type == "Hostname" && addr.Address == nodeName {
				return machine.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no machine found for node name %q", nodeName)
}

// triggerReconcileMachine forces a reconciliation of the target Machine object
// by updating its metadata.labels with a unique value.
//
// GetPowerStatus retrieves the node's power status by querying the corresponding Machine object.
// To ensure the status is current, this function first triggers a reconciliation of the Machine resource.
// This helps speed up synchronization with the machine-api controller before reading the providerStatus.
func triggerReconcileMachine(machineClient *machineclient.Clientset, machineName string) error {
	// oc patch machine MACHINE_NAME -n openshift-machine-api -p '{"metadata":{"labels":{"far-test-reconcile-trigger-seq":"XXXXXX"}}}' --type=merge

	// Generate a sequence value based on timestamp
	seq := strconv.FormatInt(time.Now().UnixNano(), 10)

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{
				"far-test-reconcile-trigger-seq": seq,
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	_, err = machineClient.MachineV1beta1().Machines(machinesNamespace).Patch(
		context.TODO(),
		machineName,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to patch machine %s: %w", machineName, err)
	}

	return nil
}
