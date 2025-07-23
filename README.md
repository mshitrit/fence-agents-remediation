# Fence Agents Remediation (FAR)

fence-agents-remediation (*FAR*) is a Kubernetes operator that uses [well-known agents](https://github.com/ClusterLabs/fence-agents) to fence and remediate unhealthy nodes. The remediation includes rebooting the unhealthy node using a fence agent, and then evicting workloads from the unhealthy node. The operator is recommended when a node becomes unhealthy, and we want remediate it by completely isolating the node from a cluster and help with recovering its workload. Isolation is needed, since we can’t “trust” the unhealthy node, to prevent it from accessing the shared resources like [RWO volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes), and recovering the workloads helps to accelerate and keep their running time.

FAR is one of the remediator operators by [Medik8s](https://www.medik8s.io/remediation/remediation/), such as [Self Node Remediation](https://github.com/medik8s/self-node-remediation) and [Machine Deletion Remediation](https://github.com/medik8s/machine-deletion-remediation), that were designed to run with the Node HealthCheck Operator [(NHC)](https://github.com/medik8s/node-healthcheck-operator) which detects an unhealthy node and creates remediation Custom Resource ([CR](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)). It is recommended to use FAR with NHC for an easier and smoother experience by fully automating the remediation process, but it can be used as a standalone remediator for the more experienced user. Moreover, like other Medik8s operators FAR was generated using the [operator-sdk](https://github.com/operator-framework/operator-sdk), and it supports Operator Lifecycle Manager ([OLM](https://olm.operatorframework.io/docs/)).

## About Fence Agents

FAR uses a fence agent to fence a Kubernetes node. Generally, fencing is the process of taking unresponsive/unhealthy computers into a safe state and isolating the computer. Fence agent is a software "driver" which is able to prevent nodes from destroying data on shared storage, and it aimed for isolating corrupted nodes. The isolation with FAR is mostly power-based fencing which enables power-cycling, resetting, or turning off the computer.

FAR uses some of the fence agents from the [upstream repository](https://github.com/ClusterLabs/fence-agents) by the *ClusterLabs* group. For example, `fence_ipmilan` for Intelligent Platform Management Interface ([IPMI](https://en.wikipedia.org/wiki/Intelligent_Platform_Management_Interface)) environments or `fence_aws` for Amazon Web Services ([AWS](https://aws.amazon.com)) platform. These upstream fence agents are Python scripts that are used to isolate a corrupted node from the rest of the cluster in a power-based fencing method. When a node is switched off, it cannot corrupt any data on shared storage. The fence agents use command-line arguments rather than configuration files, and to understand better the parameters you can view the fence agent's metadata (e.g., `fence_ipmilan -o metadata`).

## Advantages

* Robustness - FAR has direct feedback from the agent's management Application Programming Interface (API) call (e.g., IPMI) about the result of the fence action without using the Kubernetes API.
* Speed - FAR is rapid since it can reboot a node and receive an acknowledgment from the API call while other remediators might need to wait a safe time till they can expect the node to be rebooted.
* Availability - FAR has high availability by running with two replicas of its pod, and when the leader of these two pods is evicted, then the other one takes control and reduces FAR downtime.
* Diversity - FAR includes several fence agents from a large known set of upstream fencing agents for bare metal servers, virtual machines, cloud platforms, etc.
* Adjustability - FAR allows to set up different parameters for running the API call that remediates the node.

## How does FAR work?

The operator watches for new or deleted CRs called `FenceAgentsRemediation` (or `far`) which trigger remediation for the node, based on the CR's name. When the CR name doesn't match a node in the cluster, then the CR won't trigger any remediation by FAR. Remediation includes adding a taint on the node, rebooting the node by fence agent, and at last deleting the remaining workloads.

FAR remediates by simply rebooting the unhealthy node, and moving any remaining workloads to other nodes, so they can continue running and be isolated from the unhealthy node. The reboot is done by executing a fence agent for the unhealthy node while evicting the workloads from this node is achieved by tainting the node and deleting the workloads. FAR unique taint, `medik8s.io/fence-agents-remediation`, has a [NoExecute effect](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/#taint-based-evictions), so  any pods that don't tolerate this taint are evicted immediately, and they won't be scheduled again after the node has been rebooted as long as the taint remains (the taint is removed on FenceAgentsRemediation CR deletion). Deleting the workloads is done to speed up Kubernetes rescheduling of the remaining pods (most likely [stateful pods](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#using-statefulsets)), that are not running anymore.

FAR includes the `FenceAgentsRemediationTemplate` (or `fartemplate`) Custom Resource Definition ([CRD](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#create-a-customresourcedefinition)) for how to create a FenceAgentsRemediation CR. The template has the same fields as far CR (e.g., agent name) and it is used for automatically creating remediation CR by another operator/mechanism (e.g., [NHC](#far-with-nhc)). The other operator is responsible of creating (and eventually deleting) the FenceAgentsRemediation CR with the name of the unhealthy node, even though FAR can be used manually without fartemplate and an additional operator (see [standalone FAR](#standalone-far)).

### Operator Workflow

#### Prerequisites

* FAR and NHC are installed on the cluster.
* One of the nodes fails (it has become unhealthy), and NHC detects this node as unhealthy and decides to create a remediation CR, FenceAgentsRemediation CR, based on the external remediator template (e.g., fartemplate).

#### Workflow

1. FAR adds NoExecute taint to the failed node
=> Ensure that any workloads are not executed after rebooting the failed node, and any stateless pods (that can’t tolerate FAR NoExecute taint) will be evicted immediately
2. FAR reboots the failed node via the Fence Agent
=> After rebooting, there are no workloads in the failed node
3. FAR forcefully deletes the pods in the failed node
=> The scheduler understands that it can schedule the failed pods on a different node
4. After the failed node becomes healthy, NHC deletes FenceAgentsRemediation CR, the NoExecute taint in Step 2 is removed, and the node becomes schedulable again

### FenceAgentsRemediation CR Status

The FenceAgentsRemediation CR status includes three [conditions](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-conditions): `Processing`, `FenceAgentActionSucceeded`, and `Succeeded`. Each condition has a status (true/false/unknown), a message, and a reason which indicates the state of the condition until it is met. Using these conditions we can understand better the state of the CR, and if an error occurred.
For example, see the below FenceAgentsRemediation CR status and the conditions state for a successful remediation.

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediation
metadata:
  name: NODE_NAME
spec: 
.
.
.
status:
  conditions:
    - type: Processing
      message: >-
        The unhealthy node was fully remediated (it was tainted, fenced using
        the fence agent and all the node resources have been deleted)
      reason: RemediationFinishedSuccessfully
      status: 'False'
    - type: FenceAgentActionSucceeded
      message: >-
        FAR taint was added and the fence agent command has been created and
        executed successfully
      reason: FenceAgentSucceeded
      status: 'True'   
    - type: Succeeded
      message: >-
        The unhealthy node was fully remediated (it was tainted, fenced using
        the fence agent and all the node resources have been deleted)
      reason: RemediationFinishedSuccessfully
      status: 'True'
  lastUpdateTime: '2024-01-30T10:49:46Z'
```

### FAR Remediation Events

The operator emits remediation events on the node and the remediation CR for better understanding of the remediation process.
Some important remediation events are `FenceAgentSucceeded`, and `RemediationFinished` which signifies that the fence agent command was succeeded and that the remediation was completed.
All the remediation events of FAR (as well as other Medik8s operators) has a message that begins with *[remediation]*. Therefore, to easily filter these events run `oc get events -A | awk '/\[remediation\]/ || NR==1'` to get any remediation event or `oc get events -A | awk '/\[remediation\]/ && /worker-1/ || NR==1'` for getting any remediation event for node and CR of name *worker-1*.

## Installation

There are three ways to install the operator:

* Deploy the latest version, which was built from the `main` branch, to a running Kubernetes/OpenShift cluster.
* Deploy the latest release version from the Kubernetes community, [OperatorHub.io](https://operatorhub.io/operator/fence-agents-remediation), to a running Kubernetes cluster.
* Build and deploy from sources to a running Kubernetes/OpenShift cluster.

### Deploy the latest version

After every PR is merged to the `main` branch, then the images are built and pushed to [`quay.io`](quay.io/medik8s/fence-agents-remediation-operator-bundle) (due to the [*post-submit* job](https://github.com/medik8s/fence-agents-remediation/blob/main/.github/workflows/post-submit.yaml) ).
For deployment of FAR using these images you need:

* Install `operator-sdk` binary from their [official website](https://sdk.operatorframework.io/docs/installation/#install-from-github-release).

* A running Kubernetes cluster, or an OpenShift (OCP) cluster with OLM installed. To install it on Kubernetes cluster run `operator-sdk olm install`.

* A valid `$KUBECONFIG` is configured to access your cluster.

* Run `operator-sdk run bundle quay.io/medik8s/fence-agents-remediation-operator-bundle:latest` to deploy the FAR's latest version on the current namespace.
  Another way to achieve that is running `BUNDLE_RUN_NAMESPACE=<INSTALLED_NAMESPACE> make bundle-run` to install FAR on *<INSTALLED_NAMESPACE>* namespace.

> *Note*: Installing FAR on a new namesapce (e.g., ns) requires setting some labels on the namespace prior to installing FAR:
> ```
> kubectl label --overwrite ns olm security.openshift.io/scc.podSecurityLabelSync=false
> kubectl label --overwrite ns olm pod-security.kubernetes.io/enforce=privileged
> ```

### Deploy from the Kubernetes community

Go to [OperatorHub](https://operatorhub.io/operator/fence-agents-remediation), click on Install, and follow the instructions on how to install the operator on Kubernetes.

### Build and deploy from sources

* Clone FAR repository.

* Follow OLM's [instructions](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#configure-the-operators-image-registry) on how to configure the operator's image registry (build and push the operator container).
* Run FAR in your cluster using its bundle container (similar to the [above installation](#deploy-the-latest-version), and also see [OLM's instructions](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#3-deploy-your-operator-with-olm)).

## Usage

FAR is recommended for use with NHC to automate high availability for unhealthy nodes since NHC detects unhealthy nodes and it can create an external remediation CR, e.g., FenceAgentsRemediation CR, for unhealthy nodes.
This automated way gives the responsibility on FenceAgentsRemediation CRs (creation and deletion) to NHC, even though FAR can also act as a standalone remediator, but it comes with the expense from the advanced administrator to identify the nodes' health for creating (and eventually) deleting these CRs.

Either way, a user must be familiar with the fence agent to be used. Know the fence agent parameters, and any other requirements on the cluster (e.g., fence_ipmilan needs machines that support IPMI).

### FAR with NHC

* Install [NHC](https://github.com/medik8s/node-healthcheck-operator/blob/main/docs/installation.md), and FAR using one of the above options ([Installation](#installation)).

* Create the fartemplate CR (see below example).

* Create a *NodeHealthCheck* CR that uses fartemplate as its external remediator in [RemediationTemplate](https://github.com/medik8s/node-healthcheck-operator/blob/main/docs/configuration.md#remediationtemplate) or [EscalatingRemediations](https://github.com/medik8s/node-healthcheck-operator/blob/main/docs/configuration.md#escalatingremediations).

#### Example FenceAgentsRemediationTemplate CR

The fartemplate CR is created by the administrator, and NHC can use it for creating a remediation CR, e.g. FenceAgentsRemediation.
For a better understanding please see the below example of a dummy fartemplate object:

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediationTemplate
metadata:
  name: fenceagentsremediationtemplate-default
  namespace: default
spec:
  template: {}
```

> *Note*: FenceAgentsRemediationTemplate CR must be created in the same namespace that the FAR operator has been installed.

Configuring NodeHealthCheck to use the example `fenceagentsremediationtemplate-default` template above.

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
  remediationTemplate:
    apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
    kind: FenceAgentsRemediationTemplate
    name: fenceagentsremediationtemplate-default
    namespace: default
```

NHC creates FenceAgentsRemediation CR using fartemplate after it detects an unhealthy node (according to NHC's unhealthy conditions).
FenceAgentsRemediation CRs are deleted by NHC after it detects the node is healthy again.

### Standalone FAR

* Install FAR using one of the above options ([Installation](#installation)).

* Create FenceAgentsRemediation CR with the name of the node to be remediated, the fence agent name, and its parameters.

#### Example FenceAgentsRemediation CR

The FAR CR, `FenceAgentsRemediation`, is created by the admin and is used to trigger the fence agent on a specific node.
The CR includes the following parameters:

* `agent` - fence agent name. File name which is validated (by kubebuilder and Webhook) against a list of supported agents in the FAR pod.
* `sharedparameters` - cluster wide parameters for executing the fence agent.
* `nodeparameters` - node specific parameters for executing the fence agent.
* `retrycount` - number of times to retry the fence agent in case of failure. The default is 5.
* `retryinterval` - interval between retries in seconds. The default is "5s".
* `timeout` - timeout for the fence agent in seconds. The default is "60s".
* `remediationStrategy` - either `OutOfServiceTaint` or `ResourceDeletion`:
    * `OutOfServiceTaint`: This remediation strategy implicitly causes the deletion of the pods and the detachment of the associated volumes on the node. It achieves this by placing the [`OutOfServiceTaint` taint](https://kubernetes.io/docs/reference/labels-annotations-taints/#node-kubernetes-io-out-of-service) on the node.
    * `ResourceDeletion`: This remediation strategy deletes the pods on the node.
* `sharedSecretName` - the name of the Secret containing cluster-wide parameters. Defaults to "fence-agents-credentials-shared", but can be overridden by the user.
* `nodeSecretNames` - is mapping the node name to the Secret name which contains params relevant for that node.

The FenceAgentsRemediation CR is created by the administrator and is used to trigger the fence agent on a specific node. The CR includes an *agent* field for the fence agent name, *sharedparameters* field with all the shared, not specific to a node, parameters, and a *nodeparameters* field to specify the parameters for the fenced node.
For better understanding please see the below example of FenceAgentsRemediation CR for node `worker-1` (see it also as the [sample FAR](https://github.com/medik8s/fence-agents-remediation/blob/main/config/samples/fence-agents-remediation_v1alpha1_fenceagentsremediation.yaml)):

```yaml
apiVersion: fence-agents-remediation.medik8s.io/v1alpha1
kind: FenceAgentsRemediation
metadata:
  name: worker-1
spec:
  agent: fence_ipmilan
  retrycount: 5
  retryinterval: "5s"
  timeout: "60s"
  sharedparameters:
    --username: "admin"
    --lanplus: ""
    --action: "reboot"
    --ip: "192.168.111.1"
  nodeparameters:
    --ipport:
      master-0: "6230"
      master-1: "6231"
      master-2: "6232"
      worker-0: "6233"
      worker-1: "6234"
      worker-2: "6235"
  remediationStrategy: OutOfServiceTaint
  sharedSecretName: fence-agents-credentials-shared
  nodeSecretNames:
    worker-0: fence-agents-credentials-worker0
    worker-1: fence-agents-credentials-worker1
    master-0: fence-agents-credentials-master0

```

#### Secret Support:

* You can define:

  * A **shared Secret** containing parameters used across all nodes.

  * **Node-specific Secrets**, use `nodeSecretNames` to define a mapping between each node to the secret it should consume.

* If a parameter exists in both a shared and a node Secret, the **node Secret value takes precedence**.

If a parameter is defined in both a Secret and in the `sharedparameters` or `nodeparameters` fields of the CR, a **validation error will occur** to prevent ambiguity.

#### NodeName Template Support:

Both `sharedparameters` and **shared Secret parameters** support Go template syntax for dynamic node name substitution:

* Use `{{.NodeName}}` in parameter values to substitute the actual node name at runtime.
* Templates are processed for both shared parameters and shared/node secret parameters.
* Invalid template syntax will prevent fence agent execution and log appropriate error messages.

**Examples:**

For shared parameters:
```yaml
spec:
  sharedparameters:
    --systems-uri: "/redfish/v1/Systems/{{.NodeName}}"
    --hostname: "{{.NodeName}}.example.com"
```

For shared secrets:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: fence-agents-credentials-shared
  namespace: openshift-workload-availability
type: Opaque
data:
  --systems-uri: L3JlZGZpc2gvdjEvU3lzdGVtcy97ey5Ob2RlTmFtZX19  # "/redfish/v1/Systems/{{.NodeName}}" base64 encoded
  --hostname: e3suTm9kZU5hbWV9fS5leGFtcGxlLmNvbQ==  # "{{.NodeName}}.example.com" base64 encoded
  --password: eXl5eQ==  # "yyyy" base64 encoded
```

When a FenceAgentsRemediation is processed for node `worker-1`, the templates will be resolved to:
- `--systems-uri=/redfish/v1/Systems/worker-1`
- `--hostname=worker-1.example.com`

Here is an example for a Secret without templates:
```yaml
apiVersion: v1
kind: Secret
metadata:
name: fence-agents-credentials-worker0
namespace: openshift-workload-availability
type: Opaque
data:
  --password: eXl5eQ== # "yyyy" base64 encoded

```

## Tests

### Run code checks and unit tests

Run `make test`

### Run e2e tests

1. Deploy the operator as explained above
2. (Only for AWS platforms) Run `make ocp-aws-credentials` to add sufficient [CredentialsRequest](https://github.com/medik8s/fence-agents-remediation/blob/main/config/ocp_aws/fence_aws_credentials_request.yaml).
3. Export the operator installed namespace (e.g., *openshift-workload-availability*) before running the e2e test:
 `export OPERATOR_NS=openshift-workload-availability && make test-e2e`

### Run Scorecard tests

  Run `make test-scorecard` on a running Kubernetes cluster to statically validate the operator bundle directory using [Scorecard](https://sdk.operatorframework.io/docs/testing-operators/scorecard/).

## Troubleshooting

1. Watch the FenceAgentsRemediation CR [status conditions](#fenceagentsremediation-cr-status) value, message, and reason for better understanding whether the fence agent action succeeded and the remediation completed.
2. Watch for the emitted [remediation events](#far-remediation-events) at FenceAgentsRemediation CR or the remediated node for easier identification of the remediation process.
3. Investigate FAR’s pod logs in the container *manager* (`kubectl logs -n <INSTALLED_NAMESPACE> --selector='app.kubernetes.io/name=fence-agents-remediation-operator' -c manager`).
4. Use [Medik8s's team must-gather](https://github.com/medik8s/must-gather) (for OCP only) by running `oc adm must-gather --image=quay.io/medik8s/must-gather`.
  It collects some related debug information for FAR and the rest of the Medik8s team operators.

## Help

Feel free to join our Google group to get more info - [Medik8s Google Group](https://groups.google.com/g/medik8s).
